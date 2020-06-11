package perconaservermongodb

import (
	"context"
	"fmt"
	"math/rand"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	v1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/robfig/cron/v3"
	"k8s.io/apimachinery/pkg/types"
)

const jobName = "ensure-version"
const never = "Never"
const disabled = "Disabled"

func (r *ReconcilePerconaServerMongoDB) deleteEnsureVersion(id int) {
	r.crons.crons.Remove(cron.EntryID(id))
	delete(r.crons.jobs, jobName)
}

func (r *ReconcilePerconaServerMongoDB) sheduleEnsureVersion(cr *api.PerconaServerMongoDB, vs VersionService) error {
	if cr.Spec.UpdateStrategy != api.SmartUpdateStatefulSetStrategyType ||
		cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply == never ||
		cr.Spec.UpgradeOptions.Apply == disabled {
		return nil
	}

	shedule, ok := r.crons.jobs[jobName]
	if ok && (cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply == never ||
		cr.Spec.UpgradeOptions.Apply == disabled) {
		r.deleteEnsureVersion(shedule.ID)
		return nil
	}
	if ok && shedule.CronShedule == cr.Spec.UpgradeOptions.Schedule {
		return nil
	}
	if ok {
		log.Info(fmt.Sprintf("remove job %s because of new %s", shedule.CronShedule, cr.Spec.UpgradeOptions.Schedule))
		r.deleteEnsureVersion(shedule.ID)
	}

	log.Info(fmt.Sprintf("add new job: %s", cr.Spec.UpgradeOptions.Schedule))
	id, err := r.crons.crons.AddFunc(cr.Spec.UpgradeOptions.Schedule, func() {
		localCr := &api.PerconaServerMongoDB{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
		if err != nil {
			log.Error(err, "failed to get CR")
			return
		}

		if localCr.Status.MongoStatus != v1.AppStateReady {
			log.Info("cluster is not ready")
			return
		}

		r.ensureVersion(localCr, vs)
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

func (r *ReconcilePerconaServerMongoDB) ensureVersion(cr *api.PerconaServerMongoDB, vs VersionService) {
	if cr.Spec.UpdateStrategy != v1.SmartUpdateStatefulSetStrategyType ||
		cr.Spec.UpgradeOptions.Schedule == "" ||
		cr.Spec.UpgradeOptions.Apply == never ||
		cr.Spec.UpgradeOptions.Apply == disabled {
		return
	}

	if cr.Status.MongoStatus != v1.AppStateReady && cr.Status.MongoVersion != "" {
		log.Info("cluster is not ready")
		return
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
		log.Error(err, "failed to update CR")
		return
	}
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
