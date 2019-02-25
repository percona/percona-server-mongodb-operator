package perconaservermongodb

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/api/errors"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/psmdb/backup"
)

func (r *ReconcilePerconaServerMongoDB) reconcileBackupCoordinator(cr *api.PerconaServerMongoDB) error {
	spec := &cr.Spec.Backup
	cSfs := backup.CoordinatorStatefulSet(&spec.Coordinator, cr.Name, cr.Namespace, r.serverVersion, spec.Debug)

	err := setControllerReference(cr, cSfs, r.scheme)
	if err != nil {
		return fmt.Errorf("set owner ref for coordinator StatefulSet: %v", err)
	}

	ctx := context.TODO()
	err = r.client.Create(ctx, cSfs)
	if err != nil && errors.IsAlreadyExists(err) {
		err := r.client.Update(ctx, cSfs)
		if err != nil {
			return fmt.Errorf("update coordinator StatefulSet: %v", err)
		}
	} else if err != nil {
		return fmt.Errorf("create coordinator StatefulSet: %v", err)
	}

	cService := backup.CoordinatorService(cr.Name, cr.Namespace)

	err = setControllerReference(cr, cService, r.scheme)
	if err != nil {
		return fmt.Errorf("set owner ref for coordinator Service: %v", err)
	}

	err = r.client.Create(ctx, cService)
	if err != nil && errors.IsAlreadyExists(err) {
		err := r.client.Update(ctx, cService)
		if err != nil {
			return fmt.Errorf("update coordinator Service: %v", err)
		}
	} else if err != nil {
		return fmt.Errorf("create coordinator Service: %v", err)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupTasks(cr *api.PerconaServerMongoDB) error {
	ctx := context.TODO()

	//TODO: Delete tasks

	for _, task := range cr.Spec.Backup.Tasks {
		if task.Enabled {
			cjob := backup.BackupCronJob(&task, cr.Name, cr.Namespace, cr.Spec.Backup.TaskImage, r.serverVersion)

			err := r.client.Create(ctx, cjob)
			if err != nil && errors.IsAlreadyExists(err) {
				err := r.client.Update(ctx, cjob)
				if err != nil {
					return fmt.Errorf("update task %s: %v", task.Name, err)
				}
			} else if err != nil {
				return fmt.Errorf("create task %s: %v", task.Name, err)
			}
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupStorageConfig(cr *api.PerconaServerMongoDB) error {
	secr, err := backup.AgentStoragesConfigSecret(cr, r.client)
	if err != nil {
		return fmt.Errorf("get storage config: %v", err)
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
