package backup

import (
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	corev1 "k8s.io/api/core/v1"
)

const (
	agentContainerImage       = "percona/percona-backup-mongodb:agent"
	agentContainerName        = "backup-agent"
	agentBackupDataMount      = "/backup"
	agentBackupDataVolumeName = "backup-data"
)

func (c *Controller) NewAgentContainer(psmdb *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) corev1.Container {
	return corev1.Container{
		Name:  agentContainerName,
		Image: agentContainerImage,
		Env: []corev1.EnvVar{
			//{
			//	Name: "PBM_AGENT_BACKUP_DIR",
			//	Value: agentBackupDataMount,
			//},
			{
				Name:  "PBM_AGENT_SERVER_ADDRESS",
				Value: c.coordinatorRPCAddress(psmdb),
			},
			{
				Name:  "PBM_AGENT_MONGODB_HOST",
				Value: "127.0.0.1",
			},
			{
				Name:  "PBM_AGENT_MONGODB_PORT",
				Value: strconv.Itoa(int(psmdb.Spec.Mongod.Net.Port)),
			},
			{
				Name: "PBM_AGENT_MONGODB_USER",
				ValueFrom: util.EnvVarSourceFromSecret(
					psmdb.Spec.Secrets.Users,
					motPkg.EnvMongoDBBackupUser,
				),
			},
			{
				Name: "PBM_AGENT_MONGODB_PASSWORD",
				ValueFrom: util.EnvVarSourceFromSecret(
					psmdb.Spec.Secrets.Users,
					motPkg.EnvMongoDBBackupPassword,
				),
			},
			{
				Name:  "PBM_AGENT_MONGODB_RECONNECT_DELAY",
				Value: "30",
			},
		},
		//WorkingDir: agentBackupDataMount,
		//Resources: util.GetContainerResourceRequirements(resources),
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &util.TrueVar,
			RunAsUser:    util.GetContainerRunUID(psmdb, c.serverVersion),
		},
		//VolumeMounts: []corev1.VolumeMount{
		//	{
		//		Name:      agentBackupDataVolumeName,
		//		MountPath: agentBackupDataMount,
		//	},
		//},
	}
}
