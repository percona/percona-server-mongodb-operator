package backup

import (
	//"reflect"

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
	backup        *v1alpha1.BackupSpec
	replset       *v1alpha1.ReplsetSpec
	serverVersion *v1alpha1.ServerVersion
	pods          []corev1.Pod
	usersSecret   *corev1.Secret
}

func New(client sdk.Client,
	psmdb *v1alpha1.PerconaServerMongoDB,
	backup *v1alpha1.BackupSpec,
	replset *v1alpha1.ReplsetSpec,
	serverVersion *v1alpha1.ServerVersion,
	pods []corev1.Pod,
	usersSecret *corev1.Secret) *Controller {
	return &Controller{
		client:        client,
		psmdb:         psmdb,
		backup:        backup,
		replset:       replset,
		serverVersion: serverVersion,
		pods:          pods,
		usersSecret:   usersSecret,
	}
}

func (c *Controller) logFields() logrus.Fields {
	return logrus.Fields{
		"backup":  c.backup.Name,
		"replset": c.replset.Name,
	}
}

func (c *Controller) pvcName() string {
	return backupDataVolume + "-" + c.backup.Name + "-" + c.psmdb.Name + "-" + c.replset.Name
}

func (c *Controller) configSecretName() string {
	return c.psmdb.Name + "-" + c.replset.Name + "-backup-config-" + c.backup.Name
}

func (c *Controller) newMCBConfigSecret() (*corev1.Secret, error) {
	config, err := c.newMCBConfigYAML()
	if err != nil {
		return nil, err
	}
	return util.NewSecret(
		c.psmdb,
		c.configSecretName(),
		map[string]string{
			backupConfigFile: string(config),
		},
	), nil
}

func (c *Controller) Delete() error {
	cronJob := c.newCronJob()
	err := c.client.Delete(cronJob)
	if err != nil {
		logrus.Errorf("failed to remove backup cronJob %s: %v", cronJob.Name, err)
		return err
	}
	err = c.client.Delete(util.NewSecret(
		c.psmdb,
		c.configSecretName(),
		nil,
	))
	if err != nil {
		logrus.Errorf("failed to remove backup cronJob %s: %v", cronJob.Name, err)
		return err
	}
	return c.deleteStatus()
}

func (c *Controller) Create() error {
	// check if backup should be disabled
	err := c.client.Get(c.newCronJob())
	if err == nil && !c.backup.Enabled {
		logrus.WithFields(c.logFields()).Info("backup is disabled, removing backup cronJob and config")
		return c.Delete()
	}
	if !c.backup.Enabled {
		return nil
	}

	// parse resources
	resources, err := util.ParseResourceSpecRequirements(c.backup.Limits, c.backup.Requests)
	if err != nil {
		return err
	}

	// create the PVC for the backup data
	backupPVC := util.NewPersistentVolumeClaim(c.psmdb, resources, c.pvcName(), c.backup.StorageClass)
	err = c.client.Create(&backupPVC)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			logrus.WithFields(c.logFields()).Errorf("failed to create persistent volume claim: %v", err)
			return err
		}
	} else {
		logrus.WithFields(c.logFields()).Infof("created backup persistent volume claim: %s", backupPVC.Name)
	}

	// create the config secret for the backup tool config file
	configSecret, err := c.newMCBConfigSecret()
	if err != nil {
		logrus.WithFields(c.logFields()).Errorf("failed to to create config secret: %v", err)
		return err
	}
	err = c.client.Create(configSecret)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			logrus.WithFields(c.logFields()).Errorf("failed to create config secret: %v", err)
			return err
		}
	} else {
		logrus.WithFields(c.logFields()).Infof("created backup config secret: %s", configSecret.Name)
	}

	// create the backup cronJob, pass any error (including the already-exists error) up
	cronJob, err := c.newBackupCronJob(resources, configSecret)
	if err != nil {
		logrus.WithFields(c.logFields()).Errorf("failed to generate backup cronJob: %v", err)
		return err
	}
	err = c.client.Create(cronJob)
	if err != nil {
		return err
	}
	logrus.WithFields(c.logFields()).Infof("created backup cronJob: %s", cronJob.Name)

	return nil
}

// updateStatus updates the BackupStatus struct
func (c *Controller) updateStatus() error {
	status := &v1alpha1.BackupStatus{
		Enabled: c.backup.Enabled,
		Name:    c.backup.Name,
		CronJob: c.cronJobName(),
		Replset: c.replset.Name,
	}
	data := c.psmdb.DeepCopy()
	for i, bkpStatus := range data.Status.Backups {
		if bkpStatus.Name == c.backup.Name {
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
	err := c.client.Update(data)
	if err != nil {
		return err
	}
	c.psmdb = data
	return nil
}

func (c *Controller) deleteStatus() error {
	err := c.client.Get(c.psmdb)
	if err != nil {
		return err
	}
	data := c.psmdb.DeepCopy()
	for i, bkpStatus := range data.Status.Backups {
		if bkpStatus.Name != c.backup.Name {
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
