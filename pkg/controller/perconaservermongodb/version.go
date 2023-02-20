package perconaservermongodb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	v "github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func (r *ReconcilePerconaServerMongoDB) deleteEnsureVersion(cr *api.PerconaServerMongoDB, id int) {
	r.crons.crons.Remove(cron.EntryID(id))
	delete(r.crons.jobs, jobName(cr))
}

func (r *ReconcilePerconaServerMongoDB) scheduleEnsureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService) error {
	log := logf.FromContext(ctx)
	schedule, ok := r.crons.jobs[jobName(cr)]
	if cr.Spec.UpgradeOptions.Schedule == "" || !(versionUpgradeEnabled(cr) || telemetryEnabled()) {
		if ok {
			r.deleteEnsureVersion(cr, schedule.ID)
		}

		return nil
	}

	if ok && schedule.CronShedule == cr.Spec.UpgradeOptions.Schedule {
		return nil
	}

	if ok {
		log.Info("remove job because of new", "old", schedule.CronShedule, "new", cr.Spec.UpgradeOptions.Schedule)
		r.deleteEnsureVersion(cr, schedule.ID)
	}

	nn := types.NamespacedName{
		Name:      cr.Name,
		Namespace: cr.Namespace,
	}

	l := r.lockers.LoadOrCreate(nn.String())

	id, err := r.crons.crons.AddFunc(cr.Spec.UpgradeOptions.Schedule, func() {
		l.statusMutex.Lock()
		defer l.statusMutex.Unlock()

		if !atomic.CompareAndSwapInt32(l.updateSync, updateDone, updateWait) {
			return
		}

		localCr := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
		if err != nil {
			log.Error(err, "failed to get CR")
			return
		}

		if localCr.Status.State != v1.AppStateReady {
			log.Info("cluster is not ready")
			return
		}

		err = localCr.CheckNSetDefaults(r.serverVersion.Platform, log)
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

	jn := jobName(cr)
	log.Info("add new job", "name", jn, "schedule", cr.Spec.UpgradeOptions.Schedule)
	r.crons.jobs[jn] = Shedule{
		ID:          int(id),
		CronShedule: cr.Spec.UpgradeOptions.Schedule,
	}

	return nil
}

func jobName(cr *api.PerconaServerMongoDB) string {
	jobName := "ensure-version"
	nn := types.NamespacedName{
		Name:      cr.Name,
		Namespace: cr.Namespace,
	}

	return fmt.Sprintf("%s/%s", jobName, nn.String())
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
	if len(cr.Spec.UpgradeOptions.Apply) == 0 || v1.OneOfUpgradeStrategy(string(cr.Spec.UpgradeOptions.Apply)) {
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
		Apply:                 string(cr.Spec.UpgradeOptions.Apply),
		KubeVersion:           r.serverVersion.Info.GitVersion,
		MongoVersion:          cr.Status.MongoVersion,
		PMMVersion:            cr.Status.PMMVersion,
		BackupVersion:         cr.Status.BackupVersion,
		CRUID:                 string(cr.GetUID()),
		Version:               cr.Version().String(),
		ShardingEnabled:       cr.Spec.Sharding.Enabled,
		HashicorpVaultEnabled: len(cr.Spec.Secrets.Vault) > 0,
		ClusterWideEnabled:    len(watchNs) == 0,
		PMMEnabled:            cr.Spec.PMM.Enabled,
		ClusterSize:           cr.Status.Size,
		PITREnabled:           cr.Spec.Backup.PITR.Enabled,
	}
	if cr.Spec.Platform != nil {
		vm.Platform = string(*cr.Spec.Platform)
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

	if len(cr.Spec.Backup.Storages) > 0 && cr.Spec.Backup.Enabled {
		vm.BackupsEnabled = true
	}

	for _, task := range cr.Spec.Backup.Tasks {
		if task.Type == pbm.PhysicalBackup && task.Enabled {
			vm.PhysicalBackupScheduled = true
			break
		}
	}

	return vm, nil
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

func (r *ReconcilePerconaServerMongoDB) ensureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService) error {
	log := logf.FromContext(ctx)

	if !(versionUpgradeEnabled(cr) || telemetryEnabled()) {
		return nil
	}

	if cr.Status.State != v1.AppStateReady && cr.Status.MongoVersion != "" {
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

	if telemetryEnabled() && (!versionUpgradeEnabled(cr) || cr.Spec.UpgradeOptions.VersionServiceEndpoint != api.GetDefaultVersionServiceEndpoint()) {
		_, err = vs.GetExactVersion(cr, api.GetDefaultVersionServiceEndpoint(), vm)
		if err != nil {
			log.Error(err, "failed to send telemetry to "+api.GetDefaultVersionServiceEndpoint())
		}
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

	session, err := r.mongoClientWithRole(ctx, cr, *replset, roleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "dial")
	}

	defer func() {
		err := session.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	info, err := mongo.RSBuildInfo(ctx, session)
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
