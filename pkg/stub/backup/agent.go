package backup

import (
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	corev1 "k8s.io/api/core/v1"
)

// AgentContainerName is the name of the backup agent container
const AgentContainerName = "backup-agent"

func (c *Controller) hasS3Backups() bool {
	if c.psmdb.Spec.Backup == nil {
		return false
	}
	for _, backup := range c.psmdb.Spec.Backup.Tasks {
		if backup.DestinationType == v1alpha1.BackupDestinationS3 {
			return true
		}
	}
	return false
}

func (c *Controller) newAgentContainerArgs() []corev1.EnvVar {
	args := []corev1.EnvVar{
		{
			Name:  "PBM_AGENT_SERVER_ADDRESS",
			Value: c.coordinatorAddress() + ":" + strconv.Itoa(int(coordinatorRPCPort)),
		},
		{
			Name:  "PBM_AGENT_MONGODB_PORT",
			Value: strconv.Itoa(int(c.psmdb.Spec.Mongod.Net.Port)),
		},
		{
			Name:  "PBM_AGENT_MONGODB_RECONNECT_DELAY",
			Value: "15",
		},
		{
			Name: "PBM_AGENT_MONGODB_USERNAME",
			ValueFrom: util.EnvVarSourceFromSecret(
				c.psmdb.Spec.Secrets.Users,
				motPkg.EnvMongoDBBackupUser,
			),
		},
		{
			Name: "PBM_AGENT_MONGODB_PASSWORD",
			ValueFrom: util.EnvVarSourceFromSecret(
				c.psmdb.Spec.Secrets.Users,
				motPkg.EnvMongoDBBackupPassword,
			),
		},
	}
	if c.hasS3Backups() {
		s3Envs := []corev1.EnvVar{
			{
				Name:  "PBM_AGENT_BACKUP_DIR",
				Value: c.psmdb.Spec.Backup.S3.Bucket,
			},
			{
				Name:  "AWS_REGION",
				Value: c.psmdb.Spec.Backup.S3.Region,
			},
			{
				Name: "AWS_ACCESS_KEY_ID",
				ValueFrom: util.EnvVarSourceFromSecret(
					c.psmdb.Spec.Backup.S3.Secret,
					"AWS_ACCESS_KEY_ID",
				),
			},
			{
				Name: "AWS_SECRET_ACCESS_KEY",
				ValueFrom: util.EnvVarSourceFromSecret(
					c.psmdb.Spec.Backup.S3.Secret,
					"AWS_SECRET_ACCESS_KEY",
				),
			},
		}
		args = append(args, s3Envs...)
	}
	return args
}

func (c *Controller) NewAgentContainer(replset *v1alpha1.ReplsetSpec) corev1.Container {
	return corev1.Container{
		Name:            AgentContainerName,
		Image:           c.getImageName("agent"),
		ImagePullPolicy: c.psmdb.Spec.ImagePullPolicy,
		Env:             c.newAgentContainerArgs(),
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &util.TrueVar,
			RunAsUser:    util.GetContainerRunUID(c.psmdb, c.serverVersion),
		},
	}
}
