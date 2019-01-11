package backup

import (
	"errors"
	"fmt"
	"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

// DefaultDestinationType is the default backup destination type
var DefaultDestinationType = v1alpha1.BackupDestinationS3

// DefaultVersion is the default version of the percona/percona-backup-mongodb project
const DefaultVersion = "0.2.0"

// DefaultS3SecretName is the default name of the AWS S3 credentials secret
const DefaultS3SecretName = "percona-server-mongodb-backup-s3"

const backupImagePrefix = "percona/percona-server-mongodb-operator"

// Controller is responsible for controlling backup configuration
type Controller struct {
	client        sdk.Client
	psmdb         *v1alpha1.PerconaServerMongoDB
	serverVersion *v1alpha1.ServerVersion
	usersSecret   *corev1.Secret
}

// New returns a new Controller
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

// Create creates a backup task cronJob and updates the CR status
func (c *Controller) Create(backup *v1alpha1.BackupTaskSpec) error {
	cronJob := c.newBackupCronJob(backup)
	err := c.client.Create(cronJob)
	if err != nil {
		return err
	}
	return c.updateStatus(backup)
}

// Update updates a backup task cronJob and updates the CR spec and status
func (c *Controller) Update(backup *v1alpha1.BackupTaskSpec) error {
	var doUpdate bool
	cronJob := newCronJob(c.psmdb, backup)

	err := c.client.Get(cronJob)
	if err != nil {
		logrus.Errorf("failed to get cronJob for backup %s: %v", backup.Name, err)
		return err
	}

	cronJobPodSpec := cronJob.Spec.JobTemplate.Spec.Template.Spec
	container := util.GetPodSpecContainer(&cronJobPodSpec, backupCtlContainerName)
	if container == nil {
		logrus.Errorf("cannot find cronJob container spec for backup %s", backup.Name)
		return errors.New("cannot find cronJob container spec for backup")
	}

	// update cronJob spec if container args changed
	if !reflect.DeepEqual(container.Args, c.newBackupCronJobContainerArgs(backup)) {
		container.Args = c.newBackupCronJobContainerArgs(backup)
		doUpdate = true
	}

	// update cronJob spec if container env changed
	if !reflect.DeepEqual(container.Env, c.newBackupCronJobContainerEnv()) {
		container.Env = c.newBackupCronJobContainerEnv()
		doUpdate = true
	}

	// update cronJob spec if schedule changed
	if cronJob.Spec.Schedule != backup.Schedule {
		cronJob.Spec.Schedule = backup.Schedule
		doUpdate = true
	}

	if doUpdate {
		err = c.client.Update(cronJob)
		if err != nil {
			logrus.Errorf("failed to update cronJob for backup %s: %v", backup.Name, err)
			return err
		}
		logrus.Infof("updated cronJob spec for backup: %s", backup.Name)
	}

	return c.updateStatus(backup)
}

// Delete deletes a backup task cronJob and CR status
func (c *Controller) Delete(backup *v1alpha1.BackupTaskSpec) error {
	err := c.client.Delete(newCronJob(c.psmdb, backup))
	if err != nil && !k8serrors.IsNotFound(err) {
		logrus.Errorf("failed to delete cronJob for backup %s: %v", backup.Name, err)
		return err
	}
	return c.deleteStatus(backup)
}

// EnsureBackupTasks ensures backup tasks are created if enabled, updated if
// they already exist or removed if they are disabled
func (c *Controller) EnsureBackupTasks() error {
	for _, task := range c.psmdb.Spec.Backup.Tasks {
		if task.Enabled && task.Schedule != "" {
			// create/update or delete backup task
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
			// delete disabled backup task
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

// DeleteBackupTasks deletes all cronJobs and CR statuses for backup tasks
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
		err := c.Delete(&v1alpha1.BackupTaskSpec{
			Name: taskStatus.Name,
		})
		if err != nil && !k8serrors.IsNotFound(err) {
			logrus.Errorf("failed to delete disabled cronJob for backup: %s", taskStatus.Name)
			return err
		}
		logrus.Infof("deleted cronJob for disabled backup: %s", taskStatus.Name)
	}

	return nil
}
