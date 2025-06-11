package perconaservermongodb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"sync/atomic"

	v "github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
)

type Schedule struct {
	ID           cron.EntryID
	CronSchedule string
}

type JobPrefix string

const (
	EnsureVersion JobPrefix = "ensure-version"
	Telemetry     JobPrefix = "telemetry"
)

func jobName(prefix JobPrefix, cr *api.PerconaServerMongoDB) string {
	nn := types.NamespacedName{
		Name:      cr.Name,
		Namespace: cr.Namespace,
	}

	return fmt.Sprintf("%s/%s", prefix, nn.String())
}

func (r *ReconcilePerconaServerMongoDB) deleteCronJob(jobName string) {
	job, ok := r.crons.ensureVersionJobs.LoadAndDelete(jobName)
	if !ok {
		return
	}
	r.crons.crons.Remove(job.(Schedule).ID)
}

func (r *ReconcilePerconaServerMongoDB) scheduleEnsureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService) error {
	jn := jobName(EnsureVersion, cr)

	log := logf.FromContext(ctx).WithValues("job", jn)

	scheduleRaw, ok := r.crons.ensureVersionJobs.Load(jn)
	if cr.Spec.UpgradeOptions.Schedule == "" || !(versionUpgradeEnabled(cr) || telemetryEnabled()) {
		if ok {
			r.deleteCronJob(jn)
		}
		return nil
	}

	schedule := Schedule{}
	if ok {
		schedule = scheduleRaw.(Schedule)
	}

	if ok && schedule.CronSchedule == cr.Spec.UpgradeOptions.Schedule {
		return nil
	}

	if ok {
		log.Info("remove job because of new", "old", schedule.CronSchedule, "new", cr.Spec.UpgradeOptions.Schedule)
		r.deleteCronJob(jn)
	}

	nn := types.NamespacedName{
		Name:      cr.Name,
		Namespace: cr.Namespace,
	}

	l := r.lockers.LoadOrCreate(nn.String())

	id, err := r.crons.AddFuncWithSeconds(cr.Spec.UpgradeOptions.Schedule, func() {
		l.statusMutex.Lock()
		defer l.statusMutex.Unlock()

		if !atomic.CompareAndSwapInt32(l.updateSync, updateDone, updateWait) {
			return
		}

		localCr := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
		if k8serrors.IsNotFound(err) {
			log.Info("cluster is not found, deleting the job",
				"name", jn, "cluster", cr.Name, "namespace", cr.Namespace)
			r.deleteCronJob(jn)
			return
		}

		if localCr.Status.State != api.AppStateReady {
			log.Info("cluster is not ready")
			return
		}

		err = localCr.CheckNSetDefaults(ctx, r.serverVersion.Platform)
		if err != nil {
			log.Error(err, "failed to set defaults for CR")
			return
		}

		err = r.ensureVersion(ctx, localCr, vs)
		if err != nil {
			log.Error(err, "failed to ensure version")
		}
	})
	if err != nil {
		return err
	}

	log.Info("add new job", "name", jn, "schedule", cr.Spec.UpgradeOptions.Schedule)

	r.crons.ensureVersionJobs.Store(jn, Schedule{
		ID:           id,
		CronSchedule: cr.Spec.UpgradeOptions.Schedule,
	})

	return nil
}

// passed version should have format "Major.Minior"
func canUpgradeVersion(fcv, new string) bool {
	if fcv >= new {
		return false
	}

	switch fcv {
	case "3.6":
		return new == "4.0"
	case "4.0":
		return new == "4.2"
	case "4.2":
		return new == "4.4"
	case "4.4":
		return new == "5.0"
	case "5.0":
		return new == "6.0"
	case "6.0":
		return new == "7.0"
	case "7.0":
		return new == "8.0"
	default:
		return false
	}
}

type UpgradeRequest struct {
	Ok         bool
	Apply      string
	NewVersion string
}

func MajorMinor(ver *v.Version) string {
	s := ver.Segments()

	if len(s) == 1 {
		s = append(s, 0)
	}

	return fmt.Sprintf("%d.%d", s[0], s[1])
}

func majorUpgradeRequested(cr *api.PerconaServerMongoDB, fcv string) (UpgradeRequest, error) {
	if len(cr.Spec.UpgradeOptions.Apply) == 0 || api.OneOfUpgradeStrategy(string(cr.Spec.UpgradeOptions.Apply)) {
		return UpgradeRequest{false, "", ""}, nil
	}

	apply := ""
	ver := string(cr.Spec.UpgradeOptions.Apply)

	applySp := strings.Split(string(cr.Spec.UpgradeOptions.Apply), "-")
	if len(applySp) > 1 && api.OneOfUpgradeStrategy(applySp[1]) {
		// if CR has "apply: 4.2-recommended"
		// 4.2 will go to version
		// recommended will go to apply
		apply = applySp[1]
		ver = applySp[0]
	}

	newVer, err := v.NewSemver(ver)
	if err != nil {
		return UpgradeRequest{false, "", ""}, errors.Wrap(err, "faied to make semver")
	}

	if len(cr.Status.MongoVersion) == 0 {
		// means cluster is starting
		// so we do not need to check is we can upgrade
		return UpgradeRequest{true, apply, ver}, nil
	}

	mongoVer, err := v.NewSemver(cr.Status.MongoVersion)
	if err != nil {
		return UpgradeRequest{false, "", ""}, errors.Wrap(err, "failed to make semver")
	}

	newMM := MajorMinor(newVer)
	mongoMM := MajorMinor(mongoVer)

	if newMM > mongoMM {
		if !canUpgradeVersion(fcv, newMM) {
			return UpgradeRequest{false, "", ""}, errors.Errorf("can't upgrade to %s with FCV set to %s", ver, fcv)
		}

		return UpgradeRequest{true, apply, ver}, nil
	}

	if newMM < mongoMM {
		if newMM != fcv {
			return UpgradeRequest{false, "", ""}, errors.Errorf("can't upgrade to %s with FCV set to %s", ver, fcv)
		}

		return UpgradeRequest{true, apply, ver}, nil
	}

	return UpgradeRequest{false, "", ""}, nil
}

func telemetryEnabled() bool {
	value, ok := os.LookupEnv("DISABLE_TELEMETRY")
	if ok {
		return value != "true"
	}
	return true
}

func versionUpgradeEnabled(cr *api.PerconaServerMongoDB) bool {
	return cr.Spec.UpgradeOptions.Apply.Lower() != api.UpgradeStrategyNever &&
		cr.Spec.UpgradeOptions.Apply.Lower() != api.UpgradeStrategyDisabled
}

func (r *ReconcilePerconaServerMongoDB) getVersionMeta(ctx context.Context, cr *api.PerconaServerMongoDB, operatorDepl *appsv1.Deployment) (VersionMeta, error) {
	watchNs, err := k8s.GetWatchNamespace()
	if err != nil {
		return VersionMeta{}, errors.Wrap(err, "get WATCH_NAMESPACE env variable")
	}
	vm := VersionMeta{
		Apply:                  string(cr.Spec.UpgradeOptions.Apply),
		CRUID:                  string(cr.GetUID()),
		Version:                cr.Version().String(),
		PMMEnabled:             cr.Spec.PMM.Enabled,
		PMMVersion:             cr.Status.PMMVersion,
		MCSEnabled:             cr.Spec.MultiCluster.Enabled,
		KubeVersion:            r.serverVersion.Info.GitVersion,
		PITREnabled:            cr.Spec.Backup.PITR.Enabled,
		ClusterSize:            cr.Status.Size,
		MongoVersion:           cr.Status.MongoVersion,
		BackupVersion:          cr.Status.BackupVersion,
		BackupsEnabled:         cr.Spec.Backup.Enabled && len(cr.Spec.Backup.Storages) > 0,
		ShardingEnabled:        cr.Spec.Sharding.Enabled,
		ClusterWideEnabled:     len(watchNs) == 0 || len(strings.Split(watchNs, ",")) > 1,
		HashicorpVaultEnabled:  len(cr.Spec.Secrets.Vault) > 0,
		RoleManagementEnabled:  len(cr.Spec.Roles) > 0,
		UserManagementEnabled:  len(cr.Spec.Users) > 0,
		VolumeExpansionEnabled: cr.Spec.VolumeExpansionEnabled,
	}

	if cr.Spec.Platform != nil {
		vm.Platform = string(*cr.Spec.Platform)
	}

	for _, rs := range cr.Spec.Replsets {
		if len(rs.Sidecars) > 0 {
			vm.SidecarsUsed = true
			break
		}
	}

	if _, ok := operatorDepl.Labels["helm.sh/chart"]; ok {
		vm.HelmDeployOperator = true
	}

	if _, ok := cr.Labels["helm.sh/chart"]; ok {
		vm.HelmDeployCR = true
	}

	for _, task := range cr.Spec.Backup.Tasks {
		if task.Type == defs.PhysicalBackup && task.Enabled {
			vm.PhysicalBackupScheduled = true
			break
		}
	}

	fcv := ""
	if cr.Status.MongoVersion != "" {
		f, err := r.getFCV(ctx, cr)
		if err != nil {
			return VersionMeta{}, errors.Wrap(err, "failed to get FCV")
		}

		fcv = f
	}
	req, err := majorUpgradeRequested(cr, fcv)
	if err != nil {
		return VersionMeta{}, errors.Wrap(err, "failed to check if major update requested")
	}
	if req.Ok {
		if len(req.Apply) != 0 {
			vm.Apply = req.Apply
			vm.MongoVersion = req.NewVersion
		} else {
			vm.Apply = req.NewVersion
		}
	}

	return vm, nil
}

func (r *ReconcilePerconaServerMongoDB) getNewVersions(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService, operatorDepl *appsv1.Deployment) (DepVersion, error) {
	log := logf.FromContext(ctx)

	endpoint := api.GetDefaultVersionServiceEndpoint()
	log.V(1).Info("Use version service endpoint", "endpoint", endpoint)

	vm, err := r.getVersionMeta(ctx, cr, operatorDepl)
	if err != nil {
		return DepVersion{}, errors.Wrap(err, "get version meta")
	}

	log.V(1).Info("Sending request to version service", "meta", vm)

	if telemetryEnabled() && (!versionUpgradeEnabled(cr) || cr.Spec.UpgradeOptions.VersionServiceEndpoint != endpoint) {
		_, err = vs.GetExactVersion(cr, endpoint, vm)
		if err != nil {
			log.Error(err, "failed to send telemetry to "+api.GetDefaultVersionServiceEndpoint())
		}
		return DepVersion{}, nil
	}

	versions, err := vs.GetExactVersion(cr, cr.Spec.UpgradeOptions.VersionServiceEndpoint, vm)
	if err != nil {
		return DepVersion{}, errors.Wrap(err, "failed to check version")
	}

	return versions, nil
}

func (r *ReconcilePerconaServerMongoDB) getOperatorDeployment(ctx context.Context) (*appsv1.Deployment, error) {
	ns, err := k8s.GetOperatorNamespace()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get operator namespace")
	}
	name, err := os.Hostname()
	if err != nil {
		return nil, errors.Wrap(err, "failed to get operator hostname")
	}

	pod := new(corev1.Pod)
	err = r.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, pod)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get operator pod")
	}
	if len(pod.OwnerReferences) == 0 {
		return nil, errors.New("operator pod has no owner reference")
	}

	rs := new(appsv1.ReplicaSet)
	err = r.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: pod.OwnerReferences[0].Name}, rs)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get operator replicaset")
	}
	if len(rs.OwnerReferences) == 0 {
		return nil, errors.New("operator replicaset has no owner reference")
	}

	depl := new(appsv1.Deployment)
	err = r.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: rs.OwnerReferences[0].Name}, depl)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get operator deployment")
	}

	return depl, nil
}

func (r *ReconcilePerconaServerMongoDB) scheduleTelemetryRequests(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService) error {
	jn := jobName(Telemetry, cr)

	log := logf.FromContext(ctx).WithValues("job", jn)

	scheduleRaw, ok := r.crons.ensureVersionJobs.Load(jn)
	if !telemetryEnabled() {
		if ok {
			r.deleteCronJob(jn)
		}
		return nil
	}

	schedule := Schedule{}
	if ok {
		schedule = scheduleRaw.(Schedule)
	}

	sch, found := os.LookupEnv("TELEMETRY_SCHEDULE")
	if !found {
		sch = fmt.Sprintf("%d * * * *", rand.Intn(60))
	}

	if ok && !found {
		return nil
	}

	if found && schedule.CronSchedule == sch {
		return nil
	}

	if ok {
		log.Info("remove job because of new", "old", schedule.CronSchedule, "new", sch)
		r.deleteCronJob(jn)
	}

	id, err := r.crons.AddFuncWithSeconds(sch, func() {
		localCr := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
		if k8serrors.IsNotFound(err) {
			log.Info("cluster is not found, deleting the job",
				"name", jn, "cluster", cr.Name, "namespace", cr.Namespace)
			r.deleteCronJob(jn)
			return
		}
		if err != nil {
			log.Error(err, "failed to get CR")
			return
		}

		if localCr.Status.State != api.AppStateReady {
			log.Info("cluster is not ready")
			return
		}

		err = localCr.CheckNSetDefaults(ctx, r.serverVersion.Platform)
		if err != nil {
			log.Error(err, "failed to set defaults for CR")
			return
		}

		operatorDepl, err := r.getOperatorDeployment(ctx)
		if err != nil {
			log.Error(err, "failed to get operator deployment")
			return
		}

		_, err = r.getNewVersions(ctx, localCr, vs, operatorDepl)
		if err != nil {
			log.Error(err, "failed to send telemetry")
		}
	})
	if err != nil {
		return err
	}

	log.Info("add new job", "name", jn, "schedule", sch)

	r.crons.ensureVersionJobs.Store(jn, Schedule{
		ID:           id,
		CronSchedule: sch,
	})

	operatorDepl, err := r.getOperatorDeployment(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to get operator deployment")
	}

	// send telemetry on startup
	_, err = r.getNewVersions(ctx, cr, vs, operatorDepl)
	if err != nil {
		log.Error(err, "failed to send telemetry")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) ensureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService) error {
	log := logf.FromContext(ctx)

	if !(versionUpgradeEnabled(cr) || telemetryEnabled()) {
		return nil
	}

	if cr.Status.State != api.AppStateReady && cr.Status.MongoVersion != "" {
		return errors.New("cluster is not ready")
	}

	operatorDepl, err := r.getOperatorDeployment(ctx)
	if err != nil {
		return errors.Wrap(err, "failed to get operator deployment")
	}

	vm, err := r.getVersionMeta(ctx, cr, operatorDepl)
	if err != nil {
		return errors.Wrap(err, "failed to get version meta")
	}

	if !versionUpgradeEnabled(cr) {
		return nil
	}

	newVersion, err := vs.GetExactVersion(cr, cr.Spec.UpgradeOptions.VersionServiceEndpoint, vm)
	if err != nil {
		return errors.Wrap(err, "failed to check version")
	}

	patch := client.MergeFrom(cr.DeepCopy())
	if cr.Spec.Image != newVersion.MongoImage {
		if cr.Status.MongoVersion == "" {
			log.Info("Set Mongo version", "newVersion", newVersion.MongoImage)
		} else {
			log.Info("Update Mongo version", "newVersion", newVersion.MongoImage, "oldVersion", cr.Status.MongoVersion)
		}
		cr.Spec.Image = newVersion.MongoImage
	}

	if cr.Spec.Backup.Image != newVersion.BackupImage {
		if cr.Status.BackupVersion == "" {
			log.Info("Set backup version", "newVersion", newVersion.BackupVersion)
		} else {
			log.Info("Update backup version", "newVersion", newVersion.BackupVersion, "oldVersion", cr.Status.BackupVersion)
		}
		cr.Spec.Backup.Image = newVersion.BackupImage
	}

	if cr.Spec.PMM.Image != newVersion.PMMImage {
		if cr.Status.PMMVersion == "" {
			log.Info("Set PMM version", "newVersion", newVersion.PMMVersion)
		} else {
			log.Info("Update PMM version", "newVersion", newVersion.PMMVersion, "oldVersion", cr.Status.PMMVersion)
		}
		cr.Spec.PMM.Image = newVersion.PMMImage
	}

	err = r.client.Patch(ctx, cr.DeepCopy(), patch)
	if err != nil {
		return errors.Wrap(err, "failed to update CR")
	}

	cr.Status.PMMVersion = newVersion.PMMVersion
	cr.Status.BackupVersion = newVersion.BackupVersion
	cr.Status.MongoVersion = newVersion.MongoVersion
	cr.Status.MongoImage = newVersion.MongoImage

	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &api.PerconaServerMongoDB{}

		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		c.Status.PMMVersion = newVersion.PMMVersion
		c.Status.BackupVersion = newVersion.BackupVersion
		c.Status.MongoVersion = newVersion.MongoVersion
		c.Status.MongoImage = newVersion.MongoImage

		return r.client.Status().Update(ctx, c)
	})
}

func (r *ReconcilePerconaServerMongoDB) fetchVersionFromMongo(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	log := logf.FromContext(ctx)

	if cr.Status.ObservedGeneration != cr.ObjectMeta.Generation ||
		cr.Status.State != api.AppStateReady ||
		cr.Status.MongoImage == cr.Spec.Image {
		return nil
	}

	session, err := r.MongoClient().Mongo(ctx, cr, replset, api.RoleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "dial")
	}

	defer func() {
		err := session.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	info, err := session.RSBuildInfo(ctx)
	if err != nil {
		return errors.Wrap(err, "get build info")
	}

	log.Info(fmt.Sprintf("update Mongo version to %v (fetched from db)", info.Version))
	cr.Status.MongoVersion = info.Version
	cr.Status.MongoImage = cr.Spec.Image

	// updating status resets our defaults, so we're passing a copy
	err = r.client.Status().Update(ctx, cr.DeepCopy())
	return errors.Wrapf(err, "failed to update CR")
}
