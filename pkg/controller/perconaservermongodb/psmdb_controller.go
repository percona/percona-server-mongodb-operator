package perconaservermongodb

import (
	"context"
	"fmt"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	"github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"
	wdMetrics "github.com/percona/mongodb-orchestration-tools/watchdog/metrics"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	return &ReconcilePerconaServerMongoDB{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		serverVersion: sv,
		reconcileIn:   time.Second * 5,

		watchdogMetrics: wdMetrics.NewCollector(),
		watchdogQuit:    make(chan bool, 1),
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

	// internalKey := secret.InternalKey(cr.Name+"-mongodb-key", cr.Namespace)
	internalKey := &corev1.Secret{}

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name + "-mongodb-key", Namespace: cr.Namespace}, internalKey)
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
func (r *ReconcilePerconaServerMongoDB) ensureWatchdog(psmdb *api.PerconaServerMongoDB, usersSecret *corev1.Secret) {
	// Skip if watchdog is started
	if r.watchdog != nil {
		return
	}

	// Skip if there are no initialized replsets
	var doStart bool
	for _, replset := range psmdb.Status.Replsets {
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
