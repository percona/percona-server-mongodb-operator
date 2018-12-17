package backup

import (
	//"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	//"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

type Controller struct {
	client        sdk.Client
	psmdb         *v1alpha1.PerconaServerMongoDB
	serverVersion *v1alpha1.ServerVersion
	usersSecret   *corev1.Secret
}

func New(client sdk.Client, psmdb *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion, usersSecret *corev1.Secret) *Controller {
	return &Controller{
		client:        client,
		psmdb:         psmdb,
		serverVersion: serverVersion,
		usersSecret:   usersSecret,
	}
}

func (c *Controller) logFields(backupTask *v1alpha1.BackupTaskSpec, replset *v1alpha1.ReplsetSpec) logrus.Fields {
	return logrus.Fields{
		"backup":  backupTask.Name,
		"replset": replset.Name,
	}
}

func (c *Controller) Delete(backupTask *v1alpha1.BackupTaskSpec) error {
	return c.deleteStatus(backupTask)
}

func (c *Controller) Get(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) error {
	return nil
}

func (c *Controller) Create(backup *v1alpha1.BackupSpec) error {
	// parse resources
	//resources, err := util.ParseResourceSpecRequirements(backup.Limits, backup.Requests)
	//if err != nil {
	//	return err
	//}
	return nil
}

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
		Replset: replset.Name,
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
func (c *Controller) deleteStatus(backupTask *v1alpha1.BackupTaskSpec) error {
	data, err := c.getPSMDBCopy()
	if err != nil {
		return err
	}

	for i, bkpStatus := range data.Status.Backups {
		if bkpStatus.Name != backupTask.Name {
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
