package perconaservermongodb

import (
	"context"
	"fmt"
	"math/rand"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/types"
)

const jobName = "ensure-version"

func (r *ReconcilePerconaServerMongoDB) deleteEnsureVersion(id int) {
	r.crons.crons.Remove(cron.EntryID(id))
	delete(r.crons.jobs, jobName)
}

func (r *ReconcilePerconaServerMongoDB) sheduleEnsureVersion(cr *api.PerconaServerMongoDB, vs VersionService) error {
	schedule, ok := r.crons.jobs[jobName]

	if cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType ||
		cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply == api.UpgradeStrategyNever ||
		cr.Spec.UpgradeOptions.Apply == api.UpgradeStrategyDiasbled {
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
		localCr := &api.PerconaServerMongoDB{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
		if err != nil {
			log.Error(err, "failed to get CR")
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
		cr.Spec.UpgradeOptions.Apply == api.UpgradeStrategyNever ||
		cr.Spec.UpgradeOptions.Apply == api.UpgradeStrategyDiasbled {
		return nil
	}

	if cr.Status.State != v1.AppStateReady && cr.Status.MongoVersion != "" {
		return errors.New("cluster is not ready")
	}

	new := vs.CheckNew()

	if cr.Status.MongoVersion != new.MongoVersion {
		log.Info(fmt.Sprintf("update Mongo version to %v", new.MongoVersion))
		cr.Spec.Image = new.Image
		cr.Status.MongoVersion = new.MongoVersion
	}
	if cr.Status.BackupVersion != new.BackupVersion {
		log.Info(fmt.Sprintf("update Backup version to %v", new.BackupVersion))
		cr.Spec.Backup.Image = new.BackupImage
		cr.Status.BackupVersion = new.BackupVersion
	}
	if cr.Status.PMMVersion != new.PMMVersion {
		log.Info(fmt.Sprintf("update PMM version to %v", new.PMMVersion))
		cr.Spec.PMM.Image = new.PMMImage
		cr.Status.PMMVersion = new.PMMVersion
	}

	err := r.client.Update(context.Background(), cr)
	if err != nil {
		return fmt.Errorf("failed to update CR: %v", err)
	}

	return nil
}

type VersionService interface {
	CheckNew() VersionResponse
}

type VersionServiceMock struct {
}

type VersionResponse struct {
	Image         string `json:"pxcImage,omitempty"`
	MongoVersion  string `json:"pxcVersion,omitempty"`
	BackupImage   string `json:"backupImage,omitempty"`
	BackupVersion string `json:"backupVersion,omitempty"`
	PMMImage      string `json:"pmmImage,omitempty"`
	PMMVersion    string `json:"pmmVersion,omitempty"`
}

func (vs VersionServiceMock) CheckNew() VersionResponse {
	vr := VersionResponse{
		Image:        "perconalab/percona-server-mongodb-operator:master-mongod4.2",
		MongoVersion: "4.2",
		BackupImage:  "percona/percona-server-mongodb-operator:1.4.0-backup",
		PMMImage:     "perconalab/percona-server-mongodb-operator:master-pmm",
	}

	if rand.Int()%2 == 0 {
		vr.Image = "perconalab/percona-server-mongodb-operator:master-mongod4.0"
		vr.MongoVersion = "4.0"
	}

	return vr
}
