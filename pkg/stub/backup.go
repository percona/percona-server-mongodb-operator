package stub

import (
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	backupDataVolume = "backup-data"
)

func (h *Handler) newPSMDBBackupCronJob(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, pods []corev1.Pod, usersSecret *corev1.Secret) *batchv1b.CronJob {
	addrs := getReplsetAddrs(m, replset, pods)
	backupPod := corev1.PodSpec{
		RestartPolicy: corev1.RestartPolicyNever,
		Containers: []corev1.Container{
			{
				Name:  "backup",
				Image: "perconalab/mongodb_consistent_backup:1.3.0-3.6",
				Args: []string{
					"--host=" + replset.Name + "/" + strings.Join(addrs, ","),
					"--username=" + string(usersSecret.Data[motPkg.EnvMongoDBBackupUser]),
					"--password=" + string(usersSecret.Data[motPkg.EnvMongoDBBackupPassword]),
					"--backup.name=" + m.Name,
					"--backup.location=/data",
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
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup: h.getContainerRunUID(m),
		},
		Volumes: []corev1.Volume{
			{
				Name: backupDataVolume,
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
