package backup

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func AgentContainer(cr *api.PerconaServerMongoDB, replSet string) corev1.Container {
	fvar := false

	return corev1.Container{
		Name:            agentContainerName,
		Image:           cr.Spec.Backup.Image,
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{
				Name: "PBM_AGENT_MONGODB_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: "MONGODB_BACKUP_USER",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cr.Spec.Secrets.Users,
						},
						Optional: &fvar,
					},
				},
			},
			{
				Name: "PBM_AGENT_MONGODB_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: "MONGODB_BACKUP_PASSWORD",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: cr.Spec.Secrets.Users,
						},
						Optional: &fvar,
					},
				},
			},
			{
				Name:  "PBM_MONGODB_REPLSET",
				Value: replSet,
			},
			{
				Name:  "PBM_MONGODB_PORT",
				Value: strconv.Itoa(int(cr.Spec.Mongod.Net.Port)),
			},
		},
		SecurityContext: cr.Spec.Backup.ContainerSecurityContext,
	}
}
