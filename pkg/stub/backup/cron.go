package backup

import (
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	backupCtlContainerName = "backup-pmbctl"
)

func newCronJob(m *v1alpha1.PerconaServerMongoDB, backup *v1alpha1.BackupTaskSpec) *batchv1b.CronJob {
	return &batchv1b.CronJob{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "batch/v1beta1",
			Kind:       "CronJob",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-backup-" + backup.Name,
			Namespace: m.Namespace,
		},
	}
}

func (c *Controller) newBackupCronJob(backup *v1alpha1.BackupTaskSpec) *batchv1b.CronJob {
	backupName := c.psmdb.Name + "-" + backup.Name
	coordinatorSpec := c.psmdb.Spec.Backup.Coordinator
	backupPod := corev1.PodSpec{
		RestartPolicy: c.psmdb.Spec.Backup.RestartPolicy,
		Containers: []corev1.Container{
			{
				Name:            backupCtlContainerName,
				Image:           c.getImageName("pbmctl"),
				ImagePullPolicy: c.psmdb.Spec.ImagePullPolicy,
				Env: []corev1.EnvVar{
					{
						Name:  "PBMCTL_SERVER_ADDRESS",
						Value: c.coordinatorAddress() + ":" + strconv.Itoa(int(coordinatorSpec.APIPort)),
					},
				},
				Args: []string{
					"run", "backup",
					"--description=" + backupName,
					"--destination-type=" + string(backup.DestinationType),
				},
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot: &util.TrueVar,
					RunAsUser:    util.GetContainerRunUID(c.psmdb, c.serverVersion),
				},
			},
		},
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup: util.GetContainerRunUID(c.psmdb, c.serverVersion),
		},
	}

	cronJob := newCronJob(c.psmdb, backup)
	cronJob.Spec = batchv1b.CronJobSpec{
		Schedule:          backup.Schedule,
		ConcurrencyPolicy: batchv1b.ForbidConcurrent,
		JobTemplate: batchv1b.JobTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: util.LabelsForPerconaServerMongoDB(c.psmdb, nil),
			},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: backupPod,
				},
			},
		},
	}
	util.AddOwnerRefToObject(cronJob, util.AsOwner(c.psmdb))
	return cronJob
}
