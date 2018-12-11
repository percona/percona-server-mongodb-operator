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

func (c *Controller) cronJobName(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) string {
	return c.psmdb.Name + "-" + replset.Name + "-backup-" + backup.Name
}

func (c *Controller) newCronJob(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) *batchv1b.CronJob {
	return &batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.cronJobName(backup, replset),
			Namespace: c.psmdb.Namespace,
		},
	}
}

func (c *Controller) newBackupCronJob(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements, configSecret *corev1.Secret) (*batchv1b.CronJob, error) {
	ls := util.LabelsForPerconaServerMongoDBReplset(c.psmdb, replset)
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
						ClaimName: c.pvcName(backup, replset),
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

func (c *Controller) Update(backup *v1alpha1.BackupSpec, replset *v1alpha1.ReplsetSpec) error {
	lfs := c.logFields(backup, replset)

	// parse resources
	resources, err := util.ParseResourceSpecRequirements(backup.Limits, backup.Requests)
	if err != nil {
		return err
	}

	cronJob := c.newCronJob(backup, replset)
	err = c.client.Get(cronJob)
	if err != nil {
		logrus.WithFields(lfs).Errorf("failed to get cronJob %s: %v", cronJob.Name, err)
		return err
	}

	// update the config file secret if necessary
	configSecret := util.NewSecret(
		c.psmdb,
		c.configSecretName(backup, replset),
		nil,
	)
	expectedConfig, err := c.newMCBConfigYAML(backup, replset)
	if err != nil {
		logrus.WithFields(lfs).Errorf("failed to marshal config to yaml: %v", err)
		return err
	}
	err = c.client.Get(configSecret)
	if err != nil {
		logrus.WithFields(lfs).Errorf("failed to get config secret %s: %v", configSecret.Name, err)
		return err
	}
	if string(configSecret.Data[backupConfigFile]) != string(expectedConfig) {
		logrus.WithFields(lfs).Infof("updating backup cronjob: %s", cronJob.Name)
		configSecret.Data[backupConfigFile] = []byte(expectedConfig)
		err = c.client.Update(configSecret)
		if err != nil {
			logrus.WithFields(lfs).Errorf("failed to update backup cronjob secret: %v", err)
			return err
		}
	}

	expectedCronJob, err := c.newBackupCronJob(backup, replset, resources, configSecret)
	if err != nil {
		return err
	}

	// update the cronJob spec if necessary
	var doUpdate bool
	cronJobContainer := util.GetPodSpecContainer(&cronJob.Spec.JobTemplate.Spec.Template.Spec, backupContainerName)
	expectedContainer := util.GetPodSpecContainer(&expectedCronJob.Spec.JobTemplate.Spec.Template.Spec, backupContainerName)
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
		logrus.WithFields(lfs).Infof("updating backup cronJob: %s", cronJob.Name)
		err = c.client.Update(expectedCronJob)
		if err != nil {
			logrus.WithFields(lfs).Errorf("failed to update cronJob %s: %v", cronJob.Name, err)
			return err
		}
	}

	return c.updateStatus(backup, replset)
}
