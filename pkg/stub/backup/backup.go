package backup

import (
	"fmt"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
	if err != nil && !k8serrors.IsNotFound(err) {
		logrus.Errorf("failed to delete cronJob for backup %s: %v", backup.Name, err)
		return err
	}
	return c.deleteStatus(backup)
}

func (c *Controller) Update(backup *v1alpha1.BackupTaskSpec) error {
	cronJob := newCronJob(c.psmdb, backup)

	err := c.client.Get(cronJob)
	if err != nil {
		logrus.Errorf("failed to get cronJob for backup %s: %v", backup.Name, err)
		return err
	}

	if cronJob.Spec.Schedule != backup.Schedule {
		cronJob.Spec.Schedule = backup.Schedule
		err = c.client.Update(cronJob)
		if err != nil {
			logrus.Errorf("failed to update cronJob for backup %s: %v", backup.Name, err)
			return err
		}
		logrus.Infof("updated cronJob schedulefor backup: %s", backup.Name)
	}

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

func (c *Controller) EnsureBackupTasks() error {
	// create/update or delete backup tasks
	for _, task := range c.psmdb.Spec.Backup.Tasks {
		if task.Enabled && task.Schedule != "" {
			err := c.Create(task)
			if err != nil {
				if !k8serrors.IsAlreadyExists(err) {
					logrus.Errorf("failed to create backup task %s: %v", task.Name, err)
					return err
				} else {
					err = c.Update(task)
					if err != nil {
						logrus.Errorf("failed to update backup task %s: %v", task.Name, err)
						return err
					}
					logrus.Debugf("updated cronJob for backup: %s", task.Name)
				}
			} else {
				logrus.Infof("create cronJob for backup: %s", task.Name)
			}
		} else {
			for _, taskStatus := range c.psmdb.Status.Backups {
				if taskStatus.Name != task.Name {
					continue
				}
				err := c.Delete(task)
				if err != nil {
					logrus.Errorf("failed to delete disabled cronJob for backup: %s", task.Name)
					return err
				}
				logrus.Infof("deleted cronJob for disabled backup: %s", task.Name)
			}
		}
	}

	return nil
}

func (c *Controller) DeleteBackupTasks() error {
	// delete known tasks from CR spec
	for _, task := range c.psmdb.Spec.Backup.Tasks {
		err := c.Delete(task)
		if err != nil && !k8serrors.IsNotFound(err) {
			logrus.Errorf("failed to delete disabled cronJob for backup: %s", task.Name)
			return err
		}
		logrus.Infof("deleted cronJob for disabled backup: %s", task.Name)
	}

	// delete tasks from CR status (in case they are not in the Spec)
	for _, taskStatus := range c.psmdb.Status.Backups {
		cronJob := newCronJob(c.psmdb, &v1alpha1.BackupTaskSpec{
			Name: taskStatus.Name,
		})
		err := c.client.Delete(cronJob)
		if err != nil && !k8serrors.IsNotFound(err) {
			logrus.Errorf("failed to delete disabled cronJob for backup: %s", taskStatus.Name)
			return err
		}
		logrus.Infof("deleted cronJob for disabled backup: %s", taskStatus.Name)
	}

	return nil
}
