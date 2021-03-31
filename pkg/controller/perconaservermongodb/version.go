package perconaservermongodb

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"golang.org/x/mod/semver"
	"k8s.io/apimachinery/pkg/types"
)

func (r *ReconcilePerconaServerMongoDB) deleteEnsureVersion(cr *api.PerconaServerMongoDB, id int) {
	r.crons.crons.Remove(cron.EntryID(id))
	delete(r.crons.jobs, jobName(cr))
}

func (r *ReconcilePerconaServerMongoDB) sheduleEnsureVersion(cr *api.PerconaServerMongoDB, vs VersionService) error {
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
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
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

		err = r.ensureVersion(localCr, vs)
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

func isUpdateValid(current, desired string) bool {
	switch current {
	case "v3.6":
		return desired == "v4.0"
	case "v4.0":
		return desired == "v4.2"
	case "v4.2":
		return desired == "v4.4"
	default:
		return false
	}
}

func canUpgradeVersion(current, new string) (bool, error) {
	cursv, err := toGoSemver(current)
	if err != nil {
		return false, errors.Wrap(err, "failed to get current semver")
	}

	newsv, err := toGoSemver(new)
	if err != nil {
		return false, errors.Wrap(err, "failed to get new semver")
	}

	currentMM := semver.MajorMinor(cursv)
	newMM := semver.MajorMinor(newsv)

	cmp := semver.Compare(currentMM, newMM)

	if cmp == -1 && isUpdateValid(currentMM, newMM) {
		return true, nil
	} else if cmp == 0 {
		return false, nil
	}

	return false, errors.Errorf("invalid upgrade: from %s to %s", current, new)
}

func toGoSemver(v string) (string, error) {
	// v prefix needed to make it valid semver for "golang.org/x/mod/semver"
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}

	if !semver.IsValid(v) {
		return "", errors.Errorf("invalid version: %s", v)
	}

	return v, nil
}

type UpgradeRequest struct {
	Ok         bool
	Apply      string
	NewVersion string
}

func majorUpgradeRequested(cr *api.PerconaServerMongoDB) (UpgradeRequest, error) {
	if len(cr.Spec.UpgradeOptions.Apply) == 0 ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyLatest ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyRecommended {
		return UpgradeRequest{false, "", ""}, nil
	}

	apply := ""
	applySp := strings.Split(string(cr.Spec.UpgradeOptions.Apply), "-")
	if len(applySp) > 1 {
		apply = applySp[1]
	}

	new, err := toGoSemver(applySp[0])
	if err != nil {
		return UpgradeRequest{false, "", ""}, errors.Wrap(err, "faied to make semver")
	}

	if len(cr.Status.MongoVersion) == 0 {
		return UpgradeRequest{true, apply, new[1:]}, nil
	}

	can, err := canUpgradeVersion(cr.Status.MongoVersion, new)
	if err != nil {
		return UpgradeRequest{false, "", ""}, errors.Wrap(err, "can't upgrade")
	}

	if can {
		return UpgradeRequest{true, apply, semver.MajorMinor(new)[1:]}, nil
	}

	return UpgradeRequest{false, "", ""}, nil
}

func (r *ReconcilePerconaServerMongoDB) ensureVersion(cr *api.PerconaServerMongoDB, vs VersionService) error {
	if cr.Spec.UpdateStrategy != v1.SmartUpdateStatefulSetStrategyType ||
		cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyNever ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyDiasbled {
		return nil
	}

	if cr.Status.State != v1.AppStateReady && cr.Status.MongoVersion != "" {
		return errors.New("cluster is not ready")
	}

	req, err := majorUpgradeRequested(cr)
	if err != nil {
		return errors.Wrap(err, "failed to check id major update requested")
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

	err = r.client.Update(context.Background(), cr)
	if err != nil {
		return fmt.Errorf("failed to update CR: %v", err)
	}

	time.Sleep(1 * time.Second) // based on experiments operator just need it.

	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, cr)
	if err != nil {
		return fmt.Errorf("failed to get CR: %v", err)
	}

	err = cr.CheckNSetDefaults(r.serverVersion.Platform, log)
	if err != nil {
		return fmt.Errorf("failed to set defaults for CR: %v", err)
	}

	cr.Status.PMMVersion = newVersion.PMMVersion
	cr.Status.BackupVersion = newVersion.BackupVersion
	cr.Status.MongoVersion = newVersion.MongoVersion
	cr.Status.MongoImage = newVersion.MongoImage

	err = r.client.Status().Update(context.Background(), cr)
	if err != nil {
		return fmt.Errorf("failed to update CR status: %v", err)
	}

	time.Sleep(1 * time.Second)

	return nil
}

func (r *ReconcilePerconaServerMongoDB) fetchVersionFromMongo(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {

	if cr.Status.ObservedGeneration != cr.ObjectMeta.Generation ||
		cr.Status.State != api.AppStateReady ||
		cr.Status.MongoImage == cr.Spec.Image {
		return nil
	}

	session, err := r.mongoClientWithRole(cr, *replset, roleClusterAdmin)
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

	err = r.client.Status().Update(context.Background(), cr)
	return errors.Wrapf(err, "failed to update CR")
}
