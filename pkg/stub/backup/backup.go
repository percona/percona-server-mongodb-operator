package backup

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/sdk"

	"github.com/sirupsen/logrus"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	backupContainerName = "backup"
	backupDataVolume    = "backup-data"
)

type Controller struct {
	client        sdk.Client
	psmdb         *v1alpha1.PerconaServerMongoDB
	serverVersion *v1alpha1.ServerVersion
}

func New(psmdb *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion, client sdk.Client) *Controller {
	return &Controller{
		client:        client,
		psmdb:         psmdb,
		serverVersion: serverVersion,
	}
}

func (c *Controller) backupConfigSecretName(replset *v1alpha1.ReplsetSpec, backup *v1alpha1.BackupSpec) string {
	return c.psmdb.Name + "-" + replset.Name + "-backup-config-" + backup.Name
}

func (c *Controller) newMCBConfigSecret(replset *v1alpha1.ReplsetSpec, backup *v1alpha1.BackupSpec, pods []corev1.Pod, usersSecret *corev1.Secret) (*corev1.Secret, error) {
	config, err := c.newMCBConfigYAML(replset, backup, pods, usersSecret)
	if err != nil {
		return nil, err
	}
	return internal.NewPSMDBSecret(c.psmdb, c.psmdb.Name+"-"+replset.Name+"-backup-config", map[string]string{
		backupConfigFile: string(config),
	}), nil
}

func (c *Controller) backupCronJobName(replset *v1alpha1.ReplsetSpec, backup *v1alpha1.BackupSpec) string {
	return c.psmdb.Name + "-" + replset.Name + "-backup-" + backup.Name
}

func (c *Controller) newCronJob(replset *v1alpha1.ReplsetSpec, backup *v1alpha1.BackupSpec) *batchv1b.CronJob {
	return &batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.backupCronJobName(replset, backup),
			Namespace: c.psmdb.Namespace,
		},
	}
}

func (c *Controller) newPSMDBBackupCronJob(replset *v1alpha1.ReplsetSpec, backup *v1alpha1.BackupSpec, pods []corev1.Pod, configSecret *corev1.Secret) *batchv1b.CronJob {
	backupPod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:  backupContainerName,
				Image: backupImagePrefix + ":" + backupImageVersion,
				Args: []string{
					"--config=/etc/mongodb-consistent-backup/" + backupConfigFile,
				},
				Env: []corev1.EnvVar{
					{
						Name:  "PEX_ROOT",
						Value: "/data/.pex",
					},
				},
				WorkingDir: "/data",
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot: &internal.TrueVar,
					RunAsUser:    internal.GetContainerRunUID(c.psmdb, c.serverVersion),
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      backupDataVolume,
						MountPath: "/data",
					},
					{
						Name:      configSecret.Name,
						MountPath: "/etc/mongodb-consistent-backup",
						ReadOnly:  true,
					},
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup: internal.GetContainerRunUID(c.psmdb, c.serverVersion),
		},
		Volumes: []corev1.Volume{
			{
				Name: backupDataVolume,
				//TODO: make backups persistent
				//PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				//	ClaimName: m.Name + "-backup-data",
				//},
			},
			{
				Name: configSecret.Name,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  configSecret.Name,
						DefaultMode: &backupConfigFileMode,
						Optional:    &internal.FalseVar,
					},
				},
			},
		},
	}
	cronJob := c.newCronJob(replset, backup)
	cronJob.Spec = batchv1b.CronJobSpec{
		Schedule:          backup.Schedule,
		ConcurrencyPolicy: batchv1b.ForbidConcurrent,
		JobTemplate: batchv1b.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: internal.LabelsForPerconaServerMongoDB(c.psmdb, replset),
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: backupPod,
				},
			},
		},
	}
	internal.AddOwnerRefToObject(cronJob, internal.AsOwner(c.psmdb))
	return cronJob
}

func (c *Controller) updateBackupCronJob(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret, cronJob *batchv1b.CronJob) error {
	err := c.client.Get(cronJob)
	if err != nil {
		logrus.Errorf("failed to get cronJob %s: %v", cronJob.Name, err)
		return err
	}

	configSecret := internal.NewPSMDBSecret(c.psmdb, c.backupConfigSecretName(replset, backup), map[string]string{})
	expectedConfig, err := c.newMCBConfigYAML(replset, pods, usersSecret)
	if err != nil {
		logrus.Errorf("failed to marshal config to yaml: %v")
		return err
	}
	err = c.client.Get(configSecret)
	if err != nil {
		logrus.Errorf("failed to get config secret %s: %v", configSecret.Name, err)
		return err
	}
	if string(configSecret.Data[backupConfigFile]) != string(expectedConfig) {
		logrus.Infof("updating backup cronjob for replset %s", replset.Name)
		configSecret.Data[backupConfigFile] = []byte(expectedConfig)
		return c.client.Update(configSecret)
	}

	return nil
}

func (c *Controller) ensureReplsetBackupsCronJob(replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) error {
	for _, backup := range c.psmdb.Spec.Backups {
		// check if backup should be disabled
		cronJob := newCronJob(m, replset, backup)
		err := c.client.Get(cronJob)
		if err == nil && !backup.Enabled {
			logrus.Info("backup %s is disabled, removing backup cronJob and config", backup.Name)
			err = c.client.Delete(cronJob)
			if err != nil {
				logrus.Errorf("failed to remove backup cronJob: %v", err)
				return err
			}
			err = c.client.Delete(internal.NewPSMDBSecret(m, c.backupConfigSecretName(), map[string]string{}))
			if err != nil {
				logrus.Errorf("failed to remove backup configMap: %v", err)
				return err
			}
			return nil
		}
		if !backup.Enabled {
			return nil
		}

		// create the config secret for the backup tool config file
		configSecret, err := c.newMCBConfigSecret(replset, backup, pods, usersSecret)
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
		}

		// create the backup cronJob
		cronJob = c.newPSMDBBackupCronJob(replset, backup, pods, configSecret)
		err = c.client.Create(cronJob)
		if err != nil {
			if k8serrors.IsAlreadyExists(err) {
				return c.updateBackupCronJob(replset, backup, pods, usersSecret, cronJob)
			} else {
				logrus.Errorf("failed to create backup cronJob for replset %s backup %s: %v", replset.Name, backup.Name, err)
				return err
			}
		}
		logrus.Infof("created backup cronJob for replset %s backup: %s", replset.Name, backup.Name)
	}
	return nil
}
