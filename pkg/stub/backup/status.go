package backup

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

func (c *Controller) getPSMDBCopy() (*v1alpha1.PerconaServerMongoDB, error) {
	err := c.client.Get(c.psmdb)
	if err != nil {
		return nil, err
	}
	return c.psmdb.DeepCopy(), nil
}

// updateStatus updates the backup BackupStatus struct in the PSMDB status
func (c *Controller) updateStatus(backupTask *v1alpha1.BackupTaskSpec, replset *v1alpha1.ReplsetSpec) error {
	status := &v1alpha1.BackupStatus{
		Enabled: backupTask.Enabled,
		Name:    backupTask.Name,
	}

	data, err := c.getPSMDBCopy()
	if err != nil {
		return err
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
	err = c.client.Update(data)
	if err != nil {
		return err
	}
	c.psmdb = data

	return nil
}

// deleteStatus deletes the backup BackupStatus struct from the PSMDB status
func (c *Controller) deleteStatus(backup *v1alpha1.BackupTaskSpec) error {
	data, err := c.getPSMDBCopy()
	if err != nil {
		return err
	}

	for i, bkpStatus := range data.Status.Backups {
		if bkpStatus.Name != backup.Name {
			continue
		}
		data.Status.Backups = append(data.Status.Backups[:i], data.Status.Backups[i+1:]...)
		err = c.client.Update(data)
		if err != nil {
			return err
		}
		c.psmdb = data
		break
	}

	return nil
}
