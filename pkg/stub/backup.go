package stub

import (
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	"github.com/sirupsen/logrus"
	yaml "gopkg.in/yaml.v2"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	backupContainerName = "backup"
	backupDataVolume    = "backup-data"
	backupImagePrefix   = "perconalab/mongodb_consistent_backup"
	backupImageVersion  = "1.3.0-3.6"
	backupConfigFile    = "config.yaml"
)

var (
	backupConfigFileMode = int32(0060)
)

// MCBConfig represents the backup section of the config file for mongodb_consistent_backup
// See: https://github.com/Percona-Lab/mongodb_consistent_backup/blob/master/conf/mongodb-consistent-backup.example.conf#L14
type MCBConfigBackup struct {
	Name     string `yaml:"name"`
	Location string `yaml:"location"`
}

// MCBConfig represents the config file for mongodb_consistent_backup
// See: https://github.com/Percona-Lab/mongodb_consistent_backup/blob/master/conf/mongodb-consistent-backup.example.conf
type MCBConfig struct {
	Host     string           `yaml:"host"`
	Username string           `yaml:"username"`
	Password string           `yaml:"password"`
	Backup   *MCBConfigBackup `yaml:"backup"`
	Verbose  bool             `yaml:"verbose,omitempty"`
}

func newMCBConfigYAML(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) (string, error) {
	addrs := getReplsetAddrs(m, replset, pods)
	config := map[string]*MCBConfig{
		"production": &MCBConfig{
			Host:     replset.Name + "/" + strings.Join(addrs, ","),
			Username: string(usersSecret.Data[motPkg.EnvMongoDBBackupUser]),
			Password: string(usersSecret.Data[motPkg.EnvMongoDBBackupPassword]),
			Backup: &MCBConfigBackup{
				Name:     m.Name,
				Location: "/data",
			},
			Verbose: m.Spec.Backup.Verbose,
		},
	}
	bytes, err := yaml.Marshal(config)
	return string(bytes), err
}

func newMCBConfigSecret(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) (*corev1.Secret, error) {
	config, err := newMCBConfigYAML(m, replset, pods, usersSecret)
	if err != nil {
		return nil, err
	}
	return newSecret(m, m.Name+"-"+replset.Name+"-backup-config", map[string]string{
		backupConfigFile: string(config),
	}), nil
}

func newCronJob(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) *batchv1b.CronJob {
	return &batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-" + replset.Name + "-backup",
			Namespace: m.Namespace,
		},
	}
}

func (h *Handler) newPSMDBBackupCronJob(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, configSecret *corev1.Secret) *batchv1b.CronJob {
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
					RunAsNonRoot: &trueVar,
					RunAsUser:    h.getContainerRunUID(m),
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
			FSGroup: h.getContainerRunUID(m),
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
						Optional:    &falseVar,
					},
				},
			},
		},
	}
	cronJob := newCronJob(m, replset)
	cronJob.Spec = batchv1b.CronJobSpec{
		Schedule:          m.Spec.Backup.Schedule,
		ConcurrencyPolicy: batchv1b.ForbidConcurrent,
		JobTemplate: batchv1b.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: labelsForPerconaServerMongoDB(m, replset),
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: backupPod,
				},
			},
		},
	}
	addOwnerRefToObject(cronJob, asOwner(m))
	return cronJob
}

func (h *Handler) updateBackupCronJob(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret, cronJob *batchv1b.CronJob) error {
	err := h.client.Get(cronJob)
	if err != nil {
		logrus.Errorf("failed to get cronJob %s: %v", cronJob.Name, err)
		return err
	}

	configSecret := newSecret(m, m.Name+"-"+replset.Name+"-backup-config", map[string]string{})
	expectedConfig, err := newMCBConfigYAML(m, replset, pods, usersSecret)
	if err != nil {
		logrus.Errorf("failed to marshal config to yaml: %v")
		return err
	}
	err = h.client.Get(configSecret)
	if err != nil {
		logrus.Errorf("failed to get config secret %s: %v", configSecret.Name, err)
		return err
	}
	if string(configSecret.Data[backupConfigFile]) != string(expectedConfig) {
		logrus.Infof("updating backup cronjob for replset %s", replset.Name)
		configSecret.Data[backupConfigFile] = []byte(expectedConfig)
		return h.client.Update(configSecret)
	}

	return nil
}

func (h *Handler) ensureReplsetBackupCronJob(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) error {
	// check if backups should be disabled
	cronJob := newCronJob(m, replset)
	err := h.client.Get(cronJob)
	if err == nil && !m.Spec.Backup.Enabled {
		logrus.Info("backups are disabled, removing backup cronJob and config")
		err = h.client.Delete(cronJob)
		if err != nil {
			logrus.Errorf("failed to remove backup cronJob: %v", err)
			return err
		}
		err = h.client.Delete(newSecret(m, m.Name+"-"+replset.Name+"-backup-config", map[string]string{}))
		if err != nil {
			logrus.Errorf("failed to remove backup configMap: %v", err)
			return err
		}
		return nil
	}
	if !m.Spec.Backup.Enabled {
		return nil
	}

	// create the config secret for the backup tool config file
	configSecret, err := newMCBConfigSecret(m, replset, pods, usersSecret)
	if err != nil {
		logrus.Errorf("failed to to create config secret for replset %s backup: %v", replset.Name, err)
		return err
	}
	err = h.client.Create(configSecret)
	if err != nil {
		if !k8serrors.IsAlreadyExists(err) {
			logrus.Errorf("failed to create config secret for replset %s backup: %v", replset.Name, err)
			return err
		}
	}

	// create the backup cronJob
	cronJob = h.newPSMDBBackupCronJob(m, replset, pods, configSecret)
	err = h.client.Create(cronJob)
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return h.updateBackupCronJob(m, replset, pods, usersSecret, cronJob)
		} else {
			logrus.Errorf("failed to create backup cronJob for replset %s: %v", replset.Name, err)
			return err
		}
	}
	logrus.Infof("created backup cronJob for replset: %s", replset.Name)

	return nil
}
