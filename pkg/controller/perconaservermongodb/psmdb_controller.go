package perconaservermongodb

import (
	"context"
	"crypto/md5"
	stderrors "errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	v "github.com/hashicorp/go-version"
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
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	psmdbconfig "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pmm"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

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
	cli, err := clientcmd.NewClient(mgr.GetConfig())
	if err != nil {
		return nil, errors.Wrap(err, "create clientcmd")
	}

	sv, err := version.Server(cli)
	if err != nil {
		return nil, errors.Wrap(err, "get server version")
	}

	mgr.GetLogger().Info("server version", "platform", sv.Platform, "version", sv.Info)

	initImage, err := getOperatorPodImage(context.TODO())
	if err != nil {
		return nil, errors.Wrap(err, "failed to get operator pod image")
	}

	client, err := client.New(mgr.GetConfig(), client.Options{
		Scheme: mgr.GetScheme(),
		Cache: &client.CacheOptions{
			DisableFor: []client.Object{&corev1.Node{}},
		},
	})
	if err != nil {
		return nil, errors.Wrap(err, "create client")
	}

	return &ReconcilePerconaServerMongoDB{
		client:                 client,
		scheme:                 mgr.GetScheme(),
		serverVersion:          sv,
		reconcileIn:            time.Second * 5,
		crons:                  NewCronRegistry(),
		lockers:                newLockStore(),
		newPBM:                 backup.NewPBM,
		restConfig:             mgr.GetConfig(),
		newCertManagerCtrlFunc: tls.NewCertManagerController,

		initImage: initImage,

		clientcmd: cli,
	}, nil
}

func getOperatorPodImage(ctx context.Context) (string, error) {
	cfg, err := config.GetConfig()
	if err != nil {
		return "", err
	}

	c, err := client.New(cfg, client.Options{})
	if err != nil {
		return "", err
	}

	nsBytes, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}

	ns := strings.TrimSpace(string(nsBytes))

	pod := &corev1.Pod{}
	err = c.Get(ctx, types.NamespacedName{Namespace: ns, Name: os.Getenv("HOSTNAME")}, pod)
	if err != nil {
		return "", err
	}

	return pod.Spec.Containers[0].Image, nil
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	return builder.ControllerManagedBy(mgr).
		For(&api.PerconaServerMongoDB{}).
		Named("psmdb-controller").
		Complete(r)
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDB{}

type CronRegistry struct {
	crons             *cron.Cron
	ensureVersionJobs *sync.Map
	backupJobs        *sync.Map
}

// AddFuncWithSeconds does the same as cron.AddFunc but changes the schedule so that the function will run the exact second that this method is called.
func (r *CronRegistry) AddFuncWithSeconds(spec string, cmd func()) (cron.EntryID, error) {
	schedule, err := cron.ParseStandard(spec)
	if err != nil {
		return 0, errors.Wrap(err, "failed to parse cron schedule")
	}
	schedule.(*cron.SpecSchedule).Second = uint64(1 << time.Now().Second())
	id := r.crons.Schedule(schedule, cron.FuncJob(cmd))
	return id, nil
}

func NewCronRegistry() CronRegistry {
	c := CronRegistry{
		crons:             cron.New(),
		ensureVersionJobs: new(sync.Map),
		backupJobs:        new(sync.Map),
	}

	c.crons.Start()

	return c
}

// ReconcilePerconaServerMongoDB reconciles a PerconaServerMongoDB object
type ReconcilePerconaServerMongoDB struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client     client.Client
	scheme     *runtime.Scheme
	restConfig *rest.Config

	crons               CronRegistry
	clientcmd           *clientcmd.Client
	serverVersion       *version.ServerVersion
	reconcileIn         time.Duration
	mongoClientProvider MongoClientProvider

	newCertManagerCtrlFunc tls.NewCertManagerControllerFunc

	newPBM backup.NewPBMFunc

	initImage string

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
		resyncMutex: new(sync.Mutex),
		updateSync:  new(int32),
	})

	return val.(lock)
}

type lock struct {
	statusMutex *sync.Mutex
	resyncMutex *sync.Mutex
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
func (r *ReconcilePerconaServerMongoDB) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

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
	err := r.client.Get(ctx, request.NamespacedName, cr)
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

	clusterStatus := api.AppStateInit

	defer func() {
		err = r.updateStatus(ctx, cr, err, clusterStatus)
		if err != nil {
			log.Error(err, "failed to update cluster status", "replset", cr.Spec.Replsets[0].Name)
		}
	}()

	if err := r.setCRVersion(ctx, cr); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "set CR version")
	}

	err = cr.CheckNSetDefaults(ctx, r.serverVersion.Platform)
	if err != nil {
		// If the user created a cluster with finalizers and wrong options, it would be impossible to delete a cluster.
		// We need to delete finalizers.
		//
		// TODO: Separate CheckNSetDefaults into two different methods "Check" and "SetDefaults".
		//       Use "SetDefaults" and try to run "checkFinalizers" instead of just deleting finalizers.
		//       Currently we can't run the "checkFinalizers" method because we will get nil pointer reference panics,
		//       due to defaults not being set.
		if cr.DeletionTimestamp != nil {
			if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
				c := &api.PerconaServerMongoDB{}

				if err := r.client.Get(ctx, client.ObjectKeyFromObject(cr), c); err != nil {
					return err
				}

				c.SetFinalizers([]string{})
				return r.client.Update(ctx, c)
			}); err != nil {
				log.Error(err, "failed to remove finalizers")
			}
		}

		err = errors.Wrap(err, "wrong psmdb options")
		return reconcile.Result{}, err
	}

	if cr.ObjectMeta.DeletionTimestamp != nil {
		rec, err := r.checkFinalizers(ctx, cr)
		if rec || err != nil {
			return rr, err
		}
	}

	err = r.reconcilePause(ctx, cr)
	if err != nil {
		return reconcile.Result{}, err
	}

	err = r.checkConfiguration(ctx, cr)
	if err != nil {
		return reconcile.Result{}, err
	}

	isDownscale, err := r.safeDownscale(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "safe downscale")
	}

	err = r.reconcileUsersSecret(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile users secret")
	}
	repls := cr.Spec.Replsets
	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		repls = append([]*api.ReplsetSpec{cr.Spec.Sharding.ConfigsvrReplSet}, repls...)
	}

	err = r.reconcileMongodConfigMaps(ctx, cr, repls)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile mongod configmaps")
	}

	if err := r.reconcileMongosConfigMap(ctx, cr); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile mongos config map")
	}

	if cr.CompareVersion("1.21.0") >= 0 {
		if err := r.reconcileLogCollectorConfigMaps(ctx, cr); err != nil {
			return reconcile.Result{}, errors.Wrap(err, "reconcile log collector config map")
		}
	}

	if cr.CompareVersion("1.5.0") >= 0 {
		err := r.reconcileUsers(ctx, cr, repls)
		if err != nil {
			return reconcile.Result{}, errors.Wrap(err, "failed to reconcile users")
		}
	}

	stsForDeletion, err := r.getSTSForDeletionWithTheirRSDetails(ctx, cr)
	if err != nil {
		return reconcile.Result{}, err
	}

	for _, rr := range stsForDeletion {
		log.Info("Deleting STS component from replst", "sts", rr.sts.Name, "rs", rr.rsName, "port", rr.rsPort)

		err = r.checkIfUserDataExistInRS(ctx, cr, rr.rsName, rr.rsPort)
		if err != nil {
			return reconcile.Result{}, errors.Wrapf(err, "check remove posibility for rs %s", rr.rsName)
		}

		if rr.sts.Labels[naming.LabelKubernetesComponent] == "mongod" {
			err = r.removeRSFromShard(ctx, cr, rr.rsName)
			if err != nil {
				return reconcile.Result{}, errors.Wrapf(err, "failed to remove rs %s", rr.rsName)
			}
		}

		err = r.client.Delete(ctx, &rr.sts)
		if err != nil {
			return reconcile.Result{}, errors.Wrapf(err, "failed to remove rs %s", rr.rsName)
		}
	}

	if cr.Status.MongoVersion == "" || strings.HasSuffix(cr.Status.MongoVersion, "intermediate") {
		err := r.ensureVersion(ctx, cr, VersionServiceClient{})
		if err != nil {
			log.Info("failed to ensure version, running with default", "error", err)
		}
	}

	err = r.reconcileSSL(ctx, cr)
	if err != nil {
		err = errors.Errorf(`TLS secrets handler: "%v". Please create your TLS secret `+api.SSLSecretName(cr)+` manually or setup cert-manager correctly`, err)
		return reconcile.Result{}, err
	}

	ikCreated, err := r.ensureSecurityKey(ctx, cr, cr.Spec.Secrets.GetInternalKey(cr), api.InternalKeyName, 768, true)
	if err != nil {
		err = errors.Wrapf(err, "ensure mongo Key %s", cr.Spec.Secrets.GetInternalKey(cr))
		return reconcile.Result{}, err
	}
	if ikCreated {
		log.Info("Created a new mongo key", "KeyName", cr.Spec.Secrets.GetInternalKey(cr))
	}

	created, err := r.ensureSecurityKey(ctx, cr, cr.Spec.Secrets.EncryptionKey, api.EncryptionKeyName, 32, false)
	if err != nil {
		err = errors.Wrapf(err, "ensure mongo Key %s", cr.Spec.Secrets.EncryptionKey)
		return reconcile.Result{}, err
	}
	if created {
		log.Info("Created a new mongo key", "KeyName", cr.Spec.Secrets.EncryptionKey)
	}

	if cr.Spec.Backup.Enabled {
		err = r.reconcileBackupTasks(ctx, cr)
		if err != nil {
			err = errors.Wrap(err, "reconcile backup tasks")
			return reconcile.Result{}, err
		}
	}

	clusterStatus, err = r.reconcileReplsets(ctx, cr, repls)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile statefulsets")
	}

	err = r.reconcileMongos(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile mongos")
	}

	if err := r.upgradeFCVIfNeeded(ctx, cr, cr.Status.MongoVersion); err != nil {
		return reconcile.Result{}, errors.Wrap(err, "failed to set FCV")
	}

	// clean orphan PVCs if downscale
	if isDownscale {
		err = r.deleteOrphanPVCs(ctx, cr)
		if err != nil {
			return reconcile.Result{}, fmt.Errorf("failed to delete orphan PVCs: %v", err)
		}
	}

	err = r.reconcileCustomUsers(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile custom users")
	}

	err = r.exportServices(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "export services")
	}

	err = r.scheduleEnsureVersion(ctx, cr, VersionServiceClient{})
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "schedule ensure version job")
	}

	err = r.scheduleTelemetryRequests(ctx, cr, VersionServiceClient{})
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "schedule telemetry job")
	}

	err = r.reconcilePBM(ctx, cr)
	if err != nil {
		return reconcile.Result{}, errors.Wrap(err, "reconcile PBM")
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileReplset(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	matchLabels := naming.MongodLabels(cr, replset)

	_, err := r.reconcileStatefulSet(ctx, cr, replset, matchLabels)
	if err != nil {
		err = errors.Errorf("reconcile StatefulSet for %s: %v", replset.Name, err)
		return err
	}

	if replset.Arbiter.Enabled {
		matchLabels = naming.ArbiterLabels(cr, replset)
		_, err := r.reconcileStatefulSet(ctx, cr, replset, matchLabels)
		if err != nil {
			err = errors.Errorf("reconcile Arbiter StatefulSet for %s: %v", replset.Name, err)
			return err
		}
	} else {
		err := r.client.Delete(ctx, psmdb.NewStatefulSet(naming.ArbiterStatefulSetName(cr, replset), cr.Namespace))
		if err != nil && !k8serrors.IsNotFound(err) {
			err = errors.Errorf("delete arbiter in replset %s: %v", replset.Name, err)
			return err
		}
	}

	if replset.NonVoting.Enabled {
		matchLabels = naming.NonVotingLabels(cr, replset)
		_, err := r.reconcileStatefulSet(ctx, cr, replset, matchLabels)
		if err != nil {
			err = errors.Errorf("reconcile nonVoting StatefulSet for %s: %v", replset.Name, err)
			return err
		}
	} else {
		err := r.client.Delete(ctx, psmdb.NewStatefulSet(naming.NonVotingStatefulSetName(cr, replset), cr.Namespace))
		if err != nil && !k8serrors.IsNotFound(err) {
			err = errors.Errorf("delete nonVoting statefulset %s: %v", replset.Name, err)
			return err
		}
	}

	if replset.Hidden.Enabled {
		matchLabels = naming.HiddenLabels(cr, replset)
		_, err := r.reconcileStatefulSet(ctx, cr, replset, matchLabels)
		if err != nil {
			err = errors.Errorf("reconcile nonVoting StatefulSet for %s: %v", replset.Name, err)
			return err
		}
	} else {
		err := r.client.Delete(ctx, psmdb.NewStatefulSet(naming.HiddenStatefulSetName(cr, replset), cr.Namespace))
		if err != nil && !k8serrors.IsNotFound(err) {
			err = errors.Errorf("delete hidden statefulset %s: %v", replset.Name, err)
			return err
		}
	}

	_, ok := cr.Status.Replsets[replset.Name]
	if !ok {
		cr.Status.Replsets[replset.Name] = api.ReplsetStatus{}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileReplsets(ctx context.Context, cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) (api.AppState, error) {
	log := logf.FromContext(ctx)

	if err := r.reconcileServices(ctx, cr, repls); err != nil {
		return "", errors.Wrap(err, "reconcile services")
	}

	for _, replset := range repls {
		if cr.Spec.Sharding.Enabled && replset.ClusterRole != api.ClusterRoleConfigSvr && replset.Name == api.ConfigReplSetName {
			return "", errors.Errorf("%s is reserved name for config server replset", api.ConfigReplSetName)
		}

		if err := r.reconcileReplset(ctx, cr, replset); err != nil {
			return "", errors.Wrapf(err, "reconcile replset %s", replset.Name)
		}

		if err := r.fetchVersionFromMongo(ctx, cr, replset); err != nil {
			return "", errors.Wrap(err, "update mongo version")
		}
	}

	mongosPods, err := r.getMongosPods(ctx, cr)
	if err != nil && !k8serrors.IsNotFound(err) {
		return "", errors.Wrap(err, "get pods list for mongos")
	}

	var errs []error
	clusterStatus := api.AppStateNone
	for _, replset := range repls {
		replsetStatus, members, err := r.reconcileCluster(ctx, cr, replset, mongosPods.Items)
		if err != nil {
			log.Error(err, "failed to reconcile cluster", "replset", replset.Name)
			errs = append(errs, err)
		}

		switch replsetStatus {
		case api.AppStateInit, api.AppStateError:
			log.V(1).Info("Replset status is not healthy", "replset", replset.Name, "status", replsetStatus)
		}

		statusPriority := []api.AppState{
			api.AppStateError,
			api.AppStateStopping,
			api.AppStatePaused,
			api.AppStateInit,
			api.AppStateReady,
		}
		for _, s := range statusPriority {
			if replsetStatus == s || clusterStatus == s {
				clusterStatus = s
				break
			}
		}

		if rs, ok := cr.Status.Replsets[replset.Name]; ok {
			rs.Members = make(map[string]api.ReplsetMemberStatus)
			for pod, member := range members {
				rs.Members[pod] = member
			}
			cr.Status.Replsets[replset.Name] = rs
		}
	}
	return clusterStatus, stderrors.Join(errs...)
}

func (r *ReconcilePerconaServerMongoDB) handleShardingToggle(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Pause || !cr.Spec.Backup.Enabled {
		return nil
	}

	getShardingStatus := func(cr *api.PerconaServerMongoDB) api.ConditionStatus {
		if cr.Spec.Sharding.Enabled {
			return api.ConditionTrue
		}

		return api.ConditionFalse
	}

	toggleShardingStatus := func(s api.ConditionStatus) bool {
		return s != api.ConditionTrue
	}

	condition := cr.Status.FindCondition(api.AppStateSharding)
	if condition == nil {
		cr.Status.AddCondition(api.ClusterCondition{
			Status:             getShardingStatus(cr),
			Type:               api.AppStateSharding,
			LastTransitionTime: metav1.NewTime(time.Now()),
		})
		return nil
	}
	if condition.Status == getShardingStatus(cr) {
		return nil
	}

	log := logf.FromContext(ctx)
	if toggleShardingStatus(condition.Status) {
		log.Info("Sharding is enabled, pausing the cluster")
	} else {
		log.Info("Sharding is disabled, pausing the cluster")
	}

	cr.Spec.Sharding.Enabled = toggleShardingStatus(condition.Status)
	cr.Spec.Pause = true
	if err := cr.CheckNSetDefaults(ctx, r.serverVersion.Platform); err != nil {
		return errors.Wrap(err, "check and set defaults")
	}

	mongodPods, err := r.getMongodPods(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get mongod pods")
	}
	mongosPods, err := r.getMongosPods(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get mongos pods")
	}
	if len(mongodPods.Items) != 0 || len(mongosPods.Items) != 0 {
		return nil
	}

	cr.Spec.Sharding.Enabled = toggleShardingStatus(condition.Status)
	cr.Spec.Pause = false
	// toggling sharding status removes all PBM collections, so we need to reconfigure
	cr.Status.BackupConfigHash = ""
	if err := cr.CheckNSetDefaults(ctx, r.serverVersion.Platform); err != nil {
		return errors.Wrap(err, "check and set defaults")
	}

	condition.Status = getShardingStatus(cr)
	condition.LastTransitionTime = metav1.NewTime(time.Now())

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcilePause(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if err := r.handleShardingToggle(ctx, cr); err != nil {
		return errors.Wrap(err, "handle sharding toggle")
	}
	if !cr.Spec.Pause || cr.DeletionTimestamp != nil {
		return nil
	}

	log := logf.FromContext(ctx)

	backupRunning, err := r.isBackupRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "check if backup is running")
	}
	if backupRunning {
		cr.Spec.Pause = false
		if err := cr.CheckNSetDefaults(ctx, r.serverVersion.Platform); err != nil {
			return errors.Wrap(err, "failed to set defaults")
		}
		log.Info("cluster will pause after all backups finished")
		return nil
	}

	for _, rs := range cr.Spec.Replsets {
		if cr.Status.State == api.AppStateStopping {
			log.Info("pausing cluster", "replset", rs.Name)
		}
		rs.Arbiter.Enabled = false
		rs.NonVoting.Enabled = false
	}

	if err := r.deletePSMDBPods(ctx, cr); err != nil {
		if err == errWaitingTermination {
			return nil
		}
		return errors.Wrap(err, "delete psmdb pods")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) setCRVersion(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if len(cr.Spec.CRVersion) > 0 {
		return nil
	}

	orig := cr.DeepCopy()
	cr.Spec.CRVersion = version.Version()

	if err := r.client.Patch(ctx, cr, client.MergeFrom(orig)); err != nil {
		return errors.Wrap(err, "patch CR")
	}

	logf.FromContext(ctx).Info("Set CR version", "version", cr.Spec.CRVersion)

	return nil
}

func (r *ReconcilePerconaServerMongoDB) checkConfiguration(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	// check if sharding has already been enabled
	_, cfgErr := r.getCfgStatefulset(ctx, cr)
	if cfgErr != nil && !k8serrors.IsNotFound(cfgErr) {
		return errors.Wrap(cfgErr, "failed to get cfg replset")
	}

	rs, rsErr := r.getMongodStatefulsets(ctx, cr)
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

// safeDownscale ensures replica set pods downscaled one by one and returns true if a downscale is in progress
func (r *ReconcilePerconaServerMongoDB) safeDownscale(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	isDownscale := false
	for _, rs := range cr.Spec.Replsets {
		sf, err := r.getRsStatefulset(ctx, cr, rs.Name)
		if err != nil && !k8serrors.IsNotFound(err) {
			return false, errors.Wrap(err, "get rs statefulset")
		}

		if k8serrors.IsNotFound(err) {
			continue
		}

		// downscale 1 pod on each reconciliation
		if *sf.Spec.Replicas-rs.Size > 1 {
			rs.Size = *sf.Spec.Replicas - 1
			isDownscale = true
		}
	}

	return isDownscale, nil
}

type statefulSetWithReplicaNameAndPort struct {
	sts    appsv1.StatefulSet
	rsName string
	rsPort int32
}

// getSTSForDeletionWithTheirRSDetails identifies StatefulSets that should be deleted and returns them
// along with their associated replica set name and port. This information is used to create a MongoDB
// client and perform necessary operations before the sts deletion.
func (r *ReconcilePerconaServerMongoDB) getSTSForDeletionWithTheirRSDetails(ctx context.Context, cr *api.PerconaServerMongoDB) ([]statefulSetWithReplicaNameAndPort, error) {
	existingSTSList := appsv1.StatefulSetList{}
	if err := r.client.List(ctx, &existingSTSList,
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				naming.LabelKubernetesInstance: cr.Name,
			}),
		},
	); err != nil {
		return nil, errors.Wrap(err, "failed to get statefulset list")
	}

	newlyAppliedRSNames := make(map[string]struct{}, len(cr.Spec.Replsets))

	for _, rs := range cr.Spec.Replsets {
		newlyAppliedRSNames[rs.Name] = struct{}{}
	}

	var removed []statefulSetWithReplicaNameAndPort

	for _, sts := range existingSTSList.Items {
		component := sts.Labels[naming.LabelKubernetesComponent]
		if component == "mongos" || sts.Name == cr.Name+"-"+api.ConfigReplSetName {
			continue
		}

		rsName := sts.Labels[naming.LabelKubernetesReplset]

		if _, ok := newlyAppliedRSNames[rsName]; ok {
			continue
		}

		port, err := getComponentPortFromSTS(sts, component)
		if err != nil {
			return nil, errors.Wrap(err, "get component port from sts")
		}
		removed = append(removed, statefulSetWithReplicaNameAndPort{rsName: rsName, rsPort: port, sts: sts})
	}

	// Sorting in reverse order to ensure that we first delete non-voting/arbiter before the main RS sts.
	sort.Slice(removed, func(i, j int) bool {
		return removed[i].sts.Name > removed[j].sts.Name
	})

	return removed, nil
}

func getComponentPortFromSTS(sts appsv1.StatefulSet, componentName string) (int32, error) {
	for _, container := range sts.Spec.Template.Spec.Containers {
		if container.Name == componentName {
			if len(container.Ports) > 0 {
				return container.Ports[0].ContainerPort, nil
			}
			return 0, fmt.Errorf("no ports found for container %s", componentName)
		}
	}

	// With this check we are catching cases like arbiter and non-voting
	// where the container name is different from the component name. For now,
	// we don't modify the ports of these components.
	if componentName != "mongod" {
		return api.DefaultMongoPort, nil
	}

	return 0, fmt.Errorf("container not found: %s", componentName)
}

// checkIfUserDataExistInRS verifies if a MongoDB replica set with particular name can be safely removed. It checks
// whether any user (non-system) databases exist, if they do, that would indicate that the replica set cannot be safely deleted.
func (r *ReconcilePerconaServerMongoDB) checkIfUserDataExistInRS(ctx context.Context, cr *api.PerconaServerMongoDB, rsName string, port int32) error {
	log := logf.FromContext(ctx)

	systemDBs := map[string]struct{}{
		"local":  {},
		"admin":  {},
		"config": {},
	}

	rs := &api.ReplsetSpec{Name: rsName}
	// Setting the port using the mongo configuration because currently we are not
	// exposing the port on the rs spec as an option.
	err := rs.Configuration.SetPort(port)
	if err != nil {
		return errors.Wrap(err, "failed to set port")
	}

	mc, err := r.mongoClientWithRole(ctx, cr, rs, api.RoleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "dial:")
	}

	defer func() {
		err := mc.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	list, err := mc.ListDBs(ctx)
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

func (r *ReconcilePerconaServerMongoDB) ensureSecurityKey(ctx context.Context, cr *api.PerconaServerMongoDB, secretName, keyName string, keyLen int, setOwner bool) (created bool, err error) {
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

	err = r.client.Get(ctx, types.NamespacedName{Name: key.Name, Namespace: key.Namespace}, key)
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

		key.Labels = naming.ClusterLabels(cr)

		err = r.client.Create(ctx, key)
		if err != nil {
			return false, errors.Wrap(err, "create key")
		}
	} else if err != nil {
		return false, errors.Wrap(err, "get key")
	}

	return created, nil
}

func (r *ReconcilePerconaServerMongoDB) deleteOrphanPVCs(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	for _, f := range cr.GetFinalizers() {
		switch f {
		case "delete-psmdb-pvc":
			logf.FromContext(ctx).Info("The value delete-psmdb-pvc is deprecated and will be deleted in 1.20.0. Use percona.com/delete-psmdb-pvc instead")
			fallthrough
		case naming.FinalizerDeletePVC:
			// remove orphan pvc
			mongodPVCs, err := r.getMongodPVCs(ctx, cr)
			if err != nil {
				return err
			}
			mongodPods, err := r.getMongodPods(ctx, cr)
			if err != nil {
				return err
			}
			mongodPodsMap := make(map[string]bool)
			for _, pod := range mongodPods.Items {
				mongodPodsMap[pod.Name] = true
			}
			for _, pvc := range mongodPVCs.Items {
				if strings.HasPrefix(pvc.Name, psmdbconfig.MongodDataVolClaimName+"-") {
					podName := strings.TrimPrefix(pvc.Name, psmdbconfig.MongodDataVolClaimName+"-")
					if _, ok := mongodPodsMap[podName]; !ok {
						// remove the orphan pvc
						logf.FromContext(ctx).Info("remove orphan pvc", "pvc", pvc.Name)
						err := r.client.Delete(ctx, &pvc)
						if err != nil {
							return errors.Wrapf(err, "failed to delete PVC %s", pvc.Name)
						}
					}
				}
			}
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteCfgIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Sharding.Enabled {
		return nil
	}

	upToDate, err := r.isAllSfsUpToDate(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check is all sfs up to date")
	}

	if !upToDate {
		return nil
	}

	sfsName := cr.Name + "-" + api.ConfigReplSetName
	sfs := psmdb.NewStatefulSet(sfsName, cr.Namespace)

	if err := r.client.Delete(ctx, sfs); err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrapf(err, "failed to delete sfs: %s", sfs.Name)
	}

	svc := corev1.Service{}
	err = r.client.Get(ctx, types.NamespacedName{Name: cr.Name + "-" + api.ConfigReplSetName, Namespace: cr.Namespace}, &svc)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "failed to get config service")
	}

	if k8serrors.IsNotFound(err) {
		return nil
	}

	err = r.client.Delete(ctx, &svc)
	if err != nil {
		return errors.Wrap(err, "failed to delete config service")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) upgradeFCVIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB, newFCV string) error {
	if !cr.Spec.UpgradeOptions.SetFCV || newFCV == "" {
		return nil
	}

	up, err := r.isAllSfsUpToDate(ctx, cr)
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

	fcv, err := r.getFCV(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to get FCV")
	}

	if !canUpgradeVersion(fcv, MajorMinor(fcvsv)) {
		return nil
	}

	err = r.setFCV(ctx, cr, newFCV)
	return errors.Wrap(err, "failed to set FCV")
}

func (r *ReconcilePerconaServerMongoDB) deleteMongos(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	err := r.client.Delete(ctx, psmdb.MongosStatefulset(cr))
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "failed to delete mongos statefulset")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteMongosIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Sharding.Enabled {
		return nil
	}

	upToDate, err := r.isAllSfsUpToDate(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check is all sfs up to date")
	}

	if !upToDate {
		return nil
	}

	ss, err := psmdb.GetMongosServices(ctx, r.client, cr)
	if err != nil {
		return errors.Wrap(err, "failed to list mongos services")
	}

	for _, svc := range ss.Items {
		err = r.client.Delete(ctx, &svc)
		if err != nil && !k8serrors.IsNotFound(err) {
			return errors.Wrap(err, "failed to delete mongos services")
		}
	}

	return r.deleteMongos(ctx, cr)
}

func (r *ReconcilePerconaServerMongoDB) reconcileMongodConfigMaps(ctx context.Context, cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) error {
	for _, rs := range repls {
		name := naming.MongodCustomConfigName(cr, rs)

		if rs.Configuration == "" {
			if err := deleteConfigMapIfExists(ctx, r.client, cr, name); err != nil {
				return errors.Wrap(err, "failed to delete mongod config map")
			}
		} else {
			cm := &corev1.ConfigMap{
				TypeMeta: metav1.TypeMeta{
					APIVersion: "v1",
					Kind:       "ConfigMap",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: cr.Namespace,
					Labels:    naming.RSLabels(cr, rs),
				},
				Data: map[string]string{
					"mongod.conf": string(rs.Configuration),
				},
			}
			if cr.CompareVersion("1.17.0") < 0 {
				cm.Labels = nil
			}
			err := r.createOrUpdateConfigMap(ctx, cr, cm)
			if err != nil {
				return errors.Wrap(err, "create or update config map")
			}
		}

		if !rs.NonVoting.Enabled {
			continue
		}

		name = naming.NonVotingConfigMapName(cr, rs)
		if rs.NonVoting.Configuration == "" {
			if err := deleteConfigMapIfExists(ctx, r.client, cr, name); err != nil {
				return errors.Wrap(err, "failed to delete nonvoting mongod config map")
			}

			continue
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: cr.Namespace,
				Labels:    naming.RSLabels(cr, rs),
			},
			Data: map[string]string{
				"mongod.conf": string(rs.NonVoting.Configuration),
			},
		}
		if cr.CompareVersion("1.17.0") < 0 {
			cm.Labels = nil
		}
		err := r.createOrUpdateConfigMap(ctx, cr, cm)
		if err != nil {
			return errors.Wrap(err, "create or update nonvoting config map")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileMongosConfigMap(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	name := naming.MongosCustomConfigName(cr)

	if !cr.Spec.Sharding.Enabled || cr.Spec.Sharding.Mongos.Configuration == "" {
		err := deleteConfigMapIfExists(ctx, r.client, cr, name)
		if err != nil {
			return errors.Wrap(err, "failed to delete mongos config map")
		}

		return nil
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
			Labels:    naming.MongosLabels(cr),
		},
		Data: map[string]string{
			"mongos.conf": string(cr.Spec.Sharding.Mongos.Configuration),
		},
	}
	if cr.CompareVersion("1.17.0") < 0 {
		cm.Labels = nil
	}
	err := r.createOrUpdateConfigMap(ctx, cr, cm)
	if err != nil {
		return err
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileLogCollectorConfigMaps(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.IsLogCollectorEnabled() {
		if err := deleteConfigMapIfExists(ctx, r.client, cr, logcollector.ConfigMapName(cr.Name)); err != nil {
			return errors.Wrap(err, "failed to delete log collector config map when log collector is disabled")
		}
		return nil
	}

	if cr.Spec.LogCollector.Configuration == "" {
		if err := deleteConfigMapIfExists(ctx, r.client, cr, logcollector.ConfigMapName(cr.Name)); err != nil {
			return errors.Wrap(err, "failed to delete log collector config map when the configuration is empty")
		}
		return nil
	}

	cm := &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      logcollector.ConfigMapName(cr.Name),
			Namespace: cr.Namespace,
			Labels:    naming.ClusterLabels(cr),
		},
		Data: map[string]string{
			logcollector.FluentBitCustomConfigurationFile: cr.Spec.LogCollector.Configuration,
		},
	}

	err := r.createOrUpdateConfigMap(ctx, cr, cm)
	if err != nil {
		return errors.Wrap(err, "create or update config map")
	}

	return nil
}

func deleteConfigMapIfExists(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, cmName string) error {
	configMap := &corev1.ConfigMap{}

	err := cl.Get(ctx, types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      cmName,
	}, configMap)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get config map")
	}

	if k8serrors.IsNotFound(err) {
		return nil
	}

	if !metav1.IsControlledBy(configMap, cr) {
		return nil
	}

	return cl.Delete(ctx, configMap)
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateConfigMap(ctx context.Context, cr *api.PerconaServerMongoDB, configMap *corev1.ConfigMap) error {
	err := setControllerReference(cr, configMap, r.scheme)
	if err != nil {
		return errors.Wrapf(err, "failed to set controller ref for config map %s", configMap.Name)
	}

	currMap := &corev1.ConfigMap{}
	err = r.client.Get(ctx, types.NamespacedName{
		Namespace: configMap.Namespace,
		Name:      configMap.Name,
	}, currMap)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get current configmap")
	}

	if k8serrors.IsNotFound(err) {
		return r.client.Create(ctx, configMap)
	}

	if !util.MapEqual(currMap.Data, configMap.Data) {
		return r.client.Update(ctx, configMap)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileMongos(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if err := r.reconcileMongosStatefulset(ctx, cr); err != nil {
		return errors.Wrap(err, "reconcile mongos")
	}

	if err := r.enableBalancerIfNeeded(ctx, cr); err != nil {
		return errors.Wrap(err, "failed to start balancer")
	}

	if err := r.disableBalancerIfNeeded(ctx, cr); err != nil {
		return errors.Wrap(err, "failed to disable balancer")
	}

	if err := r.deleteMongosIfNeeded(ctx, cr); err != nil {
		return errors.Wrap(err, "delete mongos")
	}

	if err := r.deleteCfgIfNeeded(ctx, cr); err != nil {
		return errors.Wrap(err, "delete config server")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileMongosStatefulset(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	stsList, err := r.getStatefulsetsExceptMongos(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to get all non-mongos sts")
	}
	uptodate, err := r.isStsListUpToDate(ctx, cr, &stsList)
	if err != nil {
		return errors.Wrap(err, "failed to check if all non-mongos sts are up to date")
	}

	rstRunning, err := r.isRestoreRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to check running restores")
	}

	mongosFirst, err := r.shouldUpdateMongosFirst(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "should update mongos first")
	}
	if (!uptodate && !mongosFirst) || rstRunning {
		return nil
	}

	sts := psmdb.MongosStatefulset(cr)
	err = setControllerReference(cr, sts, r.scheme)
	if err != nil {
		return errors.Wrapf(err, "set owner ref for statefulset %s", sts.Name)
	}
	err = r.client.Get(ctx, types.NamespacedName{Name: sts.Name, Namespace: sts.Namespace}, sts)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrapf(err, "get statefulset %s", sts.Name)
	}

	customConfig, err := r.getCustomConfig(ctx, cr.Namespace, naming.MongosCustomConfigName(cr))
	if err != nil {
		return errors.Wrap(err, "check if mongos custom configuration exists")
	}

	cfgPods, err := psmdb.GetRSPods(ctx, r.client, cr, api.ConfigReplSetName)
	if err != nil {
		return errors.Wrap(err, "get configsvr pods")
	}

	// wait all configsvr pods to prevent unnecessary updates to mongos
	if int(cr.Spec.Sharding.ConfigsvrReplSet.Size) > len(cfgPods.Items) {
		return nil
	}

	cfgRs := cr.Spec.Sharding.ConfigsvrReplSet
	cfgInstances := make([]string, 0, len(cfgPods.Items)+len(cr.Spec.Sharding.ConfigsvrReplSet.ExternalNodes))
	for _, pod := range cfgPods.Items {
		host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, cfgRs, false, pod)
		if err != nil {
			return errors.Wrapf(err, "get host for pod '%s'", pod.Name)
		}
		cfgInstances = append(cfgInstances, host)
	}

	for _, ext := range cr.Spec.Sharding.ConfigsvrReplSet.ExternalNodes {
		cfgInstances = append(cfgInstances, ext.Host)
	}

	templateSpec, err := psmdb.MongosTemplateSpec(cr, r.initImage, log, customConfig, cfgInstances)
	if err != nil {
		return errors.Wrapf(err, "create template spec for mongos")
	}

	sslAnn, err := r.sslAnnotation(ctx, cr)
	if err != nil {
		if errors.Is(err, errTLSNotReady) {
			return nil
		}
		return errors.Wrap(err, "failed to get ssl annotations")
	}
	if templateSpec.Annotations == nil {
		templateSpec.Annotations = make(map[string]string)
	}

	for k, v := range sslAnn {
		templateSpec.Annotations[k] = v
	}

	secret := new(corev1.Secret)
	err = r.client.Get(ctx, types.NamespacedName{Name: api.UserSecretName(cr), Namespace: cr.Namespace}, secret)
	if client.IgnoreNotFound(err) != nil {
		return errors.Wrapf(err, "check pmm secrets: %s", api.UserSecretName(cr))
	}
	pmmC := pmm.Container(ctx, cr, secret, cfgRs.GetPort(), cr.Spec.PMM.MongosParams)
	if pmmC != nil {
		templateSpec.Spec.Containers = append(
			templateSpec.Spec.Containers,
			*pmmC,
		)
	}

	pvcs := cr.Spec.Sharding.Mongos.SidecarPVCs
	if err := ensurePVCs(ctx, r.client, cr.Namespace, pvcs); err != nil {
		return errors.Wrap(err, "ensure pvc")
	}

	sts.Spec = psmdb.MongosStatefulsetSpec(cr, templateSpec)

	err = r.createOrUpdate(ctx, sts)
	if err != nil {
		return errors.Wrapf(err, "update or create mongos %s", sts)
	}

	err = r.reconcilePDB(ctx, cr, cr.Spec.Sharding.Mongos.PodDisruptionBudget, templateSpec.Labels, cr.Namespace, sts)
	if err != nil {
		return errors.Wrap(err, "reconcile PodDisruptionBudget for mongos")
	}

	err = r.smartMongosUpdate(ctx, cr, sts)
	if err != nil {
		return errors.Wrap(err, "smart update")
	}

	return nil
}

func ensurePVCs(
	ctx context.Context,
	cl client.Client,
	namespace string,
	pvcs []corev1.PersistentVolumeClaim,
) error {
	for _, pvc := range pvcs {
		// ignore pvc namespace
		pvc.Namespace = namespace

		err := cl.Get(ctx,
			types.NamespacedName{Namespace: pvc.Namespace, Name: pvc.Name},
			&corev1.PersistentVolumeClaim{})
		if err == nil {
			// already exists
			continue
		}

		if !k8serrors.IsNotFound(err) {
			return errors.Wrapf(err, "get %v/%v", pvc.Namespace, pvc.Name)
		}

		if err := cl.Create(ctx, &pvc); err != nil {
			return errors.Wrapf(err, "create PVC %v/%v", pvc.Namespace, pvc.Name)
		}
	}

	return nil
}

var errTLSNotReady = errors.New("waiting for TLS secret")

func (r *ReconcilePerconaServerMongoDB) sslAnnotation(ctx context.Context, cr *api.PerconaServerMongoDB) (map[string]string, error) {
	annotation := make(map[string]string)

	annotation["percona.com/ssl-hash"] = ""
	annotation["percona.com/ssl-internal-hash"] = ""

	getHash := func(secret *corev1.Secret) string {
		secretString := fmt.Sprintln(secret.Data)
		return fmt.Sprintf("%x", md5.Sum([]byte(secretString)))
	}

	getSecret := func(name string) (*corev1.Secret, error) {
		secretObj := corev1.Secret{}
		err := r.client.Get(ctx,
			types.NamespacedName{
				Namespace: cr.Namespace,
				Name:      name,
			},
			&secretObj,
		)
		if err != nil {
			return nil, err
		}
		return &secretObj, nil
	}

	sslSecret, err := getSecret(api.SSLSecretName(cr))
	if err != nil {
		if k8serrors.IsNotFound(err) {
			if cr.UnsafeTLSDisabled() {
				return annotation, nil
			}
			return nil, errTLSNotReady
		}
	}
	annotation["percona.com/ssl-hash"] = getHash(sslSecret)

	sslInternalSecret, err := getSecret(api.SSLInternalSecretName(cr))
	if err != nil {
		if k8serrors.IsNotFound(err) {
			isCustomSecret, serr := tls.IsSecretCreatedByUser(ctx, r.client, cr, sslSecret)
			if serr != nil {
				return nil, errors.Wrap(serr, "failed to check if secret is created by user")
			}
			if cr.UnsafeTLSDisabled() || isCustomSecret {
				return annotation, nil
			}
			return nil, errTLSNotReady
		}
	}
	annotation["percona.com/ssl-internal-hash"] = getHash(sslInternalSecret)

	return annotation, nil
}

func (r *ReconcilePerconaServerMongoDB) getTLSHash(ctx context.Context, cr *api.PerconaServerMongoDB, secretName string) (string, error) {
	secretObj := corev1.Secret{}
	err := r.client.Get(ctx,
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

func (r *ReconcilePerconaServerMongoDB) reconcilePDB(ctx context.Context, cr *api.PerconaServerMongoDB, spec *api.PodDisruptionBudgetSpec, labels map[string]string, namespace string, owner client.Object) error {
	if spec == nil {
		return nil
	}

	metaAccessor, ok := owner.(metav1.ObjectMetaAccessor)
	if !ok {
		return errors.New("can't convert object to ObjectMetaAccessor")
	}

	ownerMeta := metaAccessor.GetObjectMeta()

	if ownerMeta.GetUID() == "" {
		err := r.client.Get(ctx, types.NamespacedName{
			Name:      ownerMeta.GetName(),
			Namespace: ownerMeta.GetNamespace(),
		}, owner)
		if err != nil {
			return errors.Wrap(err, "failed to get owner uid for pdb")
		}
	}

	pdb := psmdb.PodDisruptionBudget(spec, labels, namespace)
	if cr.CompareVersion("1.17.0") < 0 {
		pdb.Labels = nil
	}
	err := setControllerReference(owner, pdb, r.scheme)
	if err != nil {
		return errors.Wrap(err, "set owner reference")
	}

	return r.createOrUpdate(ctx, pdb)
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdate(ctx context.Context, obj client.Object) error {
	log := logf.FromContext(ctx).WithValues(
		"name", obj.GetName(),
		"kind", obj.GetObjectKind(),
		"generation", obj.GetGeneration(),
		"resourceVersion", obj.GetResourceVersion(),
	)

	status, err := util.Apply(ctx, r.client, obj)

	switch status {
	case util.ApplyStatusCreated:
		log.V(1).Info("Object created")
	case util.ApplyStatusUpdated:
		log.V(1).Info("Object updated")
	}
	return err
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateSvc(ctx context.Context, cr *api.PerconaServerMongoDB, svc *corev1.Service, saveOldMeta bool) error {
	if !saveOldMeta && len(cr.Spec.IgnoreAnnotations) == 0 && len(cr.Spec.IgnoreLabels) == 0 {
		return r.createOrUpdate(ctx, svc)
	}
	oldSvc := new(corev1.Service)
	err := r.client.Get(ctx, types.NamespacedName{
		Name:      svc.GetName(),
		Namespace: svc.GetNamespace(),
	}, oldSvc)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return r.createOrUpdate(ctx, svc)
		}
		return errors.Wrap(err, "get object")
	}

	if saveOldMeta {
		svc.SetAnnotations(util.MapMerge(oldSvc.GetAnnotations(), svc.GetAnnotations()))
		svc.SetLabels(util.MapMerge(oldSvc.GetLabels(), svc.GetLabels()))
	}
	setIgnoredAnnotations(cr, svc, oldSvc)
	setIgnoredLabels(cr, svc, oldSvc)
	return r.createOrUpdate(ctx, svc)
}

func setIgnoredAnnotations(cr *api.PerconaServerMongoDB, obj, oldObject client.Object) {
	oldAnnotations := oldObject.GetAnnotations()
	if len(oldAnnotations) == 0 {
		return
	}

	ignoredAnnotations := util.MapFilterByKeys(oldAnnotations, cr.Spec.IgnoreAnnotations)

	annotations := util.MapMerge(obj.GetAnnotations(), ignoredAnnotations)
	obj.SetAnnotations(annotations)
}

func setIgnoredLabels(cr *api.PerconaServerMongoDB, obj, oldObject client.Object) {
	oldLabels := oldObject.GetLabels()
	if len(oldLabels) == 0 {
		return
	}

	ignoredLabels := util.MapFilterByKeys(oldLabels, cr.Spec.IgnoreLabels)

	labels := util.MapMerge(obj.GetLabels(), ignoredLabels)
	obj.SetLabels(labels)
}

func setControllerReference(owner client.Object, obj metav1.Object, scheme *runtime.Scheme) error {
	ownerRef, err := OwnerRef(owner, scheme)
	if err != nil {
		return err
	}
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
	return nil
}

// OwnerRef returns OwnerReference to object
func OwnerRef(ro client.Object, scheme *runtime.Scheme) (metav1.OwnerReference, error) {
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

func (r *ReconcilePerconaServerMongoDB) getCustomConfig(ctx context.Context, namespace, name string) (psmdbconfig.CustomConfig, error) {
	n := types.NamespacedName{
		Namespace: namespace,
		Name:      name,
	}

	sources := []psmdbconfig.VolumeSourceType{
		psmdbconfig.VolumeSourceSecret,
		psmdbconfig.VolumeSourceConfigMap,
	}

	for _, s := range sources {
		obj := psmdbconfig.VolumeSourceTypeToObj(s)

		ok, err := getObjectByName(ctx, r.client, n, obj.GetRuntimeObject())
		if err != nil {
			return psmdbconfig.CustomConfig{}, errors.Wrapf(err, "get %s", s)
		}
		if !ok {
			continue
		}

		hashHex, err := obj.GetHashHex()
		if err != nil {
			return psmdbconfig.CustomConfig{}, errors.Wrapf(err, "failed to get hash of %s", s)
		}

		conf := psmdbconfig.CustomConfig{
			Type:    s,
			HashHex: hashHex,
		}

		return conf, nil
	}

	return psmdbconfig.CustomConfig{}, nil
}

func getObjectByName(ctx context.Context, c client.Client, n types.NamespacedName, obj client.Object) (bool, error) {
	err := c.Get(ctx, n, obj)
	if err != nil && !k8serrors.IsNotFound(err) {
		return false, err
	}

	// object exists
	if err == nil {
		return true, nil
	}

	return false, nil
}
