package perconaservermongodb

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

func (r *ReconcilePerconaServerMongoDB) reconcileBackupCoordinator(cr *api.PerconaServerMongoDB) (*appsv1.StatefulSet, error) {
	sfs, err := r.reconcileBackupSfs(cr)
	if err != nil {
		return nil, err
	}

	return sfs, r.reconcileBackupService(cr)
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupSfs(cr *api.PerconaServerMongoDB) (*appsv1.StatefulSet, error) {
	cSfs := backup.CoordinatorStatefulSet(cr)
	if !cr.Spec.Backup.Enabled {
		err := r.client.Delete(context.TODO(), cSfs)
		if err != nil && !errors.IsNotFound(err) {
			return cSfs, fmt.Errorf("delete Statefulset: %v", err)
		}
		return cSfs, nil
	}

	cSfs.Spec = backup.CoordinatorStatefulSetSpec(cr, &cr.Spec.Backup.Coordinator, r.serverVersion, cr.Spec.Backup.Debug)

	err := setControllerReference(cr, cSfs, r.scheme)
	if err != nil {
		return nil, fmt.Errorf("set owner ref for coordinator StatefulSet: %v", err)
	}
	err = r.createOrUpdate(cSfs, cSfs.Name, cSfs.Namespace)
	if err != nil {
		return nil, fmt.Errorf("statefulset: %v", err)
	}

	return cSfs, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupService(cr *api.PerconaServerMongoDB) error {
	cService := backup.CoordinatorService(cr.Name, cr.Namespace)
	if !cr.Spec.Backup.Enabled {
		err := r.client.Delete(context.TODO(), cService)
		if err != nil && !errors.IsNotFound(err) {
			return fmt.Errorf("delete Service: %v", err)
		}

		return nil
	}

	err := setControllerReference(cr, cService, r.scheme)
	if err != nil {
		return fmt.Errorf("set owner ref for coordinator Service: %v", err)
	}
	err = r.client.Create(context.TODO(), cService)
	if err != nil && !errors.IsAlreadyExists(err) {
		return fmt.Errorf("create coordinator Service: %v", err)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupTasks(cr *api.PerconaServerMongoDB, owner runtime.Object) error {
	ctasks := make(map[string]struct{})
	ls := make(map[string]string)
	for _, task := range cr.Spec.Backup.Tasks {
		cjob := backup.BackupCronJob(&task, cr.Name, cr.Namespace, cr.Spec.Backup.Image, cr.Spec.ImagePullSecrets, r.serverVersion)
		ls = cjob.ObjectMeta.Labels
		if task.Enabled {
			ctasks[cjob.Name] = struct{}{}

			err := setControllerReference(owner, cjob, r.scheme)
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
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(ls),
		},
		tasksList,
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

func (r *ReconcilePerconaServerMongoDB) reconcileBackupStorageConfig(cr *api.PerconaServerMongoDB, owner runtime.Object) error {
	secr, err := backup.AgentStoragesConfigSecret(cr, r.client)
	if err != nil {
		return fmt.Errorf("get storage config: %v", err)
	}
	err = setControllerReference(owner, secr, r.scheme)
	if err != nil {
		return fmt.Errorf("set owner reference for AgentStoragesConfigSecret: %v", err)
	}

	ctx := context.TODO()
	err = r.client.Create(ctx, secr)
	if err != nil && errors.IsAlreadyExists(err) {
		err := r.client.Update(ctx, secr)
		if err != nil {
			return fmt.Errorf("update storage config: %v", err)
		}
	} else if err != nil {
		return fmt.Errorf("create storage config: %v", err)
	}

	return nil
}
