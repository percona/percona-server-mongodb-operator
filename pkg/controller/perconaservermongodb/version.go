package perconaservermongodb

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

const jobName = "ensure-version"

func (r *ReconcilePerconaServerMongoDB) deleteEnsureVersion(id int) {
	r.crons.crons.Remove(cron.EntryID(id))
	delete(r.crons.jobs, jobName)
}

func (r *ReconcilePerconaServerMongoDB) sheduleEnsureVersion(cr *api.PerconaServerMongoDB, vs VersionService) error {
	schedule, ok := r.crons.jobs[jobName]
	if cr.Spec.UpdateStrategy != v1.SmartUpdateStatefulSetStrategyType ||
		cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyNever ||
		cr.Spec.UpgradeOptions.Apply.Lower() == api.UpgradeStrategyDiasbled {
		if ok {
			r.deleteEnsureVersion(schedule.ID)
		}

		return nil
	}

	if ok && schedule.CronShedule == cr.Spec.UpgradeOptions.Schedule {
		return nil
	}

	if ok {
		log.Info(fmt.Sprintf("remove job %s because of new %s", schedule.CronShedule, cr.Spec.UpgradeOptions.Schedule))
		r.deleteEnsureVersion(schedule.ID)
	}

	log.Info(fmt.Sprintf("add new job: %s", cr.Spec.UpgradeOptions.Schedule))
	id, err := r.crons.crons.AddFunc(cr.Spec.UpgradeOptions.Schedule, func() {
		r.statusMutex.Lock()
		defer r.statusMutex.Unlock()

		if !atomic.CompareAndSwapInt32(&r.updateSync, updateDone, updateWait) {
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

	r.crons.jobs[jobName] = Shedule{
		ID:          int(id),
		CronShedule: cr.Spec.UpgradeOptions.Schedule,
	}

	return nil
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

	vm := VersionMeta{
		Apply:         cr.Spec.UpgradeOptions.Apply,
		KubeVersion:   r.serverVersion.Info.GitVersion,
		MongoVersion:  cr.Status.MongoVersion,
		PMMVersion:    cr.Status.PMMVersion,
		BackupVersion: cr.Status.BackupVersion,
		CRUID:         string(cr.GetUID()),
	}
	if cr.Spec.Platform != nil {
		vm.Platform = string(*cr.Spec.Platform)
	}

	newVersion, err := vs.GetExactVersion(vm)
	if err != nil {
		return fmt.Errorf("failed to check version: %v", err)
	}

	if cr.Spec.Image != newVersion.MongoImage {
		log.Info(fmt.Sprintf("update Mongo version from %s to %s", cr.Status.MongoVersion, newVersion.MongoVersion))
		cr.Spec.Image = newVersion.MongoImage
	}

	if cr.Spec.Backup.Image != newVersion.BackupImage {
		log.Info(fmt.Sprintf("update Backup version from %s to %s", cr.Status.BackupVersion, newVersion.BackupVersion))
		cr.Spec.Backup.Image = newVersion.BackupImage
	}

	if cr.Spec.PMM.Image != newVersion.PMMImage {
		log.Info(fmt.Sprintf("update PMM version from %s to %s", cr.Status.PMMVersion, newVersion.PMMVersion))
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

func (r *ReconcilePerconaServerMongoDB) fetchVersionFromMongo(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, pods corev1.PodList, usersSecret *corev1.Secret) error {
	if cr.Status.ObservedGeneration != cr.ObjectMeta.Generation ||
		cr.Status.State != api.AppStateReady ||
		cr.Status.MongoImage == cr.Spec.Image {
		return nil
	}

	rsAddrs, err := psmdb.GetReplsetAddrs(r.client, cr, replset, pods.Items)
	if err != nil {
		return errors.Wrap(err, "get replset addr")
	}

	session, err := mongo.Dial(rsAddrs, replset.Name, string(usersSecret.Data[envMongoDBClusterAdminUser]), string(usersSecret.Data[envMongoDBClusterAdminPassword]), false)
	if err != nil {
		return errors.Wrap(err, "dial")
	}

	defer session.Disconnect(context.TODO())

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
