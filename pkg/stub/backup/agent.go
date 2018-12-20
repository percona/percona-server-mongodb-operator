package backup

import (
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	corev1 "k8s.io/api/core/v1"
)

const (
	agentContainerName        = "backup-agent"
	agentBackupDataMount      = "/backup"
	agentBackupDataVolumeName = "backup-data"
	envAWSAccessKey           = "AWS_ACCESS_KEY_ID"
	envAWSSecretKey           = "AWS_SECRET_ACCESS_KEY"
)

func (c *Controller) hasAWSBackups() bool {
	if c.psmdb.Spec.Backup == nil {
		return false
	}
	for _, backup := range c.psmdb.Spec.Backup.Tasks {
		if backup.DestinationType == v1alpha1.BackupDestinationAWS {
			return true
		}
	}
	return false
}

func (c *Controller) newAgentContainerArgs() []corev1.EnvVar {
	args := []corev1.EnvVar{
		{
			Name:  "PBM_AGENT_SERVER_ADDRESS",
			Value: c.coordinatorRPCAddress(),
		},
		{
			Name:  "PBM_AGENT_MONGODB_HOST",
			Value: "127.0.0.1",
		},
		{
			Name:  "PBM_AGENT_MONGODB_PORT",
			Value: strconv.Itoa(int(c.psmdb.Spec.Mongod.Net.Port)),
		},
		{
			Name: "PBM_AGENT_MONGODB_USER",
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
	if c.hasAWSBackups() {
		awsEnvs := []corev1.EnvVar{
			{
				Name: envAWSAccessKey,
				ValueFrom: util.EnvVarSourceFromSecret(
					c.psmdb.Spec.Secrets.BackupAWS,
					envAWSAccessKey,
				),
			},
			{
				Name: envAWSSecretKey,
				ValueFrom: util.EnvVarSourceFromSecret(
					c.psmdb.Spec.Secrets.BackupAWS,
					envAWSSecretKey,
				),
			},
		}
		args = append(args, awsEnvs...)
	}
	return args
}

func (c *Controller) NewAgentContainer(replset *v1alpha1.ReplsetSpec) corev1.Container {
	return corev1.Container{
		Name:  agentContainerName,
		Image: c.getImageName("agent"),
		Env:   c.newAgentContainerArgs(),
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &util.TrueVar,
			RunAsUser:    util.GetContainerRunUID(c.psmdb, c.serverVersion),
		},
	}
}
