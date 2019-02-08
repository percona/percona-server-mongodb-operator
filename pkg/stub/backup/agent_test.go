package backup

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk/mocks"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	pbmStorage "github.com/percona/percona-backup-mongodb/storage"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStubBackupNewAgentContainer(t *testing.T) {
	c := New(nil, &v1alpha1.PerconaServerMongoDB{
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: &v1alpha1.BackupSpec{},
			Mongod: &v1alpha1.MongodSpec{
				Net: &v1alpha1.MongodSpecNet{
					Port: int32(0),
				},
			},
			Secrets: &v1alpha1.SecretsSpec{
				Users: "users-secret",
			},
		},
	}, nil, nil)

	replset := &v1alpha1.ReplsetSpec{
		Name: t.Name() + "-rs",
	}
	container := c.NewAgentContainer(replset)
	assert.NotNil(t, container)
	assert.NotNil(t, container.SecurityContext.RunAsUser)
	assert.Equal(t, backupImagePrefix+":backup-agent", container.Image)

	// test with version set
	c.psmdb.Spec.Backup.Version = "0.0.0"
	container = c.NewAgentContainer(replset)
	assert.NotNil(t, container)
	assert.Equal(t, backupImagePrefix+":0.0.0-backup-agent", container.Image)

	assert.Len(t, container.Env, 6)
}

func TestNewAgentStoragesConfig(t *testing.T) {
	storagesSpec := map[string]v1alpha1.BackupStorageSpec{
		"test": v1alpha1.BackupStorageSpec{
			Type: v1alpha1.BackupStorageS3,
			S3: v1alpha1.BackupStorageS3Spec{
				Bucket:            "my-s3-bucket-name",
				Region:            "us-west-2",
				CredentialsSecret: "test-s3-credentials",
				EndpointURL:       "https://minio.local/minio",
			},
		},
	}

	client := &mocks.Client{}
	c := New(client, &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: &v1alpha1.BackupSpec{
				Storages: storagesSpec,
			},
		},
	}, nil, nil)

	client.On("Get", mock.AnythingOfType("*v1.Secret")).Return(nil).Run(func(args mock.Arguments) {
		secret := args.Get(0).(*corev1.Secret)
		assert.Equal(t, "test-s3-credentials", secret.Name)
		secret.Data = map[string][]byte{
			awsAccessKeySecretKey:       []byte("test-aws-access-key"),
			awsSecretAccessKeySecretKey: []byte("test-aws-secret-access-key"),
		}
	})

	secret, err := c.newAgentStoragesConfig()
	assert.NoError(t, err)
	assert.NotNil(t, secret)
	assert.Equal(t, t.Name()+"-backup-agent-config", secret.Name)

	storages := &pbmStorage.Storages{}
	err = yaml.Unmarshal([]byte(secret.StringData[agentConfigFileName]), storages)
	assert.NoError(t, err)
	assert.NotNil(t, storages.Storages["test"])

	testStorage := storages.Storages["test"]
	assert.Equal(t, "us-west-2", testStorage.S3.Region)
	assert.Equal(t, "https://minio.local/minio", testStorage.S3.EndpointURL)
	assert.Equal(t, "my-s3-bucket-name", testStorage.S3.Bucket)
	assert.Equal(t, "test-aws-access-key", testStorage.S3.Credentials.AccessKeyID)
	assert.Equal(t, "test-aws-secret-access-key", testStorage.S3.Credentials.SecretAccessKey)
}
