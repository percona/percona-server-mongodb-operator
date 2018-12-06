package backup

import (
	"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

func getBackupContainer(podSpec corev1.PodSpec) *corev1.Container {
	for i, container := range podSpec.Containers {
		if container.Name == backupContainerName {
			return &podSpec.Containers[i]
		}
	}
	return nil
}

func (c *Controller) backupPVCName(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) string {
	return backupDataVolume + "-" + backup.Name + "-" + c.psmdb.Name + "-" + replset.Name
}

func (c *Controller) backupConfigSecretName(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) string {
	return c.psmdb.Name + "-" + replset.Name + "-backup-config-" + backup.Name
}

func (c *Controller) newMCBConfigSecret(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod) (*corev1.Secret, error) {
	config, err := c.newMCBConfigYAML(backup, replset)
	if err != nil {
		return nil, err
	}
	return util.NewSecret(
		c.psmdb,
		c.backupConfigSecretName(backup, replset),
		map[string]string{
			backupConfigFile: string(config),
		},
	), nil
}

func (c *Controller) updateBackupStatus(backupStatuses []*v1alpha1.BackupStatus) error {
	if !reflect.DeepEqual(c.psmdb.Status.Backups, backupStatuses) {
		c.psmdb.Status.Backups = backupStatuses
		return c.client.Update(c.psmdb)
	}
	return nil
}

func (c *Controller) Run(replset *v1alpha1.ReplsetSpec, pods []corev1.Pod) error {
	statuses := make([]*v1alpha1.BackupStatus, 0)
	for _, backup := range c.psmdb.Spec.Backups {
		// check if backup should be disabled
		cronJob := c.newCronJob(backup, replset)
		err := c.client.Get(cronJob)
		if err == nil && !backup.Enabled {
			logrus.Infof("backup %s is disabled, removing backup cronJob and config", backup.Name)
			err = c.client.Delete(cronJob)
			if err != nil {
				logrus.Errorf("failed to remove backup cronJob: %v", err)
				return err
			}
			err = c.client.Delete(util.NewSecret(
				c.psmdb,
				c.backupConfigSecretName(backup, replset),
				nil,
			))
			if err != nil {
				logrus.Errorf("failed to remove backup configMap: %v", err)
				return err
			}
			return nil
		}
		if !backup.Enabled {
			continue
		}

		// parse resources
		resources, err := util.ParseResourceSpecRequirements(backup.Limits, backup.Requests)
		if err != nil {
			return err
		}

		// create the PVC for the backup data
		backupPVC := util.NewPersistentVolumeClaim(c.psmdb, resources, c.backupPVCName(backup, replset), backup.StorageClass)
		err = c.client.Create(&backupPVC)
		if err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				logrus.Errorf("failed to persistent volume claim for replset %s backup: %v", replset.Name, err)
				return err
			}
		} else {
			logrus.Infof("created backup PVC for replset %s backup: %s", replset.Name, backup.Name)
		}

		// create the config secret for the backup tool config file
		configSecret, err := c.newMCBConfigSecret(backup, replset, pods)
		if err != nil {
			logrus.Errorf("failed to to create config secret for replset %s backup %s: %v", replset.Name, backup.Name, err)
			return err
		}
		err = c.client.Create(configSecret)
		if err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create config secret for replset %s backup: %v", replset.Name, err)
				return err
			}
		} else {
			logrus.Infof("created backup config-file secret for replset %s backup: %s", replset.Name, backup.Name)
		}

		// create the backup cronJob
		cronJob, err = c.newBackupCronJob(backup, replset, resources, pods, configSecret)
		if err != nil {
			logrus.Errorf("failed to create backup cronJob for replset %s backup %s: %v", replset.Name, backup.Name, err)
			return err
		}
		err = c.client.Create(cronJob)
		if err != nil {
			if !k8serrors.IsAlreadyExists(err) {
				logrus.Errorf("failed to create backup cronJob for replset %s backup %s: %v", replset.Name, backup.Name, err)
				return err
			}
		} else {
			logrus.Infof("created backup cronJob for replset %s backup: %s", replset.Name, backup.Name)
		}

		// update cronJob
		err = c.updateBackupCronJob(backup, replset, resources, pods)
		if err != nil {
			logrus.Errorf("failed to update backup cronJob for replset %s backup %s: %v", replset.Name, backup.Name, err)
			return err
		}

		statuses = append(statuses, &v1alpha1.BackupStatus{
			Enabled: backup.Enabled,
			Name:    backup.Name,
			CronJob: cronJob.Name,
			Replset: replset.Name,
		})
	}

	return c.updateBackupStatus(statuses)
}
