package backup

import (
	"strconv"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

// AgentContainer creates the container object for a backup agent
func AgentContainer(cr *api.PerconaServerMongoDB, replsetName string, replsetSize int32) (corev1.Container, error) {
	res, err := psmdb.CreateResources(cr.Spec.Backup.Resources)
	if err != nil {
		return corev1.Container{}, errors.Wrap(err, "create resources")
	}

	fvar := false
	usersSecretName := cr.Spec.Secrets.Users
	if cr.CompareVersion("1.5.0") >= 0 {
		usersSecretName = "internal-" + cr.Name + "-users"
	}
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
				Value: strconv.Itoa(int(cr.Spec.Mongod.Net.Port)),
			},
		},
		SecurityContext: cr.Spec.Backup.ContainerSecurityContext,
		Resources:       res,
	}, nil
}
