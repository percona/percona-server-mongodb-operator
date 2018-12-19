package backup

import (
	"fmt"
	//"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	//"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

const (
	backupImagePrefix = "percona/percona-server-mongodb-operator"
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

func (c *Controller) getImageName(component string) string {
	if c.psmdb.Spec.Backup.Version != "" {
		return fmt.Sprintf("%s:%s-backup-%s", backupImagePrefix, c.psmdb.Spec.Backup.Version, component)
	}
	return fmt.Sprintf("%s:backup-%s", backupImagePrefix, component)
}

func (c *Controller) logFields(backupTask *v1alpha1.BackupTaskSpec, replset *v1alpha1.ReplsetSpec) logrus.Fields {
	return logrus.Fields{
		"backup":  backupTask.Name,
		"replset": replset.Name,
	}
}

func (c *Controller) Delete(backup *v1alpha1.Backup) error {
	err := c.client.Delete(newCronJob(c.psmdb, backup))
	if err != nil {
		logrus.Errorf("failed to delete cronJob for backup %s for replica set %s", backup.Task.Name, backup.Replset.Name)
		return err
	}
	return c.deleteStatus(backup)
}

//func (c *Controller) Get(backup *v1alpha1.Backup) error {
//	return nil
//}

func (c *Controller) Create(backup *v1alpha1.Backup) error {
	logrus.Infof("creating cronJob for backup %s for replset: %s", backup.Task.Name, backup.Replset.Name)
	cronJob := c.newBackupCronJob(backup)
	return c.client.Create(cronJob)
}
