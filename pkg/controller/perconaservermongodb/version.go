package perconaservermongodb

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"sync/atomic"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/versionservice"
)

type jobSchedule struct {
	ID           cron.EntryID
	CronSchedule string
}

type jobPrefix string

const (
	ensureVersionPrefix jobPrefix = "ensure-version"
	telemetryPrefix     jobPrefix = "telemetry"
)

func jobName(prefix jobPrefix, cr *api.PerconaServerMongoDB) string {
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
	schedule, ok := job.(jobSchedule)
	if !ok {
		return
	}
	r.crons.crons.Remove(schedule.ID)
}

func (r *ReconcilePerconaServerMongoDB) scheduleEnsureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs versionservice.Service) error {
	jn := jobName(ensureVersionPrefix, cr)

	log := logf.FromContext(ctx).WithValues("job", jn)

	scheduleRaw, ok := r.crons.ensureVersionJobs.Load(jn)
	if cr.Spec.UpgradeOptions.Schedule == "" || (!versionservice.UpgradeEnabled(cr) && !versionservice.TelemetryEnabled()) {
		if ok {
			r.deleteCronJob(jn)
		}
		return nil
	}

	schedule := jobSchedule{}
	if ok {
		schedule, _ = scheduleRaw.(jobSchedule)
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
		if err != nil {
			log.Error(err, "get CR")
			return
		}

		if localCr.Status.State != api.AppStateReady {
			log.Info("cluster is not ready")
			return
		}

		err = localCr.CheckNSetDefaults(ctx, r.serverVersion.Platform)
		if err != nil {
			log.Error(err, "set defaults for CR")
			return
		}

		err = r.ensureVersion(ctx, localCr, vs)
		if err != nil {
			log.Error(err, "ensure version")
		}
	})
	if err != nil {
		return err
	}

	log.Info("add new job", "name", jn, "schedule", cr.Spec.UpgradeOptions.Schedule)

	r.crons.ensureVersionJobs.Store(jn, jobSchedule{
		ID:           id,
		CronSchedule: cr.Spec.UpgradeOptions.Schedule,
	})

	return nil
}

func (r *ReconcilePerconaServerMongoDB) buildVersionMeta(ctx context.Context, cr *api.PerconaServerMongoDB, operatorDepl *appsv1.Deployment) (versionservice.Meta, error) {
	fcv := ""
	if cr.Status.MongoVersion != "" {
		f, err := r.getFCV(ctx, cr)
		if err != nil {
			return versionservice.Meta{}, errors.Wrap(err, "get FCV")
		}
		fcv = f
	}

	return versionservice.BuildMeta(ctx, r.client, cr, operatorDepl, r.serverVersion.Info.GitVersion, fcv)
}

func (r *ReconcilePerconaServerMongoDB) getNewVersions(ctx context.Context, cr *api.PerconaServerMongoDB, vs versionservice.Service, operatorDepl *appsv1.Deployment) (versionservice.Dep, error) {
	log := logf.FromContext(ctx)

	endpoint := api.GetDefaultVersionServiceEndpoint()
	log.V(1).Info("Use version service endpoint", "endpoint", endpoint)

	vm, err := r.buildVersionMeta(ctx, cr, operatorDepl)
	if err != nil {
		return versionservice.Dep{}, errors.Wrap(err, "get version meta")
	}

	log.V(1).Info("Sending request to version service", "meta", vm)

	if versionservice.TelemetryEnabled() && (!versionservice.UpgradeEnabled(cr) || cr.Spec.UpgradeOptions.VersionServiceEndpoint != endpoint) {
		_, err = vs.GetExactVersion(cr, endpoint, vm)
		if err != nil {
			log.Error(err, "send telemetry", "endpoint", api.GetDefaultVersionServiceEndpoint())
		}
		return versionservice.Dep{}, nil
	}

	versions, err := vs.GetExactVersion(cr, cr.Spec.UpgradeOptions.VersionServiceEndpoint, vm)
	if err != nil {
		return versionservice.Dep{}, errors.Wrap(err, "check version")
	}

	return versions, nil
}

func (r *ReconcilePerconaServerMongoDB) getOperatorDeployment(ctx context.Context) (*appsv1.Deployment, error) {
	ns, err := k8s.GetOperatorNamespace()
	if err != nil {
		return nil, errors.Wrap(err, "get operator namespace")
	}
	name, err := os.Hostname()
	if err != nil {
		return nil, errors.Wrap(err, "get operator hostname")
	}

	pod := new(corev1.Pod)
	err = r.client.Get(ctx, types.NamespacedName{Namespace: ns, Name: name}, pod)
	if err != nil {
		return nil, errors.Wrap(err, "get operator pod")
	}
	if len(pod.OwnerReferences) == 0 {
		return nil, errors.New("operator pod has no owner reference")
	}

	rs := new(appsv1.ReplicaSet)
	err = r.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: pod.OwnerReferences[0].Name}, rs)
	if err != nil {
		return nil, errors.Wrap(err, "get operator replicaset")
	}
	if len(rs.OwnerReferences) == 0 {
		return nil, errors.New("operator replicaset has no owner reference")
	}

	depl := new(appsv1.Deployment)
	err = r.client.Get(ctx, types.NamespacedName{Namespace: pod.Namespace, Name: rs.OwnerReferences[0].Name}, depl)
	if err != nil {
		return nil, errors.Wrap(err, "get operator deployment")
	}

	return depl, nil
}

func (r *ReconcilePerconaServerMongoDB) scheduleTelemetryRequests(ctx context.Context, cr *api.PerconaServerMongoDB, vs versionservice.Service) error {
	jn := jobName(telemetryPrefix, cr)

	log := logf.FromContext(ctx).WithValues("job", jn)

	scheduleRaw, ok := r.crons.ensureVersionJobs.Load(jn)
	if !versionservice.TelemetryEnabled() {
		if ok {
			r.deleteCronJob(jn)
		}
		return nil
	}

	schedule := jobSchedule{}
	if ok {
		schedule, _ = scheduleRaw.(jobSchedule)
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
			log.Error(err, "get CR")
			return
		}

		if localCr.Status.State != api.AppStateReady {
			log.Info("cluster is not ready")
			return
		}

		err = localCr.CheckNSetDefaults(ctx, r.serverVersion.Platform)
		if err != nil {
			log.Error(err, "set defaults for CR")
			return
		}

		operatorDepl, err := r.getOperatorDeployment(ctx)
		if err != nil {
			log.Error(err, "get operator deployment")
			return
		}

		_, err = r.getNewVersions(ctx, localCr, vs, operatorDepl)
		if err != nil {
			log.Error(err, "send telemetry")
		}
	})
	if err != nil {
		return err
	}

	log.Info("add new job", "name", jn, "schedule", sch)

	r.crons.ensureVersionJobs.Store(jn, jobSchedule{
		ID:           id,
		CronSchedule: sch,
	})

	operatorDepl, err := r.getOperatorDeployment(ctx)
	if err != nil {
		return errors.Wrap(err, "get operator deployment")
	}

	// send telemetry on startup
	_, err = r.getNewVersions(ctx, cr, vs, operatorDepl)
	if err != nil {
		log.Error(err, "send telemetry")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) ensureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs versionservice.Service) error {
	log := logf.FromContext(ctx)

	if !versionservice.UpgradeEnabled(cr) && !versionservice.TelemetryEnabled() {
		return nil
	}

	if cr.Status.State != api.AppStateReady && cr.Status.MongoVersion != "" {
		return errors.New("cluster is not ready")
	}

	operatorDepl, err := r.getOperatorDeployment(ctx)
	if err != nil {
		return errors.Wrap(err, "get operator deployment")
	}

	vm, err := r.buildVersionMeta(ctx, cr, operatorDepl)
	if err != nil {
		return errors.Wrap(err, "get version meta")
	}

	if !versionservice.UpgradeEnabled(cr) {
		return nil
	}

	newVersion, err := vs.GetExactVersion(cr, cr.Spec.UpgradeOptions.VersionServiceEndpoint, vm)
	if err != nil {
		return errors.Wrap(err, "check version")
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
		return errors.Wrap(err, "patch CR")
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

	session, err := r.mongoClientWithRole(ctx, cr, replset, api.RoleClusterAdmin)
	if err != nil {
		return errors.Wrap(err, "dial")
	}

	defer func() {
		err := session.Disconnect(ctx)
		if err != nil {
			log.Error(err, "close connection")
		}
	}()

	info, err := session.RSBuildInfo(ctx)
	if err != nil {
		return errors.Wrap(err, "get build info")
	}

	log.Info("update Mongo version fetched from db", "version", info.Version)
	cr.Status.MongoVersion = info.Version
	cr.Status.MongoImage = cr.Spec.Image

	// updating status resets our defaults, so we're passing a copy
	err = r.client.Status().Update(ctx, cr.DeepCopy())
	return errors.Wrap(err, "update CR status")
}
