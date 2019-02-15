package perconaservermongodb

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"time"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	podk8s "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	"github.com/percona/mongodb-orchestration-tools/watchdog"
	wdConfig "github.com/percona/mongodb-orchestration-tools/watchdog/config"
	wdMetrics "github.com/percona/mongodb-orchestration-tools/watchdog/metrics"
	"gopkg.in/mgo.v2"
	appsv1 "k8s.io/api/apps/v1"
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

	"github.com/Percona-Lab/percona-server-mongodb-operator/clientcmd"
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

		watchdog: make(map[string]*watchdog.Watchdog),

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
	watchdog        map[string]*watchdog.Watchdog
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

	err = cr.CheckNSetDefaults(r.serverVersion.Platform, log)
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

		matchLables := map[string]string{
			"app":                       "percona-server-mongodb",
			"percona-server-mongodb_cr": cr.Name,
			"replset":                   replset.Name,
		}

		pods := &corev1.PodList{}
		err := r.client.List(context.TODO(),
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(matchLables),
			},
			pods,
		)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("get pods list for replset %s: %v", replset.Name, err)
		}

		crState.Pods = append(crState.Pods, pods.Items...)

		sfs, err := r.reconcileStatefulSet(false, cr, replset, matchLables, internalKey.Name)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("reconcile StatefulSet for %s: %v", replset.Name, err)
		}

		crState.Statefulsets = append(crState.Statefulsets, *sfs)

		if replset.Arbiter.Enabled {
			arbiterSfs, err := r.reconcileStatefulSet(true, cr, replset, matchLables, internalKey.Name)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("reconcile Arbiter StatefulSet for %s: %v", replset.Name, err)
			}

			crState.Statefulsets = append(crState.Statefulsets, *arbiterSfs)
		}

		// Create Service
		if replset.Expose.Enabled {
			crState.ServicesExpose = true
			srvs, err := r.ensureExternalServices(cr, replset, pods)
			if err != nil {
				return reconcile.Result{}, fmt.Errorf("failed to ensure services of replset %s: %v", replset.Name, err)
			}
			crState.Services = append(crState.Services, srvs...)
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

			crState.Services = append(crState.Services, *service)
		}

		var rstatus *api.ReplsetStatus
		rstatus, ok := cr.Status.Replsets[replset.Name]
		if !ok || rstatus == nil {
			rstatus = &api.ReplsetStatus{Name: replset.Name}
			cr.Status.Replsets[replset.Name] = rstatus
		}

		if !r.rsetInitialized(cr, replset, *pods, secrets) {
			err = r.handleReplsetInit(cr, replset, pods.Items)
			if err == nil {
				rstatus.Initialized = true
			} else {
				log.WithValues("replset", replset.Name).Error(err, "Failed to init replset")
			}
		}
	}

	err = r.client.Update(context.TODO(), cr)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("update psmdb status: %v", err)
	}
	// Ensure the watchdog is started (to contol the MongoDB Replica Set config)
	r.ensureWatchdog(cr, secrets)

	r.pods.Update(crState)

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(arbiter bool, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, matchLables map[string]string, internalKeyName string) (*appsv1.StatefulSet, error) {
	sfsName := cr.Name + "-" + replset.Name
	size := replset.Size
	containerName := "mongod"
	if arbiter {
		sfsName += "-arbiter"
		containerName += "-arbiter"
		size = replset.Arbiter.Size
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

	sfsSpec, err := psmdb.StatefulSpec(cr, replset, containerName, matchLables, size, internalKeyName, r.serverVersion)
	if err != nil {
		return nil, fmt.Errorf("create StatefulSet.Spec %s: %v", sfs.Name, err)
	}

	if !arbiter {
		pvc, err := psmdb.PersistentVolumeClaim(psmdb.MongodDataVolClaimName, cr.Namespace, replset)
		if err != nil {
			return nil, fmt.Errorf("pvc create: %v", err)
		}
		sfsSpec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
			pvc,
		}

		if len(sfsSpec.Template.Spec.Containers) > 0 {
			sfsSpec.Template.Spec.Containers[0].VolumeMounts = append(
				sfsSpec.Template.Spec.Containers[0].VolumeMounts,
				corev1.VolumeMount{
					Name:      psmdb.MongodDataVolClaimName,
					MountPath: psmdb.MongodContainerDataDir,
				},
			)
		}
	}

	if errors.IsNotFound(errGet) {
		sfs.Spec = sfsSpec
		err = r.client.Create(context.TODO(), sfs)
		if err != nil && !errors.IsAlreadyExists(err) {
			return nil, fmt.Errorf("create StatefulSet %s: %v", sfs.Name, err)
		}
	} else {
		sfs.Spec.Replicas = &size
		sfs.Spec.Template.Spec.Containers = sfsSpec.Template.Spec.Containers
		err = r.client.Update(context.TODO(), sfs)
		if err != nil {
			return nil, fmt.Errorf("update StatefulSet %s: %v", sfs.Name, err)
		}
	}

	return sfs, nil
}

func (r *ReconcilePerconaServerMongoDB) rsetInitialized(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods corev1.PodList, usersSecret *corev1.Secret) bool {
	session, err := mgo.DialWithInfo(r.getReplsetDialInfo(cr, replset, pods.Items, usersSecret))
	if err != nil {
		// log.Info("Cannot connect to mongodb replset %s to check initialization: %v", replset.Name, err)
		return false
	}

	session.Close()
	return true
}

// getReplsetDialInfo returns a *mgo.Session configured to connect (with auth) to a Pod MongoDB
func (r *ReconcilePerconaServerMongoDB) getReplsetDialInfo(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) *mgo.DialInfo {
	return &mgo.DialInfo{
		Addrs:          r.getReplsetAddrs(m, replset, pods),
		ReplicaSetName: replset.Name,
		Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		Timeout:        3 * time.Second,
		FailFast:       true,
	}
}

// getReplsetAddrs returns a slice of replset host:port addresses
func (r *ReconcilePerconaServerMongoDB) getReplsetAddrs(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods []corev1.Pod) []string {
	addrs := make([]string, 0)
	var hostname string

	if replset.Expose.Enabled {
		for _, pod := range pods {
			svc, err := r.getExtServices(m, pod.Name)
			if err != nil {
				log.Error(err, "failed to fetch service address")
				continue
			}
			hostname, err := psmdb.GetServiceAddr(*svc, pod, r.client)
			if err != nil {
				log.Error(err, "failed to get service hostname")
				continue
			}
			addrs = append(addrs, hostname.String())
		}
	} else {
		for _, pod := range pods {
			hostname = podk8s.GetMongoHost(pod.Name, m.Name, replset.Name, m.Namespace)
			addrs = append(addrs, hostname+":"+strconv.Itoa(int(m.Spec.Mongod.Net.Port)))
		}
	}
	return addrs
}

// ensureWatchdog ensures the PSMDB watchdog has started. This process controls the replica set and sharding
// state of a PSMDB cluster.
//
// See: https://github.com/percona/mongodb-orchestration-tools/tree/master/watchdog
//
func (r *ReconcilePerconaServerMongoDB) ensureWatchdog(cr *api.PerconaServerMongoDB, usersSecret *corev1.Secret) {
	// Skip if watchdog is started
	if _, ok := r.watchdog[cr.Name]; ok {
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
	r.watchdog[cr.Name] = watchdog.New(&wdConfig.Config{
		Username:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[motPkg.EnvMongoDBClusterAdminPassword]),
		APIPoll:        5 * time.Second,
		ReplsetPoll:    5 * time.Second,
		ReplsetTimeout: 3 * time.Second,
	}, r.pods, r.watchdogMetrics, r.watchdogQuit)
	go r.watchdog[cr.Name].Run()

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

		if replset.Expose.Enabled {
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
