package backup

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"

	corev1 "k8s.io/api/core/v1"
)

const (
	schedulerContainerName = "backup-scheduler"
	schedulerDockerImage   = "percona/percona-backup-mongodb:scheduler"
)

func (c *Controller) newSchedulerPodSpec() corev1.PodSpec {
	return corev1.PodSpec{
		Containers: []corev1.Container{
			{
				Name:            schedulerContainerName,
				Image:           schedulerDockerImage,
				ImagePullPolicy: corev1.PullIfNotPresent,
				Env: []corev1.EnvVar{
					{
						Name:  "PBM_SCHEDULER_SERVER_ADDRESS",
						Value: c.coordinatorRPCAddress(),
					},
				},
				//Resources:  util.GetContainerResourceRequirements(resources),
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
}
