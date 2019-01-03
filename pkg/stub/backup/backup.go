package backup

import (
	"fmt"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

const (
	DefaultVersion = "0.1.0"

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

func (c *Controller) Delete(backup *v1alpha1.BackupTaskSpec) error {
	err := c.client.Delete(newCronJob(c.psmdb, backup))
	if err != nil {
		logrus.Errorf("failed to delete cronJob for backup: %s", backup.Name)
		return err
	}
	return c.deleteStatus(backup)
}

func (c *Controller) Update(backup *v1alpha1.BackupTaskSpec) error {
	return c.updateStatus(backup)
}

func (c *Controller) Create(backup *v1alpha1.BackupTaskSpec) error {
	cronJob := c.newBackupCronJob(backup)
	err := c.client.Create(cronJob)
	if err != nil {
		return err
	}
	return c.updateStatus(backup)
}
