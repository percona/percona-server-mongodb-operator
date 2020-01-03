package perconaservermongodb

import (
	"context"
	"crypto/md5"
	"fmt"
	"time"

	"github.com/pkg/errors"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return rr, err
	}

	defer func() {
		r.updateStatus(cr, err)
	}()

	err = cr.CheckNSetDefaults(r.serverVersion.Platform, log)
	if err != nil {
		err = errors.Wrap(err, "wrong psmdb options")
		return reconcile.Result{}, err
	}
	err = r.reconcileUsersSecret(cr)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("reconcile users secret: %v", err)
	}

	if !cr.Spec.UnsafeConf {
		err = r.reconsileSSL(cr)
		if err != nil {
			err = errors.Errorf(`TLS secrets handler: "%v". Please create your TLS secret `+cr.Spec.Secrets.SSL+` manually or setup cert-manager correctly`, err)
			return reconcile.Result{}, err
		}
	}

	internalKey := cr.Name + "-mongodb-keyfile"
	ikCreated, err := r.ensureSecurityKey(cr, internalKey, "mongodb-key", 768, true)
	if err != nil {
		err = errors.Wrapf(err, "ensure mongo Key %s", internalKey)
		return reconcile.Result{}, err
	}
	if ikCreated {
		reqLogger.Info("Created a new mongo key", "KeyName", internalKey)
	}

	if *cr.Spec.Mongod.Security.EnableEncryption {
		created, err := r.ensureSecurityKey(cr, cr.Spec.Mongod.Security.EncryptionKeySecret, psmdb.EncryptionKeyName, 32, false)
		if err != nil {
			err = errors.Wrapf(err, "ensure mongo Key %s", cr.Spec.Mongod.Security.EncryptionKeySecret)
			return reconcile.Result{}, err
		}
		if created {
			reqLogger.Info("Created a new mongo key", "KeyName", cr.Spec.Mongod.Security.EncryptionKeySecret)
		}
	}

	secrets := &corev1.Secret{}
	err = r.client.Get(
		context.TODO(),
		types.NamespacedName{Name: cr.Spec.Secrets.Users, Namespace: cr.Namespace},
		secrets,
	)
	if err != nil {
		err = errors.Wrap(err, "get mongodb secrets")
		return reconcile.Result{}, err
	}

	bcpSfs, err := r.reconcileBackupCoordinator(cr)
	if err != nil {
		err = errors.Wrap(err, "reconcile backup coordinator")
		return reconcile.Result{}, err
	}

	if cr.Spec.Backup.Enabled {
		err = r.reconcileBackupStorageConfig(cr, bcpSfs)
		if err != nil {
			err = errors.Wrap(err, "reconcile backup storage config")
			return reconcile.Result{}, err
		}

		err = r.reconcileBackupTasks(cr, bcpSfs)
		if err != nil {
			err = errors.Wrap(err, "reconcile backup tasks")
			return reconcile.Result{}, err
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
			err = errors.Errorf("get pods list for replset %s: %v", replset.Name, err)
			return reconcile.Result{}, err
		}

		_, err = r.reconcileStatefulSet(false, cr, replset, matchLabels, internalKey)
		if err != nil {
			err = errors.Errorf("reconcile StatefulSet for %s: %v", replset.Name, err)
			return reconcile.Result{}, err
		}

		if replset.Arbiter.Enabled {
			_, err := r.reconcileStatefulSet(true, cr, replset, matchLabels, internalKey)
			if err != nil {
				err = errors.Errorf("reconcile Arbiter StatefulSet for %s: %v", replset.Name, err)
				return reconcile.Result{}, err
			}
		} else {
			err := r.client.Delete(context.TODO(), psmdb.NewStatefulSet(
				cr.Name+"-"+replset.Name+"-arbiter",
				cr.Namespace,
			))

			if err != nil && !k8serrors.IsNotFound(err) {
				err = errors.Errorf("delete arbiter in replset %s: %v", replset.Name, err)
				return reconcile.Result{}, err
			}
		}

		err = r.removeOudatedServices(cr, replset, pods)
		if err != nil {
			err = errors.Errorf("failed to remove old services of replset %s: %v", replset.Name, err)
			return reconcile.Result{}, err
		}

		// Create Service
		if replset.Expose.Enabled {
			srvs, err := r.ensureExternalServices(cr, replset, pods)
			if err != nil {
				err = errors.Errorf("failed to ensure services of replset %s: %v", replset.Name, err)
				return reconcile.Result{}, err
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
				err = errors.Errorf("set owner ref for Service %s: %v", service.Name, err)
				return reconcile.Result{}, err
			}

			err = r.client.Get(context.TODO(), types.NamespacedName{Name: service.Name, Namespace: service.Namespace}, &corev1.Service{})
			if err != nil && k8serrors.IsNotFound(err) {
				err := r.client.Create(context.TODO(), service)
				if err != nil {
					return reconcile.Result{}, errors.Errorf("failed to create service for replset %s: %v", replset.Name, err)
				}
			} else if err != nil {
				return reconcile.Result{}, errors.Errorf("failed to check service for replset %s: %v", replset.Name, err)
			}
		}

		_, ok := cr.Status.Replsets[replset.Name]
		if !ok {
			cr.Status.Replsets[replset.Name] = &api.ReplsetStatus{}
		}

		err = r.reconcileCluster(cr, replset, *pods, secrets)
		if err != nil {
			reqLogger.Error(err, "failed to reconcile cluster", "replset", replset.Name)
		}
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDB) ensureSecurityKey(cr *api.PerconaServerMongoDB, secretName, keyName string, keyLen int, setOwner bool) (created bool, err error) {
	key := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cr.Namespace,
		},
	}

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, key)
	if err != nil && k8serrors.IsNotFound(err) {
		created = true
		if setOwner {
			err = setControllerReference(cr, key, r.scheme)
			if err != nil {
				return false, errors.Wrap(err, "set owner ref")
			}
		}

		key.Data = make(map[string][]byte)
		key.Data[keyName], err = secret.GenerateKey1024(keyLen)
		if err != nil {
			return false, errors.Wrap(err, "key generation")
		}

		err = r.client.Create(context.TODO(), key)
		if err != nil {
			return false, errors.Wrap(err, "create key")
		}
	} else if err != nil {
		return false, errors.Wrap(err, "get key")
	}

	return created, nil
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
	if errGet != nil && !k8serrors.IsNotFound(errGet) {
		return nil, fmt.Errorf("get StatefulSet %s: %v", sfs.Name, err)
	}

	sfsSpec, err := psmdb.StatefulSpec(cr, replset, containerName, matchLabels, multiAZ, size, internalKeyName)
	if err != nil {
		return nil, fmt.Errorf("create StatefulSet.Spec %s: %v", sfs.Name, err)
	}

	// add TLS/SSL Volume
	t := true
	sfsSpec.Template.Spec.Volumes = append(sfsSpec.Template.Spec.Volumes,
		corev1.Volume{
			Name: "ssl",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.Secrets.SSL,
					Optional:    &cr.Spec.UnsafeConf,
					DefaultMode: &secretFileMode,
				},
			},
		},
		corev1.Volume{
			Name: "ssl-internal",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.Secrets.SSLInternal,
					Optional:    &t,
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
			sfsSpec.Template.Spec.Containers = append(sfsSpec.Template.Spec.Containers, backup.AgentContainer(cr))
			sfsSpec.Template.Spec.Volumes = append(sfsSpec.Template.Spec.Volumes, backup.AgentVolume(cr.Name))
		}

		if cr.Spec.PMM.Enabled {
			pmmsec := corev1.Secret{}
			err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Spec.Secrets.Users, Namespace: cr.Namespace}, &pmmsec)
			if err != nil {
				return nil, fmt.Errorf("check pmm secrets: %v", err)
			}

			_, okl := pmmsec.Data[psmdb.PMMUserKey]
			_, okp := pmmsec.Data[psmdb.PMMPasswordKey]
			is120, err := cr.VersionGreaterThanOrEqual("1.2.0")
			if err != nil {
				return nil, fmt.Errorf("check version error: %v", err)
			}

			pmmC := psmdb.PMMContainer(cr.Spec.PMM, cr.Spec.Secrets.Users, okl && okp, cr.Name, is120)
			if is120 {
				res, err := psmdb.CreateResources(cr.Spec.PMM.Resources)
				if err != nil {
					return nil, fmt.Errorf("pmm container error: create resources error: %v", err)
				}
				pmmC.Resources = res
			}
			sfsSpec.Template.Spec.Containers = append(
				sfsSpec.Template.Spec.Containers,
				pmmC,
			)
		}
	}

	sfsSpec.UpdateStrategy.Type = cr.Spec.UpdateStrategy

	sslHash, err := r.getTLSHash(cr, cr.Spec.Secrets.SSL)
	if err != nil {
		return nil, fmt.Errorf("get secret hash error: %v", err)
	}
	if len(sfsSpec.Template.Annotations) == 0 {
		sfsSpec.Template.Annotations = make(map[string]string)
	}
	is110, err := cr.VersionGreaterThanOrEqual("1.1.0")
	if err != nil {
		return nil, fmt.Errorf("detect version error: %v", err)
	}
	if is110 {
		sfsSpec.Template.Annotations["percona.com/ssl-hash"] = sslHash
	}
	sslInternalHash, err := r.getTLSHash(cr, cr.Spec.Secrets.SSLInternal)
	if err != nil && !k8serrors.IsNotFound(err) {
		return nil, fmt.Errorf("get secret hash error: %v", err)
	} else if err == nil {
		if is110 {
			sfsSpec.Template.Annotations["percona.com/ssl-internal-hash"] = sslInternalHash
		}
	}

	sfs.Spec = sfsSpec
	if k8serrors.IsNotFound(errGet) {
		err = r.client.Create(context.TODO(), sfs)
		if err != nil && !k8serrors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("create StatefulSet %s: %v", sfs.Name, err)
		}
	} else {
		err := r.reconcilePDB(pdbspec, matchLabels, cr.Namespace, sfs)
		if err != nil {
			return nil, fmt.Errorf("PodDisruptionBudget for %s: %v", sfs.Name, err)
		}
		sfs.Spec.Replicas = &size
		err = r.client.Update(context.TODO(), sfs)
		if err != nil {
			return nil, fmt.Errorf("update StatefulSet %s: %v", sfs.Name, err)
		}
	}

	return sfs, nil
}

func (r *ReconcilePerconaServerMongoDB) getTLSHash(cr *api.PerconaServerMongoDB, secretName string) (string, error) {
	if cr.Spec.UnsafeConf {
		return "", nil
	}
	secretObj := corev1.Secret{}
	err := r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      secretName,
		},
		&secretObj,
	)
	if err != nil {
		return "", err
	}
	secretString := fmt.Sprintln(secretObj.Data)
	hash := fmt.Sprintf("%x", md5.Sum([]byte(secretString)))

	return hash, nil
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
	if err != nil && k8serrors.IsNotFound(err) {
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

	if err != nil && k8serrors.IsNotFound(err) {
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
