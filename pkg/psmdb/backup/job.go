package backup

import (
	"strconv"

	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/version"
)

func BackupCronJob(backup *api.BackupTaskSpec, crName, namespace, image string, imagePullSecrets []corev1.LocalObjectReference, sv *version.ServerVersion) *batchv1b.CronJob {
	var fsgroup *int64
	if sv.Platform == api.PlatformKubernetes {
		var tp int64 = 1001
		fsgroup = &tp
	}

	trueVar := true

	backupPod := corev1.PodSpec{
		RestartPolicy:    corev1.RestartPolicyNever,
		ImagePullSecrets: imagePullSecrets,
		Containers: []corev1.Container{
			{
				Name:    backupCtlContainerName,
				Image:   image,
				Command: []string{"pbmctl"},
				Env: []corev1.EnvVar{
					{
						Name:  "PBMCTL_SERVER_ADDRESS",
						Value: crName + coordinatorSuffix + ":" + strconv.Itoa(coordinatorAPIPort),
					},
				},
				Args: newBackupCronJobContainerArgs(backup, crName),
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot: &trueVar,
					RunAsUser:    fsgroup,
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup: fsgroup,
		},
	}

	return &batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      crName + "-backup-" + backup.Name,
			Namespace: namespace,
		},
		Spec: batchv1b.CronJobSpec{
			Schedule:          backup.Schedule,
			ConcurrencyPolicy: batchv1b.ForbidConcurrent,
			JobTemplate: batchv1b.JobTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						"app":     "percona-server-mongodb",
						"cluster": crName,
					},
				},
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: backupPod,
					},
				},
			},
		},
	}
}

func newBackupCronJobContainerArgs(backup *api.BackupTaskSpec, crName string) []string {
	args := []string{
		"run", "backup",
		"--description=" + crName + "-" + backup.Name,
		"--storage=" + backup.StorageName,
	}

	switch backup.CompressionType {
	case api.BackupCompressionGzip:
		args = append(args, "--compression-algorithm=gzip")
	default:
		args = append(args, "--compression-algorithm=none")
	}

	return args
}
