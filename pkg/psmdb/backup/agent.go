package backup

import (
	"context"
	"fmt"
	"strconv"

	pbmStorage "github.com/percona/percona-backup-mongodb/storage"
	yaml "gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func AgentContainer(cr *api.PerconaServerMongoDB) corev1.Container {
	fvar := false

	return corev1.Container{
		Name:            agentContainerName,
		Image:           cr.Spec.Backup.Image,
		Command:         []string{"pbm-agent"},
		ImagePullPolicy: corev1.PullAlways,
		Env: []corev1.EnvVar{
			{
				Name:  "PBM_AGENT_SERVER_ADDRESS",
				Value: cr.Name + coordinatorSuffix + ":" + strconv.Itoa(coordinatorRPCPort),
			},
			{
				Name:  "PBM_AGENT_STORAGES_CONFIG", /* needed for backward compatibility with 1.0.0 */
				Value: agentConfigDir + "/" + agentStoragesConfigFile,
			},
			{
				Name:  "PBM_AGENT_STORAGE_CONFIG",
				Value: agentConfigDir + "/" + agentStoragesConfigFile,
			},
			{
				Name:  "PBM_AGENT_MONGODB_PORT",
				Value: strconv.Itoa(int(cr.Spec.Mongod.Net.Port)),
			},
			{
				Name:  "PBM_AGENT_DEBUG",
				Value: strconv.FormatBool(cr.Spec.Backup.Debug),
			},
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
		},
		SecurityContext: cr.Spec.Backup.ContainerSecurityContext,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      cr.Name + "-backup-agent-config",
				MountPath: agentConfigDir,
				ReadOnly:  true,
			},
		},
	}
}

func AgentVolume(crName string) corev1.Volume {
	fvar := false
	return corev1.Volume{
		Name: crName + "-backup-agent-config",
		VolumeSource: corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: crName + "-backup-agent-config",
				Optional:   &fvar,
			},
		},
	}
}

func AgentStoragesConfigSecret(cr *api.PerconaServerMongoDB, cl client.Client) (*corev1.Secret, error) {
	storages := make(map[string]pbmStorage.Storage)
	for storageName, storageSpec := range cr.Spec.Backup.Storages {
		switch storageSpec.Type {
		case api.BackupStorageS3:
			// https://jira.percona.com/browse/CLOUD-132 workaround
			// with an empty CredentialsSecret k8s.client returns objectsList instead of an object,
			// which in turn cause panic
			if storageSpec.S3.CredentialsSecret == "" {
				return nil, fmt.Errorf("no credentials specified for the secret name %s", storageName)
			}
			s3secret, err := secret(cr.Namespace, storageSpec.S3.CredentialsSecret, cl)
			if err != nil {
				return nil, fmt.Errorf("getting s3 credentials secret name %s: %v", storageName, err)
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
		case api.BackupStorageFilesystem:
			return nil, fmt.Errorf("filesystem backup storage not supported yet, skipping storage name: %s", storageName)
		default:
			return nil, fmt.Errorf("unsupported backup storage type: %s", storageName)
		}
	}

	storagesYaml, err := yaml.Marshal(storages)
	if err != nil {
		return nil, err
	}

	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-backup-agent-config",
			Namespace: cr.Namespace,
		},
		Data: map[string][]byte{agentStoragesConfigFile: storagesYaml},
	}, nil
}

func secret(namespace, secretName string, cl client.Client) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
	}
	err := cl.Get(context.TODO(), types.NamespacedName{Name: secretName, Namespace: namespace}, secret)
	return secret, err
}
