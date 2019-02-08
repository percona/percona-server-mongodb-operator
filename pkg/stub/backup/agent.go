package backup

import (
	"errors"
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	pbmStorage "github.com/percona/percona-backup-mongodb/storage"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
)

// AgentContainerName is the name of the backup agent container
const AgentContainerName = "backup-agent"

const (
	agentConfigDir              = "/etc/percona-backup-mongodb"
	agentConfigFileName         = "agent.yml"
	awsAccessKeySecretKey       = "AWS_ACCESS_KEY_ID"
	awsSecretAccessKeySecretKey = "AWS_SECRET_ACCESS_KEY"
)

func (c *Controller) agentConfigSecretName() string {
	return c.psmdb.Name + "-backup-agent-config"
}

func (c *Controller) newAgentContainerArgs() []corev1.EnvVar {
	return []corev1.EnvVar{
		{
			Name:  "PBM_AGENT_SERVER_ADDRESS",
			Value: c.coordinatorServiceName() + ":" + strconv.Itoa(int(coordinatorRPCPort)),
		},
		{
			Name:  "PBM_AGENT_STORAGES_CONFIG",
			Value: agentConfigDir + "/" + agentConfigFileName,
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
}

func (c *Controller) newAgentStoragesConfig() (*corev1.Secret, error) {
	storages := map[string]pbmStorage.Storage{}

	for storageName, storageSpec := range c.psmdb.Spec.Backup.Storages {
		switch storageSpec.Type {
		case v1alpha1.BackupStorageS3:
			s3secret, err := util.GetSecret(c.psmdb, c.client, storageSpec.S3.CredentialsSecret)
			if err != nil {
				return nil, err
			}
			storages[storageName] = pbmStorage.Storage{
				Type: "s3",
				S3: pbmStorage.S3{
					Bucket:      storageSpec.S3.Bucket,
					Region:      storageSpec.S3.Region,
					EndpointURL: storageSpec.S3.EndpointURL,
					Credentials: pbmStorage.Credentials{
						AccessKeyID:     string(s3secret.Data[awsAccessKeySecretKey]),
						SecretAccessKey: string(s3secret.Data[awsSecretAccessKeySecretKey]),
					},
				},
			}
		case v1alpha1.BackupStorageFilesystem:
			return nil, errors.New("filesystem storage not supported yet")
		default:
			return nil, errors.New("unsupported backup storage type")
		}
	}

	storagesYaml, err := yaml.Marshal(&pbmStorage.Storages{
		Storages: storages,
	})
	if err != nil {
		return nil, err
	}

	return util.NewSecret(c.psmdb, c.agentConfigSecretName(), map[string]string{
		agentConfigFileName: string(storagesYaml),
	}), nil
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
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      c.agentConfigSecretName(),
				MountPath: agentConfigDir,
				ReadOnly:  true,
			},
		},
	}
}

func (c *Controller) NewAgentVolumes() ([]corev1.Volume, error) {
	storagesSecret, err := c.newAgentStoragesConfig()
	if err != nil {
		return nil, err
	}
	return []corev1.Volume{
		{
			Name: storagesSecret.Name,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: storagesSecret.Name,
					Optional:   &util.FalseVar,
				},
			},
		},
	}, nil
}
