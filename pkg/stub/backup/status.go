package backup

import (
	"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

// updateStatus updates the backup BackupStatus struct in the PSMDB status
func (c *Controller) updateStatus(backupTask *v1alpha1.BackupTaskSpec) error {
	err := c.client.Get(c.psmdb)
	if err != nil {
		return err
	}
	data := c.psmdb.DeepCopy()

	cronJob := newCronJob(c.psmdb, backupTask)
	status := &v1alpha1.BackupTaskStatus{
		Enabled:     backupTask.Enabled,
		Name:        backupTask.Name,
		StorageName: backupTask.StorageName,
		CronJob:     cronJob.Name,
	}

	for i, bkpStatus := range data.Status.Backups {
		if bkpStatus.Name == backupTask.Name {
			data.Status.Backups[i] = status
			err := c.client.Update(data)
			if err != nil {
				return err
			}
			c.psmdb.Status = data.Status
			return nil
		}
	}

	data.Status.Backups = append(data.Status.Backups, status)
	return c.client.Update(data)
}

// deleteStatus deletes the backup BackupStatus struct from the PSMDB status
func (c *Controller) deleteStatus(backup *v1alpha1.BackupTaskSpec) error {
	err := c.client.Get(c.psmdb)
	if err != nil {
		return err
	}
	data := c.psmdb.DeepCopy()

	for i, bkpStatus := range data.Status.Backups {
		if bkpStatus.Name != backup.Name {
			continue
		}
		data.Status.Backups = append(data.Status.Backups[:i], data.Status.Backups[i+1:]...)
		break
	}

	if !reflect.DeepEqual(data, c.psmdb) {
		return c.client.Update(data)
	}
	return nil
}
