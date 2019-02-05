package perconaservermongodb

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Percona-Lab/percona-server-mongodb-operator/clientcmd"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	"github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"
	wdMetrics "github.com/percona/mongodb-orchestration-tools/watchdog/metrics"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/psmdb/secret"
	"github.com/Percona-Lab/percona-server-mongodb-operator/version"
)

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

		watchdogMetrics: wdMetrics.NewCollector(),
		watchdogQuit:    make(chan bool, 1),

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

	pods            *podk8s.Pods
	watchdog        *watchdog.Watchdog
	watchdogMetrics *wdMetrics.Collector
	watchdogQuit    chan bool
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

	err = cr.CheckNSetDefaults(r.serverVersion.Platform)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("wrong psmdb options: %v", err)
	}

	internalKey := secret.InternalKeyMeta(cr.Name+"-intrnl-mongodb-key", cr.Namespace)
	err = setControllerReference(cr, internalKey, r.scheme)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("set owner ref for InternalKey %s: %v", internalKey.Name, err)
	}

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name + "-intrnl-mongodb-key", Namespace: cr.Namespace}, internalKey)
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

	// Setup watchdog 'k8s' pod source and CustomResourceState struct for CR
	// (https://github.com/percona/mongodb-orchestration-tools/blob/master/pkg/pod/pod.go#L51-L56)
	if r.pods == nil {
		r.pods = podk8s.NewPods(cr.Namespace)
	}
	crState := &podk8s.CustomResourceState{
		Name: cr.Name,
	}

	for i, replset := range cr.Spec.Replsets {
		// multiple replica sets is not supported until sharding is
		// added to the operator
		if i > 0 {
			reqLogger.Error(nil, "multiple replica sets is not yet supported, skipping replset %s", replset.Name)
			continue
		}

		pods := &corev1.PodList{}
		err := r.client.List(context.TODO(),
			&client.ListOptions{
				Namespace: cr.Namespace,
				LabelSelector: labels.SelectorFromSet(
					map[string]string{
						"app":                       "percona-server-mongodb",
						"percona-server-mongodb_cr": cr.Name,
						"replset":                   replset.Name,
					}),
			},
			pods,
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("get pods list for replset %s: %v", replset.Name, err)
		}

		crState.Pods = append(crState.Pods, pods.Items...)

		sfs := psmdb.NewStatefulSet(cr.Name+"-"+replset.Name, cr.Namespace)
		err = setControllerReference(cr, sfs, r.scheme)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("set owner ref for StatefulSet %s: %v", sfs.Name, err)
		}

		err = r.client.Get(context.TODO(), types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, sfs)

		if err != nil && errors.IsNotFound(err) {
			ls := map[string]string{
				"app":                       "percona-server-mongodb",
				"percona-server-mongodb_cr": cr.Name,
				"replset":                   replset.Name,
			}
			sfs.Spec, err = psmdb.StatefulSpec(cr, replset, ls, replset.Size, internalKey.Name, r.serverVersion)

			err = r.client.Create(context.TODO(), sfs)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("create StatefulSet %s: %v", sfs.Name, err)
			}
			// crState.Statefulsets = append(crState.Statefulsets, sfs)
		} else if err != nil {
			return reconcile.Result{}, fmt.Errorf("get StatefulSet %s: %v", sfs.Name, err)
		}

		// Create Service
		if replset.Expose != nil && replset.Expose.Enabled {
			_, err := r.ensureExternalServices(cr, replset, pods)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to ensure services of replset %s: %v", replset.Name, err)
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

		var rstatus api.ReplsetStatus
		if rstatus, ok := cr.Status.Replsets[replset.Name]; !ok {
			rstatus = &api.ReplsetStatus{}
			cr.Status.Replsets[replset.Name] = rstatus
		}

		if !rstatus.Initialized {
			// try making a replica set connection to the pods to
			// check if the replset was already initialized
			// session, err := mgo.DialWithInfo(getReplsetDialInfo(m, replset, podList.Items, usersSecret))
			// if err != nil {
			// 	log.Info("Cannot connect to mongodb replset %s to check initialization: %v", replset.Name, err)
			// } else {
			// 	session.Close()
			// 	rstatus.Initialized = true
			// }

			err = r.handleReplsetInit(cr, replset, pods.Items)
			if err == nil {
				rstatus.Initialized = true
			} else {
				log.WithValues("replset", replset.Name).Error(err, "Failed to init replset")
			}
		}
	}
	// Ensure the watchdog is started (to contol the MongoDB Replica Set config)
	r.ensureWatchdog(cr, secrets)

	return rr, nil
}

// ensureWatchdog ensures the PSMDB watchdog has started. This process controls the replica set and sharding
// state of a PSMDB cluster.
//
// See: https://github.com/percona/mongodb-orchestration-tools/tree/master/watchdog
//
func (r *ReconcilePerconaServerMongoDB) ensureWatchdog(cr *api.PerconaServerMongoDB, usersSecret *corev1.Secret) {
	// Skip if watchdog is started
	if r.watchdog != nil {
		return
	}

	// Skip if there are no initialized replsets
	var doStart bool
	for _, replset := range cr.Status.Replsets {
		if replset.Initialized {
			doStart = true
			break
		}
	}
	if !doStart {
		return
	}

	// Start the watchdog if it has not been started
	r.watchdog = watchdog.New(&wdConfig.Config{
		Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		APIPoll:        5 * time.Second,
		ReplsetPoll:    5 * time.Second,
		ReplsetTimeout: 3 * time.Second,
	}, r.pods, r.watchdogMetrics, r.watchdogQuit)
	go r.watchdog.Run()

	// register prometheus collector
	// prometheus.MustRegister(h.watchdogMetrics)
	// logrus.Debug("Registered watchdog Prometheus collector")
}

func setControllerReference(cr *api.PerconaServerMongoDB, obj metav1.Object, scheme *runtime.Scheme) error {
	ownerRef, err := cr.OwnerRef(scheme)
	if err != nil {
		return err
	}
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
	return nil
}

var ErrNoRunningMongodContainers = fmt.Errorf("no mongod containers in running state")

// handleReplsetInit runs the k8s-mongodb-initiator from within the first running pod's mongod container.
// This must be ran from within the running container to utilise the MongoDB Localhost Exeception.
//
// See: https://docs.mongodb.com/manual/core/security-users/#localhost-exception
//
func (r *ReconcilePerconaServerMongoDB) handleReplsetInit(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerAndPodRunning(pod, "mongod") || !isPodReady(pod) {
			continue
		}

		log.Info("Initiating replset", "replset", replset.Name, "pod", pod.Name)

		cmd := []string{
			"k8s-mongodb-initiator",
			"init",
		}

		if replset.Expose != nil && replset.Expose.Enabled {
			svc, err := r.getExtServices(m, pod.Name)
			if err != nil {
				return fmt.Errorf("failed to fetch services: %v", err)
			}
			hostname, err := psmdb.GetServiceAddr(*svc, pod, r.client)
			if err != nil {
				return fmt.Errorf("failed to fetch service address: %v", err)
			}
			cmd = append(cmd, "--ip", hostname.Host, "--port", strconv.Itoa(hostname.Port))

		}

		var errb bytes.Buffer
		err := r.clientcmd.Exec(&pod, "mongod", cmd, nil, nil, &errb, false)
		if err != nil {
			return fmt.Errorf("exec: %v /  %s", err, errb.String())
		}

		return nil
	}
	return ErrNoRunningMongodContainers
}

// isMongodPod returns a boolean reflecting if a pod
// is running a mongod container
func isMongodPod(pod corev1.Pod) bool {
	container := getPodContainer(&pod, "mongod")
	return container != nil
}

func getPodContainer(pod *corev1.Pod, containerName string) *corev1.Container {
	for _, cont := range pod.Spec.Containers {
		if cont.Name == containerName {
			return &cont
		}
	}
	return nil
}

// isContainerAndPodRunning returns a boolean reflecting if
// a container and pod are in a running state
func isContainerAndPodRunning(pod corev1.Pod, containerName string) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == containerName && container.State.Running != nil {
			return true
		}
	}
	return false
}

// isPodReady returns a boolean reflecting if a pod is in a "ready" state
func isPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		if condition.Type == corev1.PodReady {
			return true
		}
	}
	return false
}

func (r *ReconcilePerconaServerMongoDB) getExtServices(m *api.PerconaServerMongoDB, podName string) (*corev1.Service, error) {
	var retries uint64 = 0

	svcMeta := &corev1.Service{}

	for retries <= 5 {
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: podName, Namespace: m.Namespace}, svcMeta)

		if err != nil {
			if errors.IsNotFound(err) {
				retries += 1
				time.Sleep(500 * time.Millisecond)
				log.Info("Service for %s not found. Retry", podName)
				continue
			}
			return nil, fmt.Errorf("failed to fetch service: %v", err)
		}
		return svcMeta, nil
	}
	return nil, fmt.Errorf("failed to fetch service. Retries limit reached")
}
