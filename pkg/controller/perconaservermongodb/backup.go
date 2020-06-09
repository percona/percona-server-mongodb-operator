package perconaservermongodb

import (
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
	ctasks := make(map[string]struct{})
	ls := backup.NewBackupCronJobLabels(cr.Name)

	for _, task := range cr.Spec.Backup.Tasks {
		cjob := backup.BackupCronJob(&task, cr.Name, cr.Namespace, cr.Spec.Backup, cr.Spec.ImagePullSecrets)
		ls = cjob.ObjectMeta.Labels
		if task.Enabled {
			ctasks[cjob.Name] = struct{}{}

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
		if _, ok := ctasks[t.Name]; !ok {
			err := r.client.Delete(context.TODO(), &t)
			if err != nil && !errors.IsNotFound(err) {
				return fmt.Errorf("delete backup task %s: %v", t.Name, err)
			}
		}
	}

	return nil
}
