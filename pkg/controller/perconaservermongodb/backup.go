package perconaservermongodb

import (
	"container/heap"
	"context"
	"fmt"

	batchv1b "k8s.io/api/batch/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

func (r *ReconcilePerconaServerMongoDB) reconcileBackupTasks(cr *api.PerconaServerMongoDB) error {
	ctasks := make(map[string]api.BackupTaskSpec)
	ls := backup.NewBackupCronJobLabels(cr.Name)

	for _, task := range cr.Spec.Backup.Tasks {
		cjob := backup.BackupCronJob(&task, cr.Name, cr.Namespace, cr.Spec.Backup, cr.Spec.ImagePullSecrets)
		ls = cjob.ObjectMeta.Labels
		if task.Enabled {
			ctasks[cjob.Name] = task

			err := setControllerReference(cr, cjob, r.scheme)
			if err != nil {
				return fmt.Errorf("set owner reference for backup task %s: %v", cjob.Name, err)
			}

			err = r.client.Create(context.TODO(), cjob)
			if err != nil && errors.IsAlreadyExists(err) {
				err := r.client.Update(context.TODO(), cjob)
				if err != nil {
					return fmt.Errorf("update task %s: %v", task.Name, err)
				}
			} else if err != nil {
				return fmt.Errorf("create task %s: %v", task.Name, err)
			}
		}
	}

	// Remove old/unused tasks
	tasksList := &batchv1b.CronJobList{}
	err := r.client.List(context.TODO(),
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
				oldjobs, err := r.oldScheduledBackups(cr, t.Name, spec.Keep)
				if err != nil {
					return fmt.Errorf("remove old backups: %v", err)
				}

				for _, todel := range oldjobs {
					_ = r.client.Delete(context.TODO(), &todel)
				}
			}
		} else {
			err := r.client.Delete(context.TODO(), &t)
			if err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("delete backup task %s: %v", t.Name, err)
			}
		}
	}

	return nil
}

// oldScheduledBackups returns list of the most old psmdb-bakups that execeed `keep` limit
func (r *ReconcilePerconaServerMongoDB) oldScheduledBackups(cr *api.PerconaServerMongoDB,
	ancestor string, keep int) ([]api.PerconaServerMongoDBBackup, error) {
	bcpList := api.PerconaServerMongoDBBackupList{}
	err := r.client.List(context.TODO(),
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
