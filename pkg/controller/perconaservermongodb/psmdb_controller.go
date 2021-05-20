package perconaservermongodb

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v "github.com/hashicorp/go-version"
	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
	"github.com/percona/percona-server-mongodb-operator/version"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
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
		return nil, errors.Wrap(err, "get server version")
	}

	log.Info("server version", "platform", sv.Platform, "version", sv.Info)

	cli, err := clientcmd.NewClient()
	if err != nil {
		return nil, errors.Wrap(err, "create clientcmd")
	}

	return &ReconcilePerconaServerMongoDB{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		serverVersion: sv,
		reconcileIn:   time.Second * 5,
		crons:         NewCronRegistry(),
		lockers:       newLockStore(),

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

type CronRegistry struct {
	crons *cron.Cron
	jobs  map[string]Shedule
}

type Shedule struct {
	ID          int
	CronShedule string
}

func NewCronRegistry() CronRegistry {
	c := CronRegistry{
		crons: cron.New(),
		jobs:  make(map[string]Shedule),
	}

	c.crons.Start()

	return c
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDB{}

// ReconcilePerconaServerMongoDB reconciles a PerconaServerMongoDB object
type ReconcilePerconaServerMongoDB struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme

	crons         CronRegistry
	clientcmd     *clientcmd.Client
	serverVersion *version.ServerVersion
	reconcileIn   time.Duration

	lockers lockStore
}

type lockStore struct {
	store *sync.Map
}

func newLockStore() lockStore {
	return lockStore{
		store: new(sync.Map),
	}
}

func (l lockStore) LoadOrCreate(key string) lock {
	val, _ := l.store.LoadOrStore(key, lock{
		statusMutex: new(sync.Mutex),
		updateSync:  new(int32),
	})

	return val.(lock)
}

type lock struct {
	statusMutex *sync.Mutex
	updateSync  *int32
}

const (
	updateDone = 0
	updateWait = 1
)

// Reconcile reads that state of the cluster for a PerconaServerMongoDB object and makes changes based on the state read
// and what is in the PerconaServerMongoDB.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDB) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

	rr := reconcile.Result{
		RequeueAfter: r.reconcileIn,
	}

	// As operator can handle a few clusters
	// lock should be created per cluster to not lock cron jobs of other clusters
	l := r.lockers.LoadOrCreate(request.NamespacedName.String())

	// PerconaServerMongoDB object is also accessed and changed by a version service's cron job (that runs concurrently)
	l.statusMutex.Lock()
	defer l.statusMutex.Unlock()
	// we have to be sure the reconcile loop will be run at least once
	// in-between any version service jobs (hence any two vs jobs shouldn't be run sequentially).
	// the version service job sets the state to  `updateWait` and the next job can be run only
	// after the state was dropped to`updateDone` again
	defer atomic.StoreInt32(l.updateSync, updateDone)

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

	isClusterLive := clusterInit

	defer func() {
		err = r.updateStatus(cr, err, isClusterLive)
		if err != nil {
			logger.Error(err, "failed to update cluster status", "replset", cr.Spec.Replsets[0].Name)
		}
	}()

	err = cr.CheckNSetDefaults(r.serverVersion.Platform, log)
	if err != nil {
		err = errors.Wrap(err, "wrong psmdb options")
		return reconcile.Result{}, err
	}

	err = r.checkConfiguration(cr)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.checkFinalizers(cr)
	if err != nil {
		logger.Error(err, "failed to run finalizers")
		return rr, err
	}

	err = r.reconcileUsersSecret(cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile users secret")
	}

	repls := cr.Spec.Replsets
	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		repls = append([]*api.ReplsetSpec{cr.Spec.Sharding.ConfigsvrReplSet}, repls...)
	}

	if cr.CompareVersion("1.5.0") >= 0 {
		err := r.reconcileUsers(cr, repls)
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "failed to reconcile users")
		}
	}

	removed, err := r.getRemovedSfs(cr)
	if err != nil {
		return reconcile.Result{}, err
	}

	for _, v := range removed {
		rsName := v.Labels["app.kubernetes.io/replset"]

		err := r.checkIfPossibleToRemove(cr, rsName)
		if err != nil {
			return reconcile.Result{}, errors.Wrapf(err, "check remove posibility for rs %s", rsName)
		}

		err = r.removeRSFromShard(cr, rsName)
		if err != nil {
			return reconcile.Result{}, errors.Wrapf(err, "failed to remove rs %s", rsName)
		}

		err = r.client.Delete(context.Background(), &v)
		if err != nil {
			return reconcile.Result{}, errors.Wrapf(err, "failed to remove rs %s", rsName)
		}
	}

	if cr.Status.MongoVersion == "" || strings.HasSuffix(cr.Status.MongoVersion, "intermediate") {
		err := r.ensureVersion(cr, VersionServiceClient{})
		if err != nil {
			logger.Info("failed to ensure version, running with default", "error", err)
		}
	}

	if !cr.Spec.UnsafeConf {
		err = r.reconsileSSL(cr)
		if err != nil {
			err = errors.Errorf(`TLS secrets handler: "%v". Please create your TLS secret `+cr.Spec.Secrets.SSL+` manually or setup cert-manager correctly`, err)
			return reconcile.Result{}, err
		}
	}

	internalKey := psmdb.InternalKey(cr)
	ikCreated, err := r.ensureSecurityKey(cr, internalKey, "mongodb-key", 768, true)
	if err != nil {
		err = errors.Wrapf(err, "ensure mongo Key %s", internalKey)
		return reconcile.Result{}, err
	}

	if ikCreated {
		logger.Info("Created a new mongo key", "KeyName", internalKey)
	}

	if *cr.Spec.Mongod.Security.EnableEncryption {
		created, err := r.ensureSecurityKey(cr, cr.Spec.Mongod.Security.EncryptionKeySecret, psmdb.EncryptionKeyName, 32, false)
		if err != nil {
			err = errors.Wrapf(err, "ensure mongo Key %s", cr.Spec.Mongod.Security.EncryptionKeySecret)
			return reconcile.Result{}, err
		}
		if created {
			logger.Info("Created a new mongo key", "KeyName", cr.Spec.Mongod.Security.EncryptionKeySecret)
		}
	}

	if cr.Spec.Backup.Enabled {
		err = r.reconcileBackupTasks(cr)
		if err != nil {
			err = errors.Wrap(err, "reconcile backup tasks")
			return reconcile.Result{}, err
		}
	}

	shards := 0
	for _, replset := range repls {
		if (cr.Spec.Sharding.Enabled && replset.ClusterRole == api.ClusterRoleShardSvr) ||
			!cr.Spec.Sharding.Enabled {
			shards++
		}

		if cr.Spec.Sharding.Enabled && replset.ClusterRole != api.ClusterRoleConfigSvr && replset.Name == api.ConfigReplSetName {
			return reconcile.Result{}, errors.Errorf("%s is reserved name for config server replset", api.ConfigReplSetName)
		}

		matchLabels := map[string]string{
			"app.kubernetes.io/name":       "percona-server-mongodb",
			"app.kubernetes.io/instance":   cr.Name,
			"app.kubernetes.io/replset":    replset.Name,
			"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
			"app.kubernetes.io/part-of":    "percona-server-mongodb",
		}

		pods, err := r.getRSPods(cr, replset.Name)
		if err != nil {
			err = errors.Errorf("get pods list for replset %s: %v", replset.Name, err)
			return reconcile.Result{}, err
		}

		mongosPods, err := r.getMongosPods(cr)
		if err != nil && !k8serrors.IsNotFound(err) {
			return reconcile.Result{}, errors.Wrap(err, "get pods list for mongos")
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

		err = r.removeOudatedServices(cr, replset, &pods)
		if err != nil {
			err = errors.Errorf("failed to remove old services of replset %s: %v", replset.Name, err)
			return reconcile.Result{}, err
		}

		// Create Service
		if replset.Expose.Enabled {
			srvs, err := r.ensureExternalServices(cr, replset, &pods)
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
				return reconcile.Result{}, errors.Wrap(err, "set owner ref for service "+service.Name)
			}

			err = r.createOrUpdate(service)
			if err != nil {
				return reconcile.Result{}, errors.Wrap(err, "create or update service for replset "+replset.Name)
			}
		}

		_, ok := cr.Status.Replsets[replset.Name]
		if !ok {
			cr.Status.Replsets[replset.Name] = &api.ReplsetStatus{}
		}

		isClusterLive, err = r.reconcileCluster(cr, replset, pods, mongosPods.Items)
		if err != nil {
			logger.Error(err, "failed to reconcile cluster", "replset", replset.Name)
		}

		if err := r.fetchVersionFromMongo(cr, replset); err != nil {
			return rr, errors.Wrap(err, "update mongo version")
		}
	}

	err = r.stopMongosInCaseOfRestore(cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "on restore")
	}

	err = r.reconcileMongos(cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile mongos")
	}

	if err := r.enableBalancerIfNeeded(cr); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "failed to start balancer")
	}

	if err := r.upgradeFCVIfNeeded(cr, *repls[0], cr.Status.MongoVersion); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "failed to set FCV")
	}

	err = r.deleteMongosIfNeeded(cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "delete mongos")
	}

	err = r.deleteCfgIfNeeded(cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "delete config server")
	}

	err = r.sheduleEnsureVersion(cr, VersionServiceClient{})
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "failed to ensure version")
	}

	// DB cluster can be not ready yet so it's requeued after some time
	if err = r.updatePITR(cr); err != nil {
		return rr, err
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDB) checkConfiguration(cr *api.PerconaServerMongoDB) error {
	// check if sharding has already been enabled
	_, cfgErr := r.getCfgStatefulset(cr)
	if cfgErr != nil && !k8serrors.IsNotFound(cfgErr) {
		return errors.Wrap(cfgErr, "failed to get cfg replset")
	}

	rs, rsErr := r.getMongodStatefulsets(cr)
	if rsErr != nil && !k8serrors.IsNotFound(rsErr) {
		return errors.Wrap(rsErr, "failed to get all replsets")
	}

	if !cr.Spec.Sharding.Enabled {
		// means we have already had sharded cluster and try to disable sharding
		if cfgErr == nil && len(rs.Items) > 1 {
			return errors.Errorf("failed to disable sharding with %d active replsets", len(rs.Items))
		}

		// means we want to run multiple replsets without sharding
		if len(cr.Spec.Replsets) > 1 {
			return errors.New("running multiple replsets without sharding is prohibited")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) getRemovedSfs(cr *api.PerconaServerMongoDB) ([]appsv1.StatefulSet, error) {
	removed := make([]appsv1.StatefulSet, 0)

	sfsList := appsv1.StatefulSetList{}
	if err := r.client.List(context.TODO(), &sfsList,
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"app.kubernetes.io/instance": cr.Name,
			}),
		},
	); err != nil {
		return nil, errors.Wrap(err, "failed to get statefulset list")
	}

	appliedRSNames := make(map[string]struct{}, len(cr.Spec.Replsets))
	for _, v := range cr.Spec.Replsets {
		appliedRSNames[cr.Name+"-"+v.Name] = struct{}{}
	}

	for _, v := range sfsList.Items {
		if v.Name == cr.Name+"-"+api.ConfigReplSetName {
			continue
		}

		if v.Labels["app.kubernetes.io/component"] == "arbiter" {
			continue
		}

		if _, ok := appliedRSNames[v.Name]; !ok {
			removed = append(removed, v)
		}
	}

	return removed, nil
}

func (r *ReconcilePerconaServerMongoDB) checkIfPossibleToRemove(cr *api.PerconaServerMongoDB, rsName string) error {
	systemDBs := map[string]struct{}{
		"local":  {},
		"admin":  {},
		"config": {},
	}

	client, err := r.mongoClientWithRole(cr, api.ReplsetSpec{Name: rsName}, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "dial:")
	}

	defer func() {
		err := client.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	list, err := mongo.ListDBs(context.Background(), client)
	if err != nil {
		log.Error(err, "failed to list databases", "rs", rsName)
		return errors.Wrapf(err, "failed to list databases for rs %s", rsName)
	}

	for _, db := range list.DBs {
		if _, ok := systemDBs[db.Name]; !ok {
			return errors.Errorf("non system db found: %s", db.Name)
		}
	}

	return nil
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

func (r *ReconcilePerconaServerMongoDB) deleteCfgIfNeeded(cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Sharding.Enabled {
		return nil
	}

	sfsName := cr.Name + "-" + api.ConfigReplSetName
	sfs := psmdb.NewStatefulSet(sfsName, cr.Namespace)

	err := r.client.Delete(context.TODO(), sfs)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrapf(err, "failed to delete sfs: %s", sfs.Name)
	}

	svc := corev1.Service{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name + "-" + api.ConfigReplSetName, Namespace: cr.Namespace}, &svc)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "failed to get config service")
	}

	if k8serrors.IsNotFound(err) {
		return nil
	}

	err = r.client.Delete(context.TODO(), &svc)
	if err != nil {
		return errors.Wrap(err, "failed to delete config service")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) stopMongosInCaseOfRestore(cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	rstRunning, err := r.isRestoreRunning(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check running restores")
	}

	if !rstRunning {
		return nil
	}

	err = r.disableBalancer(cr)
	if err != nil {
		return errors.Wrap(err, "failed to disable balancer")
	}

	err = r.deleteMongos(cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete mongos")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) upgradeFCVIfNeeded(cr *api.PerconaServerMongoDB, repl api.ReplsetSpec, newFCV string) error {
	if !cr.Spec.UpgradeOptions.SetFCV {
		return nil
	}

	up, err := r.isAllSfsUpToDate(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check is all sfs up to date")
	}

	if !up {
		return nil
	}

	fcvsv, err := v.NewSemver(newFCV)
	if err != nil {
		return errors.Wrap(err, "invalid version")
	}

	fcv, err := r.getFCV(cr)
	if err != nil {
		return errors.Wrap(err, "failed to get FCV")
	}

	if !canUpgradeVersion(fcv, MajorMinor(fcvsv)) {
		return nil
	}

	err = r.setFCV(cr, newFCV)
	return errors.Wrap(err, "failed to set FCV")
}

func (r *ReconcilePerconaServerMongoDB) deleteMongos(cr *api.PerconaServerMongoDB) error {
	msDepl := psmdb.MongosDeployment(cr)
	err := r.client.Delete(context.TODO(), msDepl)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "failed to delete mongos deployment")
	}

	mongosSvc := psmdb.MongosService(cr)
	err = r.client.Delete(context.TODO(), &mongosSvc)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "failed to delete mongos service")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteMongosIfNeeded(cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Sharding.Enabled {
		return nil
	}

	return r.deleteMongos(cr)
}

func (r *ReconcilePerconaServerMongoDB) reconcileMongos(cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	uptodate, err := r.isAllSfsUpToDate(cr)
	if err != nil {
		return errors.Wrap(err, "failed to chaeck if all sfs are up to date")
	}

	rstRunning, err := r.isRestoreRunning(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check running restores")
	}

	if !uptodate || rstRunning {
		return nil
	}

	msDepl := psmdb.MongosDeployment(cr)
	err = setControllerReference(cr, msDepl, r.scheme)
	if err != nil {
		return errors.Wrapf(err, "set owner ref for deployment %s", msDepl.Name)
	}

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: msDepl.Name, Namespace: msDepl.Namespace}, msDepl)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrapf(err, "get deployment %s", msDepl.Name)
	}

	if !k8serrors.IsNotFound(err) && msDepl.Status.UpdatedReplicas < msDepl.Status.Replicas {
		log.Info("waiting for mongos update")
		return nil
	}

	opPod, err := r.operatorPod()
	if err != nil {
		return errors.Wrap(err, "failed to get operator pod")
	}

	deplSpec, err := psmdb.MongosDeploymentSpec(cr, opPod, log)
	if err != nil {
		return errors.Wrapf(err, "create deployment spec %s", msDepl.Name)
	}

	sslAnn, err := r.sslAnnotation(cr)
	if err != nil {
		return errors.Wrap(err, "failed to get ssl annotations")
	}
	if deplSpec.Template.Annotations == nil {
		deplSpec.Template.Annotations = make(map[string]string)
	}

	for k, v := range sslAnn {
		deplSpec.Template.Annotations[k] = v
	}

	if cr.CompareVersion("1.8.0") < 0 {
		depl, err := r.getMongosDeployment(cr)
		if err != nil && !k8serrors.IsNotFound(err) {
			return errors.Wrap(err, "failed to get mongos deployment")
		}

		for k, v := range depl.Spec.Template.Annotations {
			if k == "last-applied-secret" || k == "last-applied-secret-ts" {
				deplSpec.Template.Annotations[k] = v
			}
		}
	}

	if cr.Spec.PMM.Enabled {
		pmmsec := corev1.Secret{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: api.UserSecretName(cr), Namespace: cr.Namespace}, &pmmsec)
		if err != nil {
			return errors.Wrapf(err, "check pmm secrets: %s", api.UserSecretName(cr))
		}

		pmmC, err := psmdb.AddPMMContainer(cr, api.UserSecretName(cr), pmmsec, cr.Spec.PMM.MongosParams)
		if err != nil {
			return errors.Wrap(err, "failed to create a pmm-client container")
		}
		deplSpec.Template.Spec.Containers = append(
			deplSpec.Template.Spec.Containers,
			pmmC,
		)
	}

	msDepl.Spec = deplSpec
	err = r.createOrUpdate(msDepl)
	if err != nil {
		return errors.Wrapf(err, "update or create deployment %s", msDepl.Name)
	}
	err = r.reconcilePDB(cr.Spec.Sharding.Mongos.PodDisruptionBudget, msDepl.Spec.Template.Labels, cr.Namespace, msDepl)
	if err != nil {
		return errors.Wrap(err, "reconcile PodDisruptionBudget for mongos deployment")
	}

	mongosSvc := psmdb.MongosService(cr)
	err = setControllerReference(cr, &mongosSvc, r.scheme)
	if err != nil {
		return errors.Wrapf(err, "set owner ref for service %s", mongosSvc.Name)
	}

	if mongosSvc.Spec.Type != cr.Spec.Sharding.Mongos.Expose.ExposeType ||
		!mapsEqual(mongosSvc.Annotations, cr.Spec.Sharding.Mongos.Expose.ServiceAnnotations) {
		err = r.client.Delete(context.TODO(), &mongosSvc)
		if err != nil && !k8serrors.IsNotFound(err) {
			return errors.Wrapf(err, "delete service %s", mongosSvc.Name)
		}
	}

	mongosSvc.Spec = psmdb.MongosServiceSpec(cr)

	err = r.createOrUpdate(&mongosSvc)
	if err != nil {
		return errors.Wrap(err, "create or update mongos svc")
	}

	return nil
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}

	for ka, va := range a {
		if vb, ok := b[ka]; !ok || vb != va {
			return false
		}
	}

	return true
}

func (r *ReconcilePerconaServerMongoDB) sslAnnotation(cr *api.PerconaServerMongoDB) (map[string]string, error) {
	annotation := make(map[string]string)

	is110 := cr.CompareVersion("1.1.0") >= 0
	if is110 {
		sslHash, err := r.getTLSHash(cr, cr.Spec.Secrets.SSL)
		if err != nil {
			return nil, errors.Wrap(err, "get secret hash error")
		}
		annotation["percona.com/ssl-hash"] = sslHash

		sslInternalHash, err := r.getTLSHash(cr, cr.Spec.Secrets.SSLInternal)
		if err != nil && !k8serrors.IsNotFound(err) {
			return nil, errors.Wrap(err, "get secret hash error")
		} else if err == nil {
			annotation["percona.com/ssl-internal-hash"] = sslInternalHash
		}
	}

	return annotation, nil
}

// TODO: reduce cyclomatic complexity
func (r *ReconcilePerconaServerMongoDB) reconcileStatefulSet(arbiter bool, cr *api.PerconaServerMongoDB,
	replset *api.ReplsetSpec, matchLabels map[string]string, internalKeyName string) (*appsv1.StatefulSet, error) {

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

	if replset.ClusterRole == api.ClusterRoleConfigSvr {
		matchLabels["app.kubernetes.io/component"] = api.ConfigReplSetName
	}

	sfs := psmdb.NewStatefulSet(sfsName, cr.Namespace)
	err := setControllerReference(cr, sfs, r.scheme)
	if err != nil {
		return nil, errors.Wrapf(err, "set owner ref for StatefulSet %s", sfs.Name)
	}

	errGet := r.client.Get(context.TODO(), types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, sfs)
	if errGet != nil && !k8serrors.IsNotFound(errGet) {
		return nil, errors.Wrapf(err, "get StatefulSet %s", sfs.Name)
	}

	inits := []corev1.Container{}
	if cr.CompareVersion("1.5.0") >= 0 {
		operatorPod, err := r.operatorPod()
		if err != nil {
			return nil, errors.Wrap(err, "failed to get operator pod")
		}
		inits = append(inits, psmdb.InitContainers(cr, operatorPod)...)
	}

	sfsSpec, err := psmdb.StatefulSpec(cr, replset, containerName, matchLabels, multiAZ, size, internalKeyName, inits, log)
	if err != nil {
		return nil, errors.Wrapf(err, "create StatefulSet.Spec %s", sfs.Name)
	}
	if sfsSpec.Template.Annotations == nil {
		sfsSpec.Template.Annotations = make(map[string]string)
	}
	for k, v := range sfs.Spec.Template.Annotations {
		sfsSpec.Template.Annotations[k] = v
	}

	if cr.CompareVersion("1.8.0") < 0 {
		sfs, err := r.getRsStatefulset(cr, replset.Name)
		if err != nil && !k8serrors.IsNotFound(err) {
			return nil, errors.Wrapf(err, "failed to get rs %s statefulset", replset.Name)
		}

		for k, v := range sfs.Annotations {
			if k == "last-applied-secret" || k == "last-applied-secret-ts" {
				sfsSpec.Template.Annotations[k] = v
			}
		}
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
	if cr.CompareVersion("1.8.0") >= 0 {
		sfsSpec.Template.Spec.Volumes = append(sfsSpec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "users-secret-file",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: api.InternalUserSecretName(cr),
					},
				},
			})
	}

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
				psmdb.PersistentVolumeClaim(psmdb.MongodDataVolClaimName, cr.Namespace, replset.Labels, replset.VolumeSpec.PersistentVolumeClaim),
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
			agentC, err := backup.AgentContainer(cr, replset.Name, replset.Size)
			if err != nil {
				return nil, errors.Wrap(err, "create a backup container")
			}
			sfsSpec.Template.Spec.Containers = append(sfsSpec.Template.Spec.Containers, agentC)
		}

		if cr.Spec.PMM.Enabled {
			pmmsec := corev1.Secret{}
			err := r.client.Get(context.TODO(), types.NamespacedName{Name: api.UserSecretName(cr), Namespace: cr.Namespace}, &pmmsec)
			if err != nil {
				return nil, errors.Wrap(err, "check pmm secrets")
			}
			pmmC, err := psmdb.AddPMMContainer(cr, api.UserSecretName(cr), pmmsec, cr.Spec.PMM.MongodParams)
			if err != nil {
				return nil, errors.Wrap(err, "failed to create a pmm-client container")
			}
			sfsSpec.Template.Spec.Containers = append(sfsSpec.Template.Spec.Containers, pmmC)
		}
	}

	switch cr.Spec.UpdateStrategy {
	case appsv1.OnDeleteStatefulSetStrategyType:
		sfsSpec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
	case api.SmartUpdateStatefulSetStrategyType:
		sfsSpec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
	default:
		var zero int32 = 0
		sfsSpec.UpdateStrategy = appsv1.StatefulSetUpdateStrategy{
			Type: appsv1.RollingUpdateStatefulSetStrategyType,
			RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
				Partition: &zero,
			},
		}
	}

	sslAnn, err := r.sslAnnotation(cr)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get ssl annotations")
	}
	for k, v := range sslAnn {
		sfsSpec.Template.Annotations[k] = v
	}

	sfs.Spec = sfsSpec
	if cr.CompareVersion("1.6.0") >= 0 {
		sfs.Labels = matchLabels
	}

	err = r.createOrUpdate(sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "update StatefulSet %s", sfs.Name)
	}

	err = r.reconcilePDB(pdbspec, matchLabels, cr.Namespace, sfs)
	if err != nil {
		return nil, errors.Wrapf(err, "PodDisruptionBudget for %s", sfs.Name)
	}

	if err := r.smartUpdate(cr, sfs, replset); err != nil {
		return nil, errors.Wrap(err, "failed to run smartUpdate")
	}

	return sfs, nil
}

func (r *ReconcilePerconaServerMongoDB) operatorPod() (corev1.Pod, error) {
	operatorPod := corev1.Pod{}

	nsBytes, err := ioutil.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return operatorPod, err
	}

	ns := strings.TrimSpace(string(nsBytes))

	if err := r.client.Get(context.TODO(), types.NamespacedName{
		Namespace: ns,
		Name:      os.Getenv("HOSTNAME"),
	}, &operatorPod); err != nil {
		return operatorPod, err
	}

	return operatorPod, nil
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

	metaAccessor, ok := owner.(metav1.ObjectMetaAccessor)
	if !ok {
		return errors.New("can't convert object to ObjectMetaAccessor")
	}

	ownerMeta := metaAccessor.GetObjectMeta()

	if ownerMeta.GetUID() == "" {
		err := r.client.Get(context.TODO(), types.NamespacedName{
			Name:      ownerMeta.GetName(),
			Namespace: ownerMeta.GetNamespace(),
		}, owner)
		if err != nil {
			return errors.Wrap(err, "failed to get owner uid for pdb")
		}
	}

	pdb := psmdb.PodDisruptionBudget(spec, labels, namespace)
	err := setControllerReference(owner, pdb, r.scheme)
	if err != nil {
		return errors.Wrap(err, "set owner reference")
	}

	return r.createOrUpdate(pdb)
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdate(obj runtime.Object) error {
	metaAccessor, ok := obj.(metav1.ObjectMetaAccessor)
	if !ok {
		return errors.New("can't convert object to ObjectMetaAccessor")
	}

	objectMeta := metaAccessor.GetObjectMeta()

	if objectMeta.GetAnnotations() == nil {
		objectMeta.SetAnnotations(make(map[string]string))
	}

	objAnnotations := objectMeta.GetAnnotations()
	delete(objAnnotations, "percona.com/last-config-hash")
	objectMeta.SetAnnotations(objAnnotations)

	hash, err := getObjectHash(obj)
	if err != nil {
		return errors.Wrap(err, "calculate object hash")
	}

	objAnnotations = objectMeta.GetAnnotations()
	objAnnotations["percona.com/last-config-hash"] = hash
	objectMeta.SetAnnotations(objAnnotations)

	val := reflect.ValueOf(obj)
	if val.Kind() == reflect.Ptr {
		val = reflect.Indirect(val)
	}
	oldObject := reflect.New(val.Type()).Interface().(runtime.Object)

	err = r.client.Get(context.Background(), types.NamespacedName{
		Name:      objectMeta.GetName(),
		Namespace: objectMeta.GetNamespace(),
	}, oldObject)

	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get object")
	}

	if k8serrors.IsNotFound(err) {
		return r.client.Create(context.TODO(), obj)
	}

	oldObjectMeta := oldObject.(metav1.ObjectMetaAccessor).GetObjectMeta()

	if oldObjectMeta.GetAnnotations()["percona.com/last-config-hash"] != hash ||
		!isObjectMetaEqual(objectMeta, oldObjectMeta) {

		objectMeta.SetResourceVersion(oldObjectMeta.GetResourceVersion())
		switch object := obj.(type) {
		case *corev1.Service:
			object.Spec.ClusterIP = oldObject.(*corev1.Service).Spec.ClusterIP
		}

		return r.client.Update(context.TODO(), obj)
	}

	return nil
}

func getObjectHash(obj runtime.Object) (string, error) {
	var dataToMarshall interface{}
	switch object := obj.(type) {
	case *appsv1.StatefulSet:
		dataToMarshall = object.Spec
	case *appsv1.Deployment:
		dataToMarshall = object.Spec
	case *corev1.Service:
		dataToMarshall = object.Spec
	default:
		dataToMarshall = obj
	}
	data, err := json.Marshal(dataToMarshall)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(data), nil
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

func isObjectMetaEqual(old, new metav1.Object) bool {
	return compareMaps(old.GetAnnotations(), new.GetAnnotations()) &&
		compareMaps(old.GetLabels(), new.GetLabels())
}

func compareMaps(x, y map[string]string) bool {
	if len(x) != len(y) {
		return false
	}

	for k, v := range x {
		yVal, ok := y[k]
		if !ok || yVal != v {
			return false
		}
	}

	return true
}
