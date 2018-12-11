package backup

import (
	//"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
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

func (c *Controller) logFields(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) logrus.Fields {
	return logrus.Fields{
		"backup":  backup.Name,
		"replset": replset.Name,
	}
}

func (c *Controller) pvcName(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) string {
	return backupDataVolume + "-" + backup.Name + "-" + c.psmdb.Name + "-" + replset.Name
}

func (c *Controller) configSecretName(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) string {
	return c.psmdb.Name + "-" + replset.Name + "-backup-config-" + backup.Name
}

func (c *Controller) newMCBConfigSecret(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) (*corev1.Secret, error) {
	config, err := c.newMCBConfigYAML(backup, replset)
	if err != nil {
		return nil, err
	}
	return util.NewSecret(
		c.psmdb,
		c.configSecretName(backup, replset),
		map[string]string{
			backupConfigFile: string(config),
		},
	), nil
}

func (c *Controller) Delete(backup *v1alpha1.BackupSpec) error {
	//cronJob := c.newCronJob()
	//err := c.client.Delete(cronJob)
	//if err != nil {
	//	logrus.Errorf("failed to remove backup cronJob %s: %v", cronJob.Name, err)
	//	return err
	//}
	//err = c.client.Delete(util.NewSecret(
	//	c.psmdb,
	//	c.configSecretName(),
	//	nil,
	//))
	//if err != nil {
	//	logrus.Errorf("failed to remove backup cronJob %s: %v", cronJob.Name, err)
	//	return err
	//}
	return c.deleteStatus(backup)
}

func (c *Controller) Get(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) error {
	return c.client.Get(c.newCronJob(backup, replset))
}

func (c *Controller) Create(backup *v1alpha1.BackupSpec) error {
	// parse resources
	//resources, err := util.ParseResourceSpecRequirements(backup.Limits, backup.Requests)
	//if err != nil {
	//	return err
	//}

	// create the PVC for the backup data
	//backupPVC := util.NewPersistentVolumeClaim(c.psmdb, resources, c.pvcName(), backup.StorageClass)
	//err = c.client.Create(&backupPVC)
	//if err != nil {
	//	if !k8serrors.IsAlreadyExists(err) {
	//		logrus.WithFields(c.logFields()).Errorf("failed to create persistent volume claim: %v", err)
	//		return err
	//	}
	//} else {
	//	logrus.WithFields(c.logFields()).Infof("created backup persistent volume claim: %s", backupPVC.Name)
	//}

	//// create the config secret for the backup tool config file
	//configSecret, err := c.newMCBConfigSecret()
	//if err != nil {
	//	logrus.WithFields(c.logFields()).Errorf("failed to to create config secret: %v", err)
	//	return err
	//}
	//err = c.client.Create(configSecret)
	//if err != nil {
	//	if !k8serrors.IsAlreadyExists(err) {
	//		logrus.WithFields(c.logFields()).Errorf("failed to create config secret: %v", err)
	//		return err
	//	}
	//} else {
	//	logrus.WithFields(c.logFields()).Infof("created backup config secret: %s", configSecret.Name)
	//}

	//// create the backup cronJob, pass any error (including the already-exists error) up
	//cronJob, err := c.newBackupCronJob(resources, configSecret)
	//if err != nil {
	//	logrus.WithFields(c.logFields()).Errorf("failed to generate backup cronJob: %v", err)
	//	return err
	//}
	//err = c.client.Create(cronJob)
	//if err != nil {
	//	return err
	//}
	//logrus.WithFields(c.logFields()).Infof("created backup cronJob: %s", cronJob.Name)

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
func (c *Controller) updateStatus(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) error {
	status := &v1alpha1.BackupStatus{
		Enabled: backup.Enabled,
		Name:    backup.Name,
		CronJob: c.cronJobName(backup, replset),
		Replset: replset.Name,
	}

	data, err := c.getPSMDBCopy()
	if err != nil {
		return err
	}

	for i, bkpStatus := range data.Status.Backups {
		if bkpStatus.Name == backup.Name {
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
func (c *Controller) deleteStatus(backup *v1alpha1.BackupSpec) error {
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
