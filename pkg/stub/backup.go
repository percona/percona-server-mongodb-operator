package stub

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
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

func getPSMDBBackupContainerArgs(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) []string {
	addrs := getReplsetAddrs(m, replset, pods)
	return []string{
		"--host=" + replset.Name + "/" + strings.Join(addrs, ","),
		"--username=" + string(usersSecret.Data[motPkg.EnvMongoDBBackupUser]),
		"--password=" + string(usersSecret.Data[motPkg.EnvMongoDBBackupPassword]),
		"--backup.name=" + m.Name,
		"--backup.location=/data",
	}
}

func (h *Handler) newPSMDBBackupCronJob(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) *batchv1b.CronJob {
	backupPod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:  backupContainerName,
				Image: "perconalab/mongodb_consistent_backup:1.3.0-3.6",
				Args:  getPSMDBBackupContainerArgs(m, replset, pods, usersSecret),
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
		},
	}
	cronJob := &batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-" + replset.Name + "-backup",
			Namespace: m.Namespace,
		},
		Spec: batchv1b.CronJobSpec{
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

	expectedArgs := getPSMDBBackupContainerArgs(m, replset, pods, usersSecret)
	container := getPodSpecContainer(&cronJob.Spec.JobTemplate.Spec.Template.Spec, backupContainerName)
	if container == nil {
		return fmt.Errorf("cannot find %s container", backupContainerName)
	}

	if !reflect.DeepEqual(container.Args, expectedArgs) {
		logrus.Infof("updating backup cronjob for replset %s: %v -> %v", replset.Name, container.Args, expectedArgs)
		container.Args = expectedArgs
		return h.client.Update(cronJob)
	}
	return nil
}

func (h *Handler) ensureReplsetBackupCronJob(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) error {
	if !m.Spec.Backup.Enabled || m.Spec.Backup.Schedule == "" {
		return nil
	}

	cronJob := h.newPSMDBBackupCronJob(m, replset, pods, usersSecret)
	err := h.client.Create(cronJob)
	if err != nil {
		if k8serrors.IsAlreadyExists(err) {
			return h.updateBackupCronJob(m, replset, pods, usersSecret, cronJob)
		} else {
			logrus.Errorf("failed to create backup cronJob for replset %s: %v", replset.Name, err)
			return err
		}
	} else {
		logrus.Infof("created backup cronJob for replset: %s", replset.Name)
	}

	return nil
}
