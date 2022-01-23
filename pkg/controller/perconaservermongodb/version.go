package perconaservermongodb

import (
	"context"
	"fmt"
	v "github.com/hashicorp/go-version"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"strings"
	"sync/atomic"
)

func (r *ReconcilePerconaServerMongoDB) deleteEnsureVersion(cr *api.PerconaServerMongoDB, id int) {
	r.crons.crons.Remove(cron.EntryID(id))
	delete(r.crons.jobs, jobName(cr))
}

func (r *ReconcilePerconaServerMongoDB) sheduleEnsureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService) error {
	schedule, ok := r.crons.jobs[jobName(cr)]
	if cr.Spec.UpdateStrategy != v1.SmartUpdateStatefulSetStrategyType ||
		cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyNever ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyDiasbled {
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
	if len(cr.Spec.UpgradeOptions.Apply) == 0 ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyLatest ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyRecommended {
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

func (r *ReconcilePerconaServerMongoDB) ensureVersion(ctx context.Context, cr *api.PerconaServerMongoDB, vs VersionService) error {
	if cr.Spec.UpdateStrategy != v1.SmartUpdateStatefulSetStrategyType ||
		cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyNever ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyDiasbled {
		return nil
	}

	if cr.Status.State != v1.AppStateReady && cr.Status.MongoVersion != "" {
		return errors.New("cluster is not ready")
	}

	fcv := ""
	if cr.Status.MongoVersion != "" {
		f, err := r.getFCV(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "failed to get FCV")
		}

		fcv = f
	}

	req, err := majorUpgradeRequested(cr, fcv)
	if err != nil {
		return errors.Wrap(err, "failed to check if major update requested")
	}

	vm := VersionMeta{
		Apply:         string(cr.Spec.UpgradeOptions.Apply),
		KubeVersion:   r.serverVersion.Info.GitVersion,
		MongoVersion:  cr.Status.MongoVersion,
		PMMVersion:    cr.Status.PMMVersion,
		BackupVersion: cr.Status.BackupVersion,
		CRUID:         string(cr.GetUID()),
		Version:       cr.Version().String(),
	}
	if cr.Spec.Platform != nil {
		vm.Platform = string(*cr.Spec.Platform)
	}

	if req.Ok {
		if len(req.Apply) != 0 {
			vm.Apply = req.Apply
			vm.MongoVersion = req.NewVersion
		} else {
			vm.Apply = req.NewVersion
		}
	}

	patch := client.MergeFrom(cr.DeepCopy())
	newVersion, err := vs.GetExactVersion(cr.Spec.UpgradeOptions.VersionServiceEndpoint, vm)
	if err != nil {
		return errors.Wrap(err, "failed to check version")
	}

	if cr.Spec.Image != newVersion.MongoImage {
		if cr.Status.MongoVersion == "" {
			log.Info(fmt.Sprintf("set Mongo version to %s", newVersion.MongoVersion))
		} else {
			log.Info(fmt.Sprintf("update Mongo version from %s to %s", cr.Status.MongoVersion, newVersion.MongoVersion))
		}
		cr.Spec.Image = newVersion.MongoImage
	}

	if cr.Spec.Backup.Image != newVersion.BackupImage {
		if cr.Status.BackupVersion == "" {
			log.Info(fmt.Sprintf("set Backup version to %s", newVersion.BackupVersion))
		} else {
			log.Info(fmt.Sprintf("update Backup version from %s to %s", cr.Status.BackupVersion, newVersion.BackupVersion))
		}
		cr.Spec.Backup.Image = newVersion.BackupImage
	}

	if cr.Spec.PMM.Image != newVersion.PMMImage {
		if cr.Status.PMMVersion == "" {
			log.Info(fmt.Sprintf("set PMM version to %s", newVersion.PMMVersion))
		} else {
			log.Info(fmt.Sprintf("update PMM version from %s to %s", cr.Status.PMMVersion, newVersion.PMMVersion))
		}
		cr.Spec.PMM.Image = newVersion.PMMImage
	}

	err = r.client.Patch(ctx, cr, patch)
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
		err := session.Disconnect(context.TODO())
		if err != nil {
			log.Error(err, "failed to close connection")
		}
	}()

	info, err := mongo.RSBuildInfo(context.Background(), session)
	if err != nil {
		return errors.Wrap(err, "get build info")
	}

	log.Info(fmt.Sprintf("update Mongo version to %v (fetched from db)", info.Version))
	cr.Status.MongoVersion = info.Version
	cr.Status.MongoImage = cr.Spec.Image

	// updating status resets our defaults, so we're passing a copy
	err = r.client.Status().Update(context.Background(), cr.DeepCopy())
	return errors.Wrapf(err, "failed to update CR")
}
