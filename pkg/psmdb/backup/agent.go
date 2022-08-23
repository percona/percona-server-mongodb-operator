package backup

import (
	"strconv"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

// AgentContainer creates the container object for a backup agent
func AgentContainer(cr *api.PerconaServerMongoDB, replsetName string) corev1.Container {
	fvar := false
	usersSecretName := api.UserSecretName(cr)

	c := corev1.Container{
		Name:            agentContainerName,
		Image:           cr.Spec.Backup.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Env: []corev1.EnvVar{
			{
				Name: "PBM_AGENT_MONGODB_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: "MONGODB_BACKUP_USER",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: usersSecretName,
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
							Name: usersSecretName,
						},
						Optional: &fvar,
					},
				},
			},
			{
				Name:  "PBM_MONGODB_REPLSET",
				Value: replsetName,
			},
			{
				Name:  "PBM_MONGODB_PORT",
				Value: strconv.Itoa(int(api.MongodPort(cr))),
			},
		},
		SecurityContext: cr.Spec.Backup.ContainerSecurityContext,
		Resources:       cr.Spec.Backup.Resources,
	}

	if cr.Spec.Sharding.Enabled {
		c.Env = append(c.Env, corev1.EnvVar{Name: "SHARDED", Value: "TRUE"})
	}

	if cr.CompareVersion("1.13.0") >= 0 {
		c.Command = []string{psmdb.BinMountPath + "/pbm-entry.sh"}
		c.Args = []string{"pbm-agent"}
		c.VolumeMounts = append(c.VolumeMounts, []corev1.VolumeMount{
			{
				Name:      "ssl",
				MountPath: psmdb.SSLDir,
				ReadOnly:  true,
			},
			{
				Name:      psmdb.BinVolumeName,
				MountPath: psmdb.BinMountPath,
				ReadOnly:  true,
			},
		}...)
	}

	return c
}
