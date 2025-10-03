package backup

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	// "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint
	"sigs.k8s.io/yaml"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/gcs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	pbmVersion "github.com/percona/percona-backup-mongodb/pbm/version"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestApplyCustomPBMConfig(t *testing.T) {
	cr := expectedCR(t)
	cr.Status.BackupVersion = pbmVersion.Current().Version

	storage := cr.Spec.Backup.Storages["test-s3-storage"]

	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "/v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-secret",
			Namespace: "test-namespace",
		},
	}
	cli := buildFakeClient(t, secret)

	config, err := GetPBMConfig(context.Background(), cli, cr, storage)
	if err != nil {
		t.Fatal(err)
	}

	expectedS3Retryer := storage.S3.Retryer
	if expectedS3Retryer.NumMaxRetries != config.Storage.S3.Retryer.NumMaxRetries {
		t.Errorf("expected %d, got %d", expectedS3Retryer.NumMaxRetries, config.Storage.S3.Retryer.NumMaxRetries)
	}
	if expectedS3Retryer.MaxRetryDelay.Duration != config.Storage.S3.Retryer.MaxRetryDelay {
		t.Errorf("expected %d, got %d", expectedS3Retryer.MaxRetryDelay.Duration, config.Storage.S3.Retryer.MaxRetryDelay)
	}
	if expectedS3Retryer.MinRetryDelay.Duration != config.Storage.S3.Retryer.MinRetryDelay {
		t.Errorf("expected %d, got %d", expectedS3Retryer.MinRetryDelay.Duration, config.Storage.S3.Retryer.MinRetryDelay)
	}

	expectedBackupOptions := cr.Spec.Backup.Configuration.BackupOptions
	if expectedBackupOptions.OplogSpanMin != config.Backup.OplogSpanMin {
		t.Errorf("expected %f, got %f", expectedBackupOptions.OplogSpanMin, config.Backup.OplogSpanMin)
	}
	if len(expectedBackupOptions.Priority) != len(config.Backup.Priority) {
		t.Errorf("expected %d, got %d", len(expectedBackupOptions.Priority), len(config.Backup.Priority))
	}
	if expectedBackupOptions.Priority["localhost:28019"] != config.Backup.Priority["localhost:28019"] {
		t.Errorf("expected %f, got %f", expectedBackupOptions.Priority["localhost:28019"], config.Backup.Priority["localhost:28019"])
	}
	if expectedBackupOptions.Timeouts.Starting != config.Backup.Timeouts.Starting {
		t.Errorf("expected %d, got %d", expectedBackupOptions.Timeouts.Starting, config.Backup.Timeouts.Starting)
	}

	if expectedBackupOptions.NumParallelCollections != config.Backup.NumParallelCollections {
		t.Errorf("expected %d, got %d", expectedBackupOptions.NumParallelCollections, config.Backup.NumParallelCollections)
	}

	expectedRestoreOptions := cr.Spec.Backup.Configuration.RestoreOptions
	if expectedRestoreOptions.BatchSize != config.Restore.BatchSize {
		t.Errorf("expected %d, got %d", expectedRestoreOptions.BatchSize, config.Restore.BatchSize)
	}
	if expectedRestoreOptions.NumInsertionWorkers != config.Restore.NumInsertionWorkers {
		t.Errorf("expected %d, got %d", expectedRestoreOptions.NumInsertionWorkers, config.Restore.NumInsertionWorkers)
	}
	if expectedRestoreOptions.NumDownloadWorkers != config.Restore.NumDownloadWorkers {
		t.Errorf("expected %d, got %d", expectedRestoreOptions.NumDownloadWorkers, config.Restore.NumDownloadWorkers)
	}

	if expectedRestoreOptions.NumParallelCollections != config.Restore.NumParallelCollections {
		t.Errorf("expected %d, got %d", expectedRestoreOptions.NumParallelCollections, config.Restore.NumParallelCollections)
	}

	if expectedRestoreOptions.MaxDownloadBufferMb != config.Restore.MaxDownloadBufferMb {
		t.Errorf("expected %d, got %d", expectedRestoreOptions.MaxDownloadBufferMb, config.Restore.MaxDownloadBufferMb)
	}
	if expectedRestoreOptions.DownloadChunkMb != config.Restore.DownloadChunkMb {
		t.Errorf("expected %d, got %d", expectedRestoreOptions.DownloadChunkMb, config.Restore.DownloadChunkMb)
	}
	if expectedRestoreOptions.MongodLocation != config.Restore.MongodLocation {
		t.Errorf("expected %s, got %s", expectedRestoreOptions.MongodLocation, config.Restore.MongodLocation)
	}
	if len(expectedRestoreOptions.MongodLocationMap) != len(config.Restore.MongodLocationMap) {
		t.Errorf("expected %d, got %d", len(expectedRestoreOptions.MongodLocationMap), len(config.Restore.MongodLocationMap))
	}
	if expectedRestoreOptions.MongodLocationMap["node01:1017"] != config.Restore.MongodLocationMap["node01:1017"] {
		t.Errorf("expected %s, got %s", expectedRestoreOptions.MongodLocationMap["node01:1017"], config.Restore.MongodLocationMap["node01:1017"])
	}
}

func TestPBMStorageConfig(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cr",
			Namespace: "test-namespace",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
		},
		Status: api.PerconaServerMongoDBStatus{
			BackupVersion: pbmVersion.Current().Version,
		},
	}

	tests := map[string]struct {
		secrets  []client.Object
		stg      api.BackupStorageSpec
		expected config.StorageConf
	}{
		"s3": {
			[]client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"AWS_ACCESS_KEY_ID":     []byte("some-access-key"),
						"AWS_SECRET_ACCESS_KEY": []byte("some-secret-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					Region:                "us-east-1",
					CredentialsSecret:     "test-secret",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
				},
			},
			config.StorageConf{
				Type: storage.S3,
				S3: &s3.Config{
					Region:                "us-east-1",
					EndpointURL:           "",
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
					Credentials: s3.Credentials{
						AccessKeyID:     "some-access-key",
						SecretAccessKey: "some-secret-key",
					},
				},
			},
		},
		"s3 without credentials": {
			[]client.Object{},
			api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					Region:                "us-east-1",
					CredentialsSecret:     "",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
				},
			},
			config.StorageConf{
				Type: storage.S3,
				S3: &s3.Config{
					Region:                "us-east-1",
					EndpointURL:           "",
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
					Credentials: s3.Credentials{
						AccessKeyID:     "",
						SecretAccessKey: "",
					},
				},
			},
		},
		"s3 with SSE with KMS": {
			[]client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"AWS_ACCESS_KEY_ID":     []byte("some-access-key"),
						"AWS_SECRET_ACCESS_KEY": []byte("some-secret-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					Region:                "us-east-1",
					CredentialsSecret:     "test-secret",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
					ServerSideEncryption: api.S3ServiceSideEncryption{
						SSEAlgorithm: "sse-algorithm",
						KMSKeyID:     "some-key-id",
					},
				},
			},
			config.StorageConf{
				Type: storage.S3,
				S3: &s3.Config{
					Region:                "us-east-1",
					EndpointURL:           "",
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
					Credentials: s3.Credentials{
						AccessKeyID:     "some-access-key",
						SecretAccessKey: "some-secret-key",
					},
					ServerSideEncryption: &s3.AWSsse{
						SseAlgorithm: "sse-algorithm",
						KmsKeyID:     "some-key-id",
					},
				},
			},
		},
		"s3 with SSE with custom key": {
			[]client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"AWS_ACCESS_KEY_ID":     []byte("some-access-key"),
						"AWS_SECRET_ACCESS_KEY": []byte("some-secret-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					Region:                "us-east-1",
					CredentialsSecret:     "test-secret",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
					ServerSideEncryption: api.S3ServiceSideEncryption{
						SSECustomerAlgorithm: "sse-customer-algorithm",
						SSECustomerKey:       "some-customer-key",
					},
				},
			},
			config.StorageConf{
				Type: storage.S3,
				S3: &s3.Config{
					Region:                "us-east-1",
					EndpointURL:           "",
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
					Credentials: s3.Credentials{
						AccessKeyID:     "some-access-key",
						SecretAccessKey: "some-secret-key",
					},
					ServerSideEncryption: &s3.AWSsse{
						SseCustomerAlgorithm: "sse-customer-algorithm",
						SseCustomerKey:       "some-customer-key",
					},
				},
			},
		},
		"gcs": {
			[]client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"GCS_CLIENT_EMAIL": []byte("serviceaccount@google.com"),
						"GCS_PRIVATE_KEY":  []byte("some-private-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageGCS,
				GCS: api.BackupStorageGCSSpec{
					Bucket:            "operator-testing",
					Prefix:            "psmdb",
					CredentialsSecret: "test-secret",
					ChunkSize:         1024 * 1024 * 10,
				},
			},
			config.StorageConf{
				Type: storage.GCS,
				GCS: &gcs.Config{
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					ChunkSize: 1024 * 1024 * 10,
					Credentials: gcs.Credentials{
						ClientEmail: "serviceaccount@google.com",
						PrivateKey:  "some-private-key",
					},
				},
			},
		},
		"gcs s3 compatibility": {
			[]client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"AWS_ACCESS_KEY_ID":     []byte("some-access-key"),
						"AWS_SECRET_ACCESS_KEY": []byte("some-secret-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					Bucket:                "operator-testing",
					Prefix:                "psmdb",
					Region:                "us-east-1",
					EndpointURL:           "https://storage.googleapis.com",
					CredentialsSecret:     "test-secret",
					UploadPartSize:        1024 * 1024 * 10,
					MaxUploadParts:        5000,
					StorageClass:          "storage-class",
					InsecureSkipTLSVerify: false,
				},
			},
			config.StorageConf{
				Type: storage.GCS,
				GCS: &gcs.Config{
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					ChunkSize: 1024 * 1024 * 10,
					Credentials: gcs.Credentials{
						HMACAccessKey: "some-access-key",
						HMACSecret:    "some-secret-key",
					},
				},
			},
		},
		"azure": {
			[]client.Object{
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-secret",
						Namespace: "test-namespace",
					},
					Data: map[string][]byte{
						"AZURE_STORAGE_ACCOUNT_NAME": []byte("some-storage-account"),
						"AZURE_STORAGE_ACCOUNT_KEY":  []byte("some-storage-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageAzure,
				Azure: api.BackupStorageAzureSpec{
					Container:         "some-container",
					Prefix:            "psmdb",
					CredentialsSecret: "test-secret",
				},
			},
			config.StorageConf{
				Type: storage.Azure,
				Azure: &azure.Config{
					Account:   "some-storage-account",
					Container: "some-container",
					Prefix:    "psmdb",
					Credentials: azure.Credentials{
						Key: "some-storage-key",
					},
				},
			},
		},
		"filesystem": {
			[]client.Object{},
			api.BackupStorageSpec{
				Type: api.BackupStorageFilesystem,
				Filesystem: api.BackupStorageFilesystemSpec{
					Path: "/mnt/backups",
				},
			},
			config.StorageConf{
				Type: storage.Filesystem,
				Filesystem: &fs.Config{
					Path: "/mnt/backups",
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cl := buildFakeClient(t, tt.secrets...)
			got, err := GetPBMStorageConfig(context.Background(), cl, cr, tt.stg)
			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func buildFakeClient(t *testing.T, objs ...client.Object) client.WithWatch {
	t.Helper()

	s := scheme.Scheme
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatal(err, "failed to add client-go scheme")
	}

	return fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).Build()
}

func expectedCR(t *testing.T) *api.PerconaServerMongoDB {
	t.Helper()

	data, err := os.ReadFile("testdata/cr-with-pbm-config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cr := new(api.PerconaServerMongoDB)

	if err := yaml.Unmarshal(data, cr); err != nil {
		t.Fatal(err)
	}

	return cr
}
