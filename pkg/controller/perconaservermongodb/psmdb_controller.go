package perconaservermongodb

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	cm "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha1"
	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
	"github.com/percona/percona-server-mongodb-operator/version"
)

var secretFileMode int32 = 288
var log = logf.Log.WithName("controller_psmdb")

// Add creates a new PerconaServerMongoDB Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	r, err := newReconciler(mgr)
	if err != nil {
		return err
	}

	return add(mgr, r)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	sv, err := version.Server()
	if err != nil {
		return nil, fmt.Errorf("get server version: %v", err)
	}

	log.Info("server version", "platform", sv.Platform, "version", sv.Info)

	cli, err := clientcmd.NewClient()
	if err != nil {
		return nil, fmt.Errorf("create clientcmd: %v", err)
	}

	return &ReconcilePerconaServerMongoDB{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		serverVersion: sv,
		reconcileIn:   time.Second * 5,

		clientcmd: cli,
	}, nil
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("psmdb-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource PerconaServerMongoDB
	err = c.Watch(&source.Kind{Type: &api.PerconaServerMongoDB{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDB{}

// ReconcilePerconaServerMongoDB reconciles a PerconaServerMongoDB object
type ReconcilePerconaServerMongoDB struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme

	clientcmd     *clientcmd.Client
	serverVersion *version.ServerVersion
	reconcileIn   time.Duration
}

// Reconcile reads that state of the cluster for a PerconaServerMongoDB object and makes changes based on the state read
// and what is in the PerconaServerMongoDB.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDB) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling PerconaServerMongoDB")

	rr := reconcile.Result{
		RequeueAfter: r.reconcileIn,
	}

	// Fetch the PerconaServerMongoDB instance
	cr := &api.PerconaServerMongoDB{}
	err := r.client.Get(context.TODO(), request.NamespacedName, cr)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return rr, err
	}

	err = cr.CheckNSetDefaults(r.serverVersion.Platform, log)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("wrong psmdb options: %v", err)
	}

	if !cr.Spec.UnsafeConf {
		err = r.reconsileSSL(cr)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf(`TLS secrets handler: "%v". Please create your TLS secrets `+cr.Spec.SSLSecretName+` and `+cr.Spec.SSLInternalSecretName+` manually or setup cert-manager correctly`, err)
		}
	}

	internalKey := secret.InternalKeyMeta(cr.Name+"-mongodb-keyfile", cr.Namespace)
	err = setControllerReference(cr, internalKey, r.scheme)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("set owner ref for InternalKey %s: %v", internalKey.Name, err)
	}

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name + "-mongodb-keyfile", Namespace: cr.Namespace}, internalKey)
	if err != nil && errors.IsNotFound(err) {
		reqLogger.Info("Creating a new internal mongo key", "Namespace", cr.Namespace, "Name", internalKey.Name)

		internalKey.Data, err = secret.GenInternalKey()
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("internal mongodb key generation: %v", err)
		}

		err = r.client.Create(context.TODO(), internalKey)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("create internal mongodb key: %v", err)
		}
	} else if err != nil {
		return reconcile.Result{}, fmt.Errorf("get internal mongodb key: %v", err)
	}

	secrets := &corev1.Secret{}
	err = r.client.Get(
		context.TODO(),
		types.NamespacedName{Name: cr.Spec.Secrets.Users, Namespace: cr.Namespace},
		secrets,
	)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("get mongodb secrets: %v", err)
	}

	bcpSfs, err := r.reconcileBackupCoordinator(cr)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("reconcile backup coordinator: %v", err)
	}

	if cr.Spec.Backup.Enabled {
		err = r.reconcileBackupStorageConfig(cr, bcpSfs)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("reconcile backup storage config: %v", err)
		}

		err = r.reconcileBackupTasks(cr, bcpSfs)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("reconcile backup tasks: %v", err)
		}
	}

	for i, replset := range cr.Spec.Replsets {
		// multiple replica sets is not supported until sharding is
		// added to the operator
		if i > 0 {
			reqLogger.Error(nil, "multiple replica sets is not yet supported, skipping replset %s", replset.Name)
			continue
		}

		matchLabels := map[string]string{
			"app.kubernetes.io/name":       "percona-server-mongodb",
			"app.kubernetes.io/instance":   cr.Name,
			"app.kubernetes.io/replset":    replset.Name,
			"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
			"app.kubernetes.io/part-of":    "percona-server-mongodb",
		}

		pods := &corev1.PodList{}
		err := r.client.List(context.TODO(),
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(matchLabels),
			},
			pods,
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("get pods list for replset %s: %v", replset.Name, err)
		}

		_, err = r.reconcileStatefulSet(false, cr, replset, matchLabels, internalKey.Name)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("reconcile StatefulSet for %s: %v", replset.Name, err)
		}

		if replset.Arbiter.Enabled {
			_, err := r.reconcileStatefulSet(true, cr, replset, matchLabels, internalKey.Name)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("reconcile Arbiter StatefulSet for %s: %v", replset.Name, err)
			}
		} else {
			err := r.client.Delete(context.TODO(), psmdb.NewStatefulSet(
				cr.Name+"-"+replset.Name+"-arbiter",
				cr.Namespace,
			))

			if err != nil && !errors.IsNotFound(err) {
				return reconcile.Result{}, fmt.Errorf("delete arbiter in replset %s: %v", replset.Name, err)
			}
		}

		err = r.removeOudatedServices(cr, replset, pods)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to remove old services of replset %s: %v", replset.Name, err)
		}

		// Create Service
		if replset.Expose.Enabled {
			srvs, err := r.ensureExternalServices(cr, replset, pods)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to ensure services of replset %s: %v", replset.Name, err)
			}
			if replset.Expose.ExposeType == corev1.ServiceTypeLoadBalancer {
				lbsvc := srvs[:0]
				for _, svc := range srvs {
					if len(svc.Status.LoadBalancer.Ingress) > 0 {
						lbsvc = append(lbsvc, svc)
					}
				}
				srvs = lbsvc
			}
		} else {
			service := psmdb.Service(cr, replset)

			err = setControllerReference(cr, service, r.scheme)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("set owner ref for Service %s: %v", service.Name, err)
			}

			err = r.client.Create(context.TODO(), service)
			if err != nil && !errors.IsAlreadyExists(err) {
				return reconcile.Result{}, fmt.Errorf("failed to create service for replset %s: %v", replset.Name, err)
			}
		}

		var rstatus *api.ReplsetStatus
		rstatus, ok := cr.Status.Replsets[replset.Name]
		if !ok || rstatus == nil {
			rstatus = &api.ReplsetStatus{Name: replset.Name}
			cr.Status.Replsets[replset.Name] = rstatus
		}

		err = r.reconcileCluster(cr, replset, *pods, secrets)
		if err != nil {
			reqLogger.Error(err, "failed to reconcile cluster", "replset", replset.Name)
		}
	}

	err = r.client.Update(context.TODO(), cr)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("update psmdb status: %v", err)
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDB) reconsileSSL(cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	err := r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.SSLSecretName,
		},
		&secretObj,
	)
	if err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("get secret: %v", err)
	}

	issuerKind := "ClusterIssuer"
	issuerName := cr.Name + "-psmdb-ca"
	var certificateDNSNames []string
	for _, replset := range cr.Spec.Replsets {
		certificateDNSNames = append(certificateDNSNames,
			cr.Name+"-"+replset.Name,
			"*."+cr.Name+"-"+replset.Name,
		)
	}

	err = r.client.Create(context.TODO(), &cm.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      issuerName,
			Namespace: cr.Namespace,
		},
		Spec: cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				SelfSigned: &cm.SelfSignedIssuer{},
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create issuer: %v", err)
	}

	err = r.client.Create(context.TODO(), &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-ssl",
			Namespace: cr.Namespace,
		},
		Spec: cm.CertificateSpec{
			SecretName: cr.Spec.SSLSecretName,
			CommonName: cr.Name,
			DNSNames:   certificateDNSNames,
			IsCA:       true,
			IssuerRef: cm.ObjectReference{
				Name: issuerName,
				Kind: issuerKind,
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create certificate: %v", err)
	}
	if cr.Spec.SSLSecretName == cr.Spec.SSLInternalSecretName {
		return nil
	}

	err = r.client.Create(context.TODO(), &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-ssl-internal",
			Namespace: cr.Namespace,
		},
		Spec: cm.CertificateSpec{
			SecretName: cr.Spec.SSLInternalSecretName,
			CommonName: cr.Name,
			DNSNames:   certificateDNSNames,
			IsCA:       true,
			IssuerRef: cm.ObjectReference{
				Name: issuerName,
				Kind: issuerKind,
			},
		},
	})
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create internal certificate: %v", err)
	}

	return nil
}

// TODO: reduce cyclomatic complexity
func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(arbiter bool, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, matchLabels map[string]string, internalKeyName string) (*appsv1.StatefulSet, error) {
	sfsName := cr.Name + "-" + replset.Name
	size := replset.Size
	containerName := "mongod"
	matchLabels["app.kubernetes.io/component"] = "mongod"
	multiAZ := replset.MultiAZ
	pdbspec := replset.PodDisruptionBudget
	if arbiter {
		sfsName += "-arbiter"
		containerName += "-arbiter"
		size = replset.Arbiter.Size
		matchLabels["app.kubernetes.io/component"] = "arbiter"
		multiAZ = replset.Arbiter.MultiAZ
		pdbspec = replset.Arbiter.PodDisruptionBudget
	}

	sfs := psmdb.NewStatefulSet(sfsName, cr.Namespace)
	err := setControllerReference(cr, sfs, r.scheme)
	if err != nil {
		return nil, fmt.Errorf("set owner ref for StatefulSet %s: %v", sfs.Name, err)
	}

	errGet := r.client.Get(context.TODO(), types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, sfs)
	if errGet != nil && !errors.IsNotFound(errGet) {
		return nil, fmt.Errorf("get StatefulSet %s: %v", sfs.Name, err)
	}

	sfsSpec, err := psmdb.StatefulSpec(cr, replset, containerName, matchLabels, multiAZ, size, internalKeyName, r.serverVersion)
	if err != nil {
		return nil, fmt.Errorf("create StatefulSet.Spec %s: %v", sfs.Name, err)
	}

	// add TLS/SSL Volume
	sfsSpec.Template.Spec.Volumes = append(sfsSpec.Template.Spec.Volumes,
		corev1.Volume{
			Name: "ssl",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.SSLSecretName,
					Optional:    &cr.Spec.UnsafeConf,
					DefaultMode: &secretFileMode,
				},
			},
		},
		corev1.Volume{
			Name: "ssl-internal",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.SSLInternalSecretName,
					Optional:    &cr.Spec.UnsafeConf,
					DefaultMode: &secretFileMode,
				},
			},
		},
	)

	if arbiter {
		sfsSpec.Template.Spec.Volumes = append(sfsSpec.Template.Spec.Volumes,
			corev1.Volume{
				Name: psmdb.MongodDataVolClaimName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)
	} else {
		if replset.VolumeSpec.PersistentVolumeClaim != nil {
			sfsSpec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
				psmdb.PersistentVolumeClaim(psmdb.MongodDataVolClaimName, cr.Namespace, replset.VolumeSpec.PersistentVolumeClaim),
			}
		} else {
			sfsSpec.Template.Spec.Volumes = append(sfsSpec.Template.Spec.Volumes,
				corev1.Volume{
					Name: psmdb.MongodDataVolClaimName,
					VolumeSource: corev1.VolumeSource{
						HostPath: replset.VolumeSpec.HostPath,
						EmptyDir: replset.VolumeSpec.EmptyDir,
					},
				},
			)
		}

		if cr.Spec.Backup.Enabled {
			sfsSpec.Template.Spec.Containers = append(sfsSpec.Template.Spec.Containers, backup.AgentContainer(cr, r.serverVersion))
			sfsSpec.Template.Spec.Volumes = append(sfsSpec.Template.Spec.Volumes, backup.AgentVolume(cr.Name))
		}

		if cr.Spec.PMM.Enabled {
			sfsSpec.Template.Spec.Containers = append(sfsSpec.Template.Spec.Containers, psmdb.PMMContainer(cr.Spec.PMM, cr.Spec.Secrets.Users))
		}
	}

	if errors.IsNotFound(errGet) {
		sfs.Spec = sfsSpec
		err = r.client.Create(context.TODO(), sfs)
		if err != nil && !errors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("create StatefulSet %s: %v", sfs.Name, err)
		}
	} else {
		err := r.reconcilePDB(pdbspec, matchLabels, cr.Namespace, sfs)
		if err != nil {
			return nil, fmt.Errorf("PodDisruptionBudget for %s: %v", sfs.Name, err)
		}

		sfs.Spec.Replicas = &size
		sfs.Spec.Template.Spec.Containers = sfsSpec.Template.Spec.Containers
		sfs.Spec.Template.Spec.Volumes = sfsSpec.Template.Spec.Volumes
		err = r.client.Update(context.TODO(), sfs)
		if err != nil {
			return nil, fmt.Errorf("update StatefulSet %s: %v", sfs.Name, err)
		}
	}

	return sfs, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcilePDB(spec *api.PodDisruptionBudgetSpec, labels map[string]string, namespace string, owner runtime.Object) error {
	if spec == nil {
		return nil
	}

	pdb := psmdb.PodDisruptionBudget(spec, labels, namespace)
	err := setControllerReference(owner, pdb, r.scheme)
	if err != nil {
		return fmt.Errorf("set owner reference: %v", err)
	}

	cpdb := &policyv1beta1.PodDisruptionBudget{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: pdb.Name, Namespace: namespace}, cpdb)
	if err != nil && errors.IsNotFound(err) {
		return r.client.Create(context.TODO(), pdb)
	} else if err != nil {
		return fmt.Errorf("get: %v", err)
	}

	cpdb.Spec = pdb.Spec
	return r.client.Update(context.TODO(), cpdb)
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdate(currentObj runtime.Object, name, namespace string) error {
	ctx := context.TODO()

	foundObj := currentObj.DeepCopyObject()
	err := r.client.Get(ctx,
		types.NamespacedName{Name: name, Namespace: namespace},
		foundObj)

	if err != nil && errors.IsNotFound(err) {
		err := r.client.Create(ctx, currentObj)
		if err != nil {
			return fmt.Errorf("create: %v", err)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("get: %v", err)
	}

	currentObj.GetObjectKind().SetGroupVersionKind(foundObj.GetObjectKind().GroupVersionKind())
	err = r.client.Update(ctx, currentObj)
	if err != nil {
		return fmt.Errorf("update: %v", err)
	}

	return nil
}

func setControllerReference(owner runtime.Object, obj metav1.Object, scheme *runtime.Scheme) error {
	ownerRef, err := OwnerRef(owner, scheme)
	if err != nil {
		return err
	}
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
	return nil
}

// OwnerRef returns OwnerReference to object
func OwnerRef(ro runtime.Object, scheme *runtime.Scheme) (metav1.OwnerReference, error) {
	gvk, err := apiutil.GVKForObject(ro, scheme)
	if err != nil {
		return metav1.OwnerReference{}, err
	}

	trueVar := true

	ca, err := meta.Accessor(ro)
	if err != nil {
		return metav1.OwnerReference{}, err
	}

	return metav1.OwnerReference{
		APIVersion: gvk.GroupVersion().String(),
		Kind:       gvk.Kind,
		Name:       ca.GetName(),
		UID:        ca.GetUID(),
		Controller: &trueVar,
	}, nil
}
