package backup

import (
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	corev1 "k8s.io/api/core/v1"
)

const (
	AgentContainerName = "backup-agent"

	agentBackupDataMount      = "/backup"
	agentBackupDataVolumeName = "backup-data"
)

func (c *Controller) newAgentContainerArgs() []corev1.EnvVar {
	coordinatorSpec := c.psmdb.Spec.Backup.Coordinator
	return []corev1.EnvVar{
		{
			Name:  "PBM_AGENT_SERVER_ADDRESS",
			Value: c.coordinatorAddress() + ":" + strconv.Itoa(int(coordinatorSpec.RPCPort)),
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
