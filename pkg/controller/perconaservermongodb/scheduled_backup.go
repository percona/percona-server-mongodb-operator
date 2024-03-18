package perconaservermongodb

import (
	"container/heap"
	"context"
	"fmt"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	batchv1 "k8s.io/api/batch/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

type BackupScheduleJob struct {
	api.BackupTaskSpec
	JobID cron.EntryID
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupTasks(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	ctasks := make(map[string]api.BackupTaskSpec)

	for _, task := range cr.Spec.Backup.Tasks {
		if !task.Enabled {
			continue
		}
		_, ok := cr.Spec.Backup.Storages[task.StorageName]
		if !ok {
			return errors.Errorf("there is no storage %s in cluster %s for %s task", task.StorageName, cr.Name, task.Name)
		}
		ctasks[task.Name] = task

		if err := r.createOrUpdateBackupTask(ctx, cr, task); err != nil {
			return err
		}
	}

	// Remove old/unused tasks
	if err := r.deleteOldBackupTasks(ctx, cr, ctasks); err != nil {
		return err
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateBackupTask(ctx context.Context, cr *api.PerconaServerMongoDB, task api.BackupTaskSpec) error {
	if cr.CompareVersion("1.13.0") < 0 {
		cjob, err := backup.BackupCronJob(cr, &task)
		if err != nil {
			return errors.Wrap(err, "can't create job")
		}
		err = setControllerReference(cr, &cjob, r.scheme)
		if err != nil {
			return errors.Wrapf(err, "set owner reference for backup task %s", cjob.Name)
		}

		err = r.createOrUpdate(ctx, &cjob)
		if err != nil {
			return errors.Wrap(err, "create or update backup job")
		}
		return nil
	}
	t := BackupScheduleJob{}
	bj, ok := r.crons.backupJobs.Load(task.JobName(cr))
	if ok {
		t = bj.(BackupScheduleJob)
	}

	if !ok || t.Schedule != task.Schedule || t.StorageName != task.StorageName {
		logf.FromContext(ctx).Info("Creating or updating backup job", "name", task.Name, "namespace", cr.Namespace, "schedule", task.Schedule)

		r.deleteBackupTask(cr, t.BackupTaskSpec)
		jobID, err := r.crons.crons.AddFunc(task.Schedule, r.createBackupTask(ctx, cr, task))
		if err != nil {
			return errors.Wrapf(err, "can't parse cronjob schedule for backup %s with schedule %s", task.Name, task.Schedule)
		}

		r.crons.backupJobs.Store(task.JobName(cr), BackupScheduleJob{
			BackupTaskSpec: task,
			JobID:          jobID,
		})
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteOldBackupTasks(ctx context.Context, cr *api.PerconaServerMongoDB, ctasks map[string]api.BackupTaskSpec) error {
	log := logf.FromContext(ctx)

	if cr.CompareVersion("1.13.0") < 0 {
		ls := backup.NewBackupCronJobLabels(cr.Name, cr.Spec.Backup.Labels)
		tasksList := &batchv1.CronJobList{}
		err := r.client.List(ctx,
			tasksList,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(ls),
			},
		)
		if err != nil {
			return fmt.Errorf("get backup list: %v", err)
		}

		for _, t := range tasksList.Items {
			if spec, ok := ctasks[t.Name]; ok {
				if spec.Keep > 0 {
					oldjobs, err := r.oldScheduledBackups(ctx, cr, t.Name, spec.Keep)
					if err != nil {
						return fmt.Errorf("remove old backups: %v", err)
					}

					for _, todel := range oldjobs {
						err = r.client.Delete(ctx, &todel)
						if err != nil {
							return fmt.Errorf("failed to delete backup object: %v", err)
						}
					}
				}
			} else {
				err := r.client.Delete(ctx, &t)
				if err != nil && !k8sErrors.IsNotFound(err) {
					return fmt.Errorf("delete backup task %s: %v", t.Name, err)
				}
			}
		}
		return nil
	}
	r.crons.backupJobs.Range(func(k, v interface{}) bool {
		item := v.(BackupScheduleJob)
		if spec, ok := ctasks[item.Name]; ok {
			if spec.Keep > 0 {
				oldjobs, err := r.oldScheduledBackups(ctx, cr, item.Name, spec.Keep)
				if err != nil {
					log.Error(err, "failed to list old backups", "job", item.Name)
					return true
				}

				for _, todel := range oldjobs {
					err = r.client.Delete(ctx, &todel)
					if err != nil {
						log.Error(err, "failed to delete backup object")
						return true
					}
				}

			}
		} else {
			log.Info("deleting outdated backup job", "job", item.Name)
			r.deleteBackupTask(cr, item.BackupTaskSpec)
		}

		return true
	})
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createBackupTask(ctx context.Context, cr *api.PerconaServerMongoDB, task api.BackupTaskSpec) func() {
	log := logf.FromContext(ctx)

	return func() {
		localCr := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
		if k8sErrors.IsNotFound(err) {
			log.Info("cluster is not found, deleting the job", "job", task.Name)
			r.deleteBackupTask(cr, task)
			return
		}
		bcp, err := backup.BackupFromTask(cr, &task)
		if err != nil {
			log.Error(err, "failed to create backup")
			return
		}
		bcp.Namespace = cr.Namespace
		err = r.client.Create(ctx, bcp)
		if err != nil {
			log.Error(err, "failed to create backup")
		}
	}
}

func (r *ReconcilePerconaServerMongoDB) deleteBackupTask(cr *api.PerconaServerMongoDB, task api.BackupTaskSpec) {
	job, ok := r.crons.backupJobs.LoadAndDelete(task.JobName(cr))
	if !ok {
		return
	}
	r.crons.crons.Remove(job.(BackupScheduleJob).JobID)
}

// oldScheduledBackups returns list of the most old psmdb-bakups that execeed `keep` limit
func (r *ReconcilePerconaServerMongoDB) oldScheduledBackups(ctx context.Context, cr *api.PerconaServerMongoDB,
	ancestor string, keep int) ([]api.PerconaServerMongoDBBackup, error) {
	bcpList := api.PerconaServerMongoDBBackupList{}
	err := r.client.List(ctx,
		&bcpList,
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"cluster":  cr.Name,
				"ancestor": ancestor,
			}),
		},
	)
	if err != nil {
		return []api.PerconaServerMongoDBBackup{}, err
	}

	if len(bcpList.Items) <= keep {
		return []api.PerconaServerMongoDBBackup{}, nil
	}

	h := &minHeap{}
	heap.Init(h)
	for _, bcp := range bcpList.Items {
		if bcp.Status.State == api.BackupStateReady {
			heap.Push(h, bcp)
		}
	}

	if h.Len() <= keep {
		return []api.PerconaServerMongoDBBackup{}, nil
	}

	ret := make([]api.PerconaServerMongoDBBackup, 0, h.Len()-keep)
	for i := h.Len() - keep; i > 0; i-- {
		o := heap.Pop(h).(api.PerconaServerMongoDBBackup)
		ret = append(ret, o)
	}

	return ret, nil
}

type minHeap []api.PerconaServerMongoDBBackup

func (h minHeap) Len() int { return len(h) }

func (h minHeap) Less(i, j int) bool {
	return h[i].CreationTimestamp.Before(&h[j].CreationTimestamp)
}

func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(x interface{}) {
	*h = append(*h, x.(api.PerconaServerMongoDBBackup))
}

func (h *minHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}
