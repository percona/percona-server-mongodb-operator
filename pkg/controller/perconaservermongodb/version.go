package perconaservermongodb

import (
	"context"
	"fmt"
	"math/rand"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/robfig/cron"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
)

const jobName = "ensure-version"

func (r *ReconcilePerconaServerMongoDB) deleteEnsureVersion(id int) {
	r.crons.crons.Remove(cron.EntryID(id))
	delete(r.crons.jobs, jobName)
}

func (r *ReconcilePerconaServerMongoDB) ensureVersion(cr *api.PerconaServerMongoDB, vs VersionService, sfs *appsv1.StatefulSet) error {
	shedule, ok := r.crons.jobs[jobName]

	if ok && cr.Spec.UpgradeOptions.Schedule == "" {
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
		sfsLocal := appsv1.StatefulSet{}
		err := r.client.Get(context.TODO(), types.NamespacedName{Name: sfs.Name, Namespace: sfs.Namespace}, &sfsLocal)
		if err != nil {
			log.Error(err, "failed to get stateful set")
			return
		}

		if sfsLocal.Status.ReadyReplicas < sfsLocal.Status.Replicas ||
			sfsLocal.Status.CurrentRevision != sfsLocal.Status.UpdateRevision {
			log.Info("cluster is not consistent")
			return
		}

		localCR := &api.PerconaServerMongoDB{}
		err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCR)
		if err != nil {
			log.Error(err, "failed to get CR")
			return
		}

		new := vs.CheckNew()
		if localCR.Spec.Image != new {
			log.Info(fmt.Sprintf("update version to %s", new))
			localCR.Spec.Image = new
			err = r.client.Update(context.Background(), localCR)
			if err != nil {
				log.Error(err, "failed to update CR")
				return
			}
		} else {
			log.Info(fmt.Sprintf("same version %s", new))
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

type VersionService interface {
	CheckNew() string
}

type VersionServiceMock struct {
}

func (vs VersionServiceMock) CheckNew() string {
	if rand.Int()%2 == 0 {
		return "perconalab/percona-server-mongodb-operator:master-mongod4.2"
	}
	return "percona/percona-server-mongodb-operator:1.4.0-mongod4.2"
}
