package backup

import (
	"reflect"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

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
	return internal.NewSecret(
		c.psmdb,
		c.backupConfigSecretName(backup, replset),
		map[string]string{
			backupConfigFile: string(config),
		},
	), nil
}

func (c *Controller) backupCronJobName(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) string {
	return c.psmdb.Name + "-" + replset.Name + "-backup-" + backup.Name
}

func (c *Controller) newCronJob(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) *batchv1b.CronJob {
	return &batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.backupCronJobName(backup, replset),
			Namespace: c.psmdb.Namespace,
		},
	}
}

func (c *Controller) newBackupCronJob(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements, pods []corev1.Pod, configSecret *corev1.Secret) (*batchv1b.CronJob, error) {
	ls := internal.LabelsForPerconaServerMongoDB(c.psmdb, replset)
	backupPod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:            backupContainerName,
				Image:           backupImagePrefix + ":" + backupImageVersion,
				ImagePullPolicy: corev1.PullIfNotPresent,
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
				Resources:  internal.GetContainerResourceRequirements(resources),
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
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: c.backupPVCName(backup, replset),
					},
				},
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
	cronJob := c.newCronJob(backup, replset)
	cronJob.ObjectMeta.Labels = ls
	cronJob.Spec = batchv1b.CronJobSpec{
		Schedule:          backup.Schedule,
		ConcurrencyPolicy: batchv1b.ForbidConcurrent,
		JobTemplate: batchv1b.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ls,
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: backupPod,
				},
			},
		},
	}
	internal.AddOwnerRefToObject(cronJob, internal.AsOwner(c.psmdb))
	return cronJob, nil
}

func (c *Controller) updateBackupCronJob(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements, pods []corev1.Pod) error {
	cronJob := c.newCronJob(backup, replset)
	err := c.client.Get(cronJob)
	if err != nil {
		logrus.Errorf("failed to get cronJob %s: %v", cronJob.Name, err)
		return err
	}

	// update the config file secret if necessary
	configSecret := internal.NewSecret(
		c.psmdb,
		c.backupConfigSecretName(backup, replset),
		map[string]string{},
	)
	expectedConfig, err := c.newMCBConfigYAML(backup, replset)
	if err != nil {
		logrus.Errorf("failed to marshal config to yaml: %v", err)
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
		err = c.client.Update(configSecret)
		if err != nil {
			logrus.Errorf("failed to update backup cronjob secret for replset %s: %v", replset.Name, err)
			return err
		}
	}

	expectedCronJob, err := c.newBackupCronJob(backup, replset, resources, pods, configSecret)
	if err != nil {
		return err
	}

	// update the cronJob spec if necessary
	var doUpdate bool
	cronJobContainer := getBackupContainer(cronJob.Spec.JobTemplate.Spec.Template.Spec)
	expectedContainer := getBackupContainer(expectedCronJob.Spec.JobTemplate.Spec.Template.Spec)
	if cronJob.Spec.Schedule != expectedCronJob.Spec.Schedule {
		doUpdate = true
	}
	if cronJobContainer.Image != expectedContainer.Image {
		doUpdate = true
	}
	//if cronJobContainer != nil && expectedContainer != nil && !reflect.DeepEqual(cronJobContainer.Resources, expectedContainer.Resources) {
	//	doUpdate = true
	//}

	if doUpdate {
		logrus.Infof("updating backup cronJob spec for replset %s backup: %s", replset.Name, backup.Name)
		err = c.client.Update(expectedCronJob)
		if err != nil {
			logrus.Errorf("failed to update cronJob for replset %s backup %s: %v", replset.Name, backup.Name, err)
			return err
		}
	}

	return nil
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
			err = c.client.Delete(internal.NewSecret(
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
		resources, err := internal.ParseResourceSpecRequirements(backup.Limits, backup.Requests)
		if err != nil {
			return err
		}

		// create the PVC for the backup data
		backupPVC := internal.NewPersistentVolumeClaim(c.psmdb, resources, c.backupPVCName(backup, replset), backup.StorageClass)
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
