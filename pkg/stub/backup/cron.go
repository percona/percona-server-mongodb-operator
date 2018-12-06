package backup

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	backupContainerName = "backup"
	backupDataVolume    = "backup-data"
	backupDataMount     = "/data"
)

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
	ls := util.LabelsForPerconaServerMongoDB(c.psmdb, replset)
	backupPod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyOnFailure,
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
						Value: backupDataMount + "/.pex",
					},
				},
				WorkingDir: backupDataMount,
				Resources:  util.GetContainerResourceRequirements(resources),
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot: &util.TrueVar,
					RunAsUser:    util.GetContainerRunUID(c.psmdb, c.serverVersion),
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      backupDataVolume,
						MountPath: backupDataMount,
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
			FSGroup: util.GetContainerRunUID(c.psmdb, c.serverVersion),
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
						Optional:    &util.FalseVar,
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
	util.AddOwnerRefToObject(cronJob, util.AsOwner(c.psmdb))
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
	configSecret := util.NewSecret(
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
