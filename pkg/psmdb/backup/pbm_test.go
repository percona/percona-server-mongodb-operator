package backup

import (
	"context"
	"net/url"
	"os"
	"testing"
	"time"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint
	"sigs.k8s.io/yaml"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/gcs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/mio"
	"github.com/percona/percona-backup-mongodb/pbm/storage/oci"
	"github.com/percona/percona-backup-mongodb/pbm/storage/oss"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	pbmVersion "github.com/percona/percona-backup-mongodb/pbm/version"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbtls "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestGetMongoURIClientCertificate(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		Name: "pbm-uri-test", Namespace: "pbm-uri-test",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			Secrets:   &api.SecretsSpec{SSL: "server-tls"},
			TLS:       &api.TLSSpec{AllowInvalidCertificates: new(false)},
		},
	}
	_, clientCert, clientKey, err := psmdbtls.Issue(nil)
	require.NoError(t, err)

	users := &corev1.Secret{
		Name: api.UserSecretName(cr), Namespace: cr.Namespace,
		Data: map[string][]byte{
			"MONGODB_BACKUP_USER":     []byte("backup"),
			"MONGODB_BACKUP_PASSWORD": []byte("password"),
		},
	}
	server := &corev1.Secret{
		Name: cr.Spec.Secrets.SSL, Namespace: cr.Namespace,
		Data: map[string][]byte{
			"tls.key": []byte("server-key\n"),
			"tls.crt": []byte("server-cert\n"),
			"ca.crt":  []byte("server-ca\n"),
		},
	}
	clientCertName := psmdbtls.CertificatePBMClient(cr).Name()
	clientTLS := &corev1.Secret{
		Name:        clientCertName,
		Namespace:   cr.Namespace,
		Annotations: map[string]string{cm.CertificateNameKey: clientCertName},
		Data:        map[string][]byte{"tls.key": clientKey, "tls.crt": clientCert},
	}

	readTLSFiles := func(t *testing.T, secrets ...client.Object) ([]byte, []byte, error) {
		t.Helper()
		objects := []client.Object{users, server}
		objects = append(objects, secrets...)
		uri, err := getMongoUri(t.Context(), buildFakeClient(t, objects...), cr, []string{"localhost:27017"}, true)
		if err != nil {
			return nil, nil, err
		}
		u, err := url.Parse(uri)
		require.NoError(t, err)
		tlsFile := u.Query().Get("tlsCertificateKeyFile")
		caFile := u.Query().Get("tlsCAFile")
		t.Cleanup(func() {
			_ = os.Remove(tlsFile)
			_ = os.Remove(caFile)
		})
		pem, err := os.ReadFile(tlsFile)
		require.NoError(t, err)
		ca, err := os.ReadFile(caFile)
		require.NoError(t, err)
		return pem, ca, nil
	}

	t.Run("missing client certificate fails", func(t *testing.T) {
		_, _, err := readTLSFiles(t)
		require.ErrorContains(t, err, "get PBM client TLS secret")
	})
	t.Run("issued client certificate uses dedicated identity", func(t *testing.T) {
		pem, ca, err := readTLSFiles(t, clientTLS)
		require.NoError(t, err)
		require.Equal(t, append(append([]byte(nil), clientKey...), clientCert...), pem)
		require.Equal(t, []byte("server-ca\n"), ca)
	})
	t.Run("custom client secret name", func(t *testing.T) {
		cr.Spec.Secrets.SSLPBM = "custom-pbm-tls"
		t.Cleanup(func() { cr.Spec.Secrets.SSLPBM = "" })
		custom := clientTLS.DeepCopy()
		custom.Name = cr.Spec.Secrets.SSLPBM
		pem, ca, err := readTLSFiles(t, custom)
		require.NoError(t, err)
		require.Equal(t, append(append([]byte(nil), clientKey...), clientCert...), pem)
		require.Equal(t, []byte("server-ca\n"), ca)
	})
	t.Run("rotated client certificate refreshes local identity", func(t *testing.T) {
		_, _, err := readTLSFiles(t, clientTLS)
		require.NoError(t, err)
		_, cert, key, err := psmdbtls.Issue(nil)
		require.NoError(t, err)
		rotated := clientTLS.DeepCopy()
		rotated.Data = map[string][]byte{"tls.key": key, "tls.crt": cert}
		pem, ca, err := readTLSFiles(t, rotated)
		require.NoError(t, err)
		require.Equal(t, append(append([]byte(nil), key...), cert...), pem)
		require.Equal(t, []byte("server-ca\n"), ca)
	})
	t.Run("client certificate without metadata uses dedicated identity", func(t *testing.T) {
		unannotated := clientTLS.DeepCopy()
		unannotated.Annotations = nil
		pem, ca, err := readTLSFiles(t, unannotated)
		require.NoError(t, err)
		require.Equal(t, append(append([]byte(nil), clientKey...), clientCert...), pem)
		require.Equal(t, []byte("server-ca\n"), ca)
	})
	t.Run("manually issued client certificate uses dedicated identity", func(t *testing.T) {
		manual := clientTLS.DeepCopy()
		manual.Annotations = nil
		cr.UID = "cluster-uid"
		manual.OwnerReferences = []metav1.OwnerReference{{APIVersion: api.SchemeGroupVersion.String(), Kind: "PerconaServerMongoDB", Name: cr.Name, UID: cr.UID, Controller: new(true)}}
		pem, ca, err := readTLSFiles(t, manual)
		require.NoError(t, err)
		require.Equal(t, append(append([]byte(nil), clientKey...), clientCert...), pem)
		require.Equal(t, []byte("server-ca\n"), ca)
	})
}

func TestPBMConfigPriority(t *testing.T) {
	secret := &corev1.Secret{
		APIVersion: "/v1",
		Kind:       "Secret",
		Name:       "test-secret",
		Namespace:  "test-namespace",
	}

	t.Run("not configured", func(t *testing.T) {
		cr := expectedCR(t)
		cr.Spec.CRVersion = version.Version()
		cr.Status.BackupVersion = pbmVersion.Current().Version
		cr.Spec.Backup.Configuration.BackupOptions.Priority = nil

		cli := buildFakeClient(t, secret)

		cfg, err := GetPBMConfig(context.Background(), cli, cr, cr.Spec.Backup.Storages["test-s3-storage"])
		require.NoError(t, err)
		require.Empty(t, cfg.Backup.Priority)
	})

	t.Run("legacy cr version", func(t *testing.T) {
		cr := expectedCR(t)
		cr.Spec.CRVersion = "1.23.0"
		cr.Status.BackupVersion = pbmVersion.Current().Version
		cr.Spec.Backup.Configuration.BackupOptions.Priority = nil

		cli := buildFakeClient(t, secret)

		// operator tries to calculate priorities, which requires users secret
		_, err := GetPBMConfig(context.Background(), cli, cr, cr.Spec.Backup.Storages["test-s3-storage"])
		require.ErrorContains(t, err, "get priorities")
	})

	t.Run("user defined", func(t *testing.T) {
		cr := expectedCR(t)
		cr.Spec.CRVersion = version.Version()
		cr.Status.BackupVersion = pbmVersion.Current().Version

		cli := buildFakeClient(t, secret)

		cfg, err := GetPBMConfig(context.Background(), cli, cr, cr.Spec.Backup.Storages["test-s3-storage"])
		require.NoError(t, err)
		require.Equal(t, config.Priority{"localhost:28019": 2.5}, cfg.Backup.Priority)
	})
}

func TestApplyCustomPBMConfig(t *testing.T) {
	cr := expectedCR(t)
	cr.Status.BackupVersion = pbmVersion.Current().Version

	storage := cr.Spec.Backup.Storages["test-s3-storage"]

	secret := &corev1.Secret{
		APIVersion: "/v1",
		Kind:       "Secret",
		Name:       "test-secret",
		Namespace:  "test-namespace",
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
		Name:      "test-cr",
		Namespace: "test-namespace",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
		},
		Status: api.PerconaServerMongoDBStatus{
			BackupVersion: MinPBMVersionOSS,
		},
	}

	tests := map[string]struct {
		secrets  []client.Object
		stg      api.BackupStorageSpec
		expected config.StorageConf
	}{
		"minio": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						"AWS_ACCESS_KEY_ID":     []byte("some-access-key"),
						"AWS_SECRET_ACCESS_KEY": []byte("some-secret-key"),
						"AWS_SESSION_TOKEN":     []byte("some-session-token"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageMinio,
				Minio: api.BackupStorageMinioSpec{
					Bucket:            "operator-testing",
					Prefix:            "psmdb",
					Region:            "us-east-1",
					EndpointURL:       "https://minio.example.com",
					CredentialsSecret: "test-secret",
				},
			},
			config.StorageConf{
				Type: storage.Minio,
				Minio: &mio.Config{
					Region:   "us-east-1",
					Endpoint: "https://minio.example.com",
					Bucket:   "operator-testing",
					Prefix:   "psmdb",
					Credentials: mio.Credentials{
						AccessKeyID:     "some-access-key",
						SecretAccessKey: "some-secret-key",
						SessionToken:    "some-session-token",
					},
				},
			},
		},
		"s3": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						"AWS_ACCESS_KEY_ID":     []byte("some-access-key"),
						"AWS_SECRET_ACCESS_KEY": []byte("some-secret-key"),
						"AWS_SESSION_TOKEN":     []byte("some-session-token"),
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
						SessionToken:    "some-session-token",
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
					Name:      "test-secret",
					Namespace: "test-namespace",
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
					Name:      "test-secret",
					Namespace: "test-namespace",
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
					Name:      "test-secret",
					Namespace: "test-namespace",
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
		"gcs without credentials (ADC / Workload Identity)": {
			[]client.Object{},
			api.BackupStorageSpec{
				Type: api.BackupStorageGCS,
				GCS: api.BackupStorageGCSSpec{
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					ChunkSize: 1024 * 1024 * 10,
				},
			},
			config.StorageConf{
				Type: storage.GCS,
				GCS: &gcs.Config{
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					ChunkSize: 1024 * 1024 * 10,
					Credentials: gcs.Credentials{
						WorkloadIdentity: true,
					},
				},
			},
		},
		"oss s3 compatibility": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						"AWS_ACCESS_KEY_ID":     []byte("some-access-key"),
						"AWS_SECRET_ACCESS_KEY": []byte("some-secret-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageS3,
				S3: api.BackupStorageS3Spec{
					Bucket:            "operator-testing",
					Prefix:            "psmdb",
					Region:            "oss-eu-central-1",
					EndpointURL:       "https://s3.oss-eu-central-1.aliyuncs.com",
					CredentialsSecret: "test-secret",
					UploadPartSize:    1024 * 1024 * 10,
					MaxUploadParts:    5000,
				},
			},
			config.StorageConf{
				Type: storage.OSS,
				OSS: &oss.Config{
					Region:         "oss-eu-central-1",
					EndpointURL:    "https://s3.oss-eu-central-1.aliyuncs.com",
					Bucket:         "operator-testing",
					Prefix:         "psmdb",
					UploadPartSize: 1024 * 1024 * 10,
					MaxUploadParts: 5000,
					Credentials: oss.Credentials{
						AccessKeyID:     "some-access-key",
						AccessKeySecret: "some-secret-key",
					},
				},
			},
		},
		"azure": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
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
		"azure-connection-string": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						// AZURE_STORAGE_CONNECTION_STRING takes precedence over individual keys
						"AZURE_STORAGE_CONNECTION_STRING": []byte("DefaultEndpointsProtocol=https;AccountName=some-storage-account;AccountKey=some-storage-key==;EndpointSuffix=core.windows.net"),
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
						Key: "some-storage-key==",
					},
				},
			},
		},
		"oss": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						"ALIBABA_ACCESS_KEY_ID":     []byte("some-access-key"),
						"ALIBABA_ACCESS_KEY_SECRET": []byte("some-secret-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageOSS,
				OSS: api.BackupStorageOSSSpec{
					Bucket:            "operator-testing",
					Prefix:            "psmdb",
					Region:            "oss-eu-central-1",
					EndpointURL:       "https://oss-eu-central-1.aliyuncs.com",
					CredentialsSecret: "test-secret",
					ConnectTimeout: metav1.Duration{
						Duration: 5 * time.Second,
					},
					UploadPartSize: 1024 * 1024 * 10,
					MaxUploadParts: 5000,
					Retryer: &api.OSSRetryer{
						MaxAttempts: 7,
						MaxBackoff: metav1.Duration{
							Duration: 10 * time.Second,
						},
						BaseDelay: metav1.Duration{
							Duration: 100 * time.Millisecond,
						},
					},
					ServerSideEncryption: api.OSSServerSideEncryption{
						EncryptionMethod:    "sse",
						EncryptionAlgorithm: "KMS",
						EncryptionKeyID:     "some-key-id",
					},
				},
			},
			config.StorageConf{
				Type: storage.OSS,
				OSS: &oss.Config{
					Region:         "oss-eu-central-1",
					EndpointURL:    "https://oss-eu-central-1.aliyuncs.com",
					Bucket:         "operator-testing",
					Prefix:         "psmdb",
					ConnectTimeout: 5 * time.Second,
					UploadPartSize: 1024 * 1024 * 10,
					MaxUploadParts: 5000,
					Credentials: oss.Credentials{
						AccessKeyID:     "some-access-key",
						AccessKeySecret: "some-secret-key",
					},
					Retryer: &oss.Retryer{
						MaxAttempts: 7,
						MaxBackoff:  10 * time.Second,
						BaseDelay:   100 * time.Millisecond,
					},
					ServerSideEncryption: &oss.SSE{
						EncryptionMethod:    "sse",
						EncryptionAlgorithm: "KMS",
						EncryptionKeyID:     "some-key-id",
					},
				},
			},
		},
		"oci with user principal credentials": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						"OCI_TENANCY":                []byte("some-tenancy"),
						"OCI_USER":                   []byte("some-user"),
						"OCI_FINGERPRINT":            []byte("some-fingerprint"),
						"OCI_PRIVATE_KEY":            []byte("some-private-key"),
						"OCI_PRIVATE_KEY_PASSPHRASE": []byte("some-passphrase"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageOCI,
				OCI: api.BackupStorageOCISpec{
					Region:    "us-ashburn-1",
					Namespace: "some-namespace",
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					Credentials: api.OCICredentialsSpec{
						Type:       api.AuthTypeUserPrincipal,
						SecretName: "test-secret",
					},
					UploadPartSize:    1024 * 1024 * 10,
					UploadConcurrency: 5,
				},
			},
			config.StorageConf{
				Type: storage.OCI,
				OCI: &oci.Config{
					Region:    "us-ashburn-1",
					Namespace: "some-namespace",
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					Credentials: oci.Credentials{
						Type: oci.AuthTypeUserPrincipal,
						UserPrincipal: &oci.UserPrincipalCredentials{
							Tenancy:              "some-tenancy",
							User:                 "some-user",
							Fingerprint:          "some-fingerprint",
							PrivateKey:           "some-private-key",
							PrivateKeyPassphrase: "some-passphrase",
						},
					},
					UploadPartSize:    1024 * 1024 * 10,
					UploadConcurrency: 5,
				},
			},
		},
		"oci with user principal credentials without passphrase": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						"OCI_TENANCY":     []byte("some-tenancy"),
						"OCI_USER":        []byte("some-user"),
						"OCI_FINGERPRINT": []byte("some-fingerprint"),
						"OCI_PRIVATE_KEY": []byte("some-private-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageOCI,
				OCI: api.BackupStorageOCISpec{
					Region:    "us-ashburn-1",
					Namespace: "some-namespace",
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					Credentials: api.OCICredentialsSpec{
						Type:       api.AuthTypeUserPrincipal,
						SecretName: "test-secret",
					},
				},
			},
			config.StorageConf{
				Type: storage.OCI,
				OCI: &oci.Config{
					Region:    "us-ashburn-1",
					Namespace: "some-namespace",
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					Credentials: oci.Credentials{
						Type: oci.AuthTypeUserPrincipal,
						UserPrincipal: &oci.UserPrincipalCredentials{
							Tenancy:     "some-tenancy",
							User:        "some-user",
							Fingerprint: "some-fingerprint",
							PrivateKey:  "some-private-key",
						},
					},
				},
			},
		},
		"oci with instance principal credentials": {
			[]client.Object{},
			api.BackupStorageSpec{
				Type: api.BackupStorageOCI,
				OCI: api.BackupStorageOCISpec{
					Region:    "us-ashburn-1",
					Namespace: "some-namespace",
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					Credentials: api.OCICredentialsSpec{
						Type: api.AuthTypeInstancePrincipal,
					},
				},
			},
			config.StorageConf{
				Type: storage.OCI,
				OCI: &oci.Config{
					Region:    "us-ashburn-1",
					Namespace: "some-namespace",
					Bucket:    "operator-testing",
					Prefix:    "psmdb",
					Credentials: oci.Credentials{
						Type: oci.AuthTypeInstancePrincipal,
					},
				},
			},
		},
		"oci with retryer": {
			[]client.Object{},
			api.BackupStorageSpec{
				Type: api.BackupStorageOCI,
				OCI: api.BackupStorageOCISpec{
					Region: "us-ashburn-1",
					Bucket: "operator-testing",
					Prefix: "psmdb",
					Credentials: api.OCICredentialsSpec{
						Type: api.AuthTypeOkeWorkloadIdentity,
					},
					Retryer: &api.OCIRetryerSpec{
						MaxAttempts: 10,
						MaxBackoff:  30 * time.Second,
					},
				},
			},
			config.StorageConf{
				Type: storage.OCI,
				OCI: &oci.Config{
					Region: "us-ashburn-1",
					Bucket: "operator-testing",
					Prefix: "psmdb",
					Credentials: oci.Credentials{
						Type: oci.AuthTypeOkeWorkloadIdentity,
					},
					Retryer: &oci.Retryer{
						MaxAttempts: 10,
						MaxBackoff:  30 * time.Second,
					},
				},
			},
		},
		"oci with SSE KMS key": {
			[]client.Object{},
			api.BackupStorageSpec{
				Type: api.BackupStorageOCI,
				OCI: api.BackupStorageOCISpec{
					Region: "us-ashburn-1",
					Bucket: "operator-testing",
					Prefix: "psmdb",
					Credentials: api.OCICredentialsSpec{
						Type: api.AuthTypeInstancePrincipal,
					},
					ServerSideEncryption: api.OCIServerSideEncryptionSpec{
						KmsKeyID: "some-kms-key-id",
					},
				},
			},
			config.StorageConf{
				Type: storage.OCI,
				OCI: &oci.Config{
					Region: "us-ashburn-1",
					Bucket: "operator-testing",
					Prefix: "psmdb",
					Credentials: oci.Credentials{
						Type: oci.AuthTypeInstancePrincipal,
					},
					ServerSideEncryption: oci.SSE{
						KmsKeyID: "some-kms-key-id",
					},
				},
			},
		},
		"oci with SSE customer key": {
			[]client.Object{
				&corev1.Secret{
					Name:      "test-sse-secret",
					Namespace: "test-namespace",
					Data: map[string][]byte{
						"OCI_SSE_CUSTOMER_KEY": []byte("some-customer-key"),
					},
				},
			},
			api.BackupStorageSpec{
				Type: api.BackupStorageOCI,
				OCI: api.BackupStorageOCISpec{
					Region: "us-ashburn-1",
					Bucket: "operator-testing",
					Prefix: "psmdb",
					Credentials: api.OCICredentialsSpec{
						Type: api.AuthTypeInstancePrincipal,
					},
					ServerSideEncryption: api.OCIServerSideEncryptionSpec{
						SecretName: "test-sse-secret",
					},
				},
			},
			config.StorageConf{
				Type: storage.OCI,
				OCI: &oci.Config{
					Region: "us-ashburn-1",
					Bucket: "operator-testing",
					Prefix: "psmdb",
					Credentials: oci.Credentials{
						Type: oci.AuthTypeInstancePrincipal,
					},
					ServerSideEncryption: oci.SSE{
						SseCustomerKey: "some-customer-key",
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

func TestPBMStorageOSSUsesSSESecret(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		Name:      "test-cr",
		Namespace: "test-namespace",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
		},
		Status: api.PerconaServerMongoDBStatus{
			BackupVersion: MinPBMVersionOSS,
		},
	}
	cl := buildFakeClient(
		t,
		&corev1.Secret{
			Name:      "test-secret",
			Namespace: "test-namespace",
			Data: map[string][]byte{
				"ALIBABA_ACCESS_KEY_ID":     []byte("some-access-key"),
				"ALIBABA_ACCESS_KEY_SECRET": []byte("some-secret-key"),
			},
		},
		&corev1.Secret{
			Name:      "oss-sse-secret",
			Namespace: "test-namespace",
			Data: map[string][]byte{
				SSECustomerKey: []byte("oss-key-id"),
			},
		},
	)

	got, err := GetPBMStorageConfig(context.Background(), cl, cr, api.BackupStorageSpec{
		Type: api.BackupStorageOSS,
		OSS: api.BackupStorageOSSSpec{
			Bucket:            "operator-testing",
			Region:            "oss-eu-central-1",
			EndpointURL:       "https://oss-eu-central-1.aliyuncs.com",
			CredentialsSecret: "test-secret",
			ServerSideEncryption: api.OSSServerSideEncryption{
				SecretName:          "oss-sse-secret",
				EncryptionMethod:    "sse",
				EncryptionAlgorithm: "KMS",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, got.OSS.ServerSideEncryption)
	require.Equal(t, storage.MaskedString("oss-key-id"), got.OSS.ServerSideEncryption.EncryptionKeyID)
}

func TestPBMStorageOSSRequiresSupportedPBMVersion(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		Name:      "test-cr",
		Namespace: "test-namespace",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
		},
		Status: api.PerconaServerMongoDBStatus{
			BackupVersion: "2.11.0",
		},
	}
	secret := &corev1.Secret{
		Name:      "test-secret",
		Namespace: "test-namespace",
		Data: map[string][]byte{
			"ALIBABA_ACCESS_KEY_ID":     []byte("some-access-key"),
			"ALIBABA_ACCESS_KEY_SECRET": []byte("some-secret-key"),
		},
	}
	cl := buildFakeClient(t, secret)

	_, err := GetPBMStorageConfig(context.Background(), cl, cr, api.BackupStorageSpec{
		Type: api.BackupStorageOSS,
		OSS: api.BackupStorageOSSSpec{
			Bucket:            "operator-testing",
			CredentialsSecret: "test-secret",
		},
	})

	require.EqualError(t, err, "oss storage requires PBM 2.12.0 or newer")
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

func TestGetMongoUri(t *testing.T) {
	tests := map[string]struct {
		tlsEnabled   bool
		allowInvalid bool
		expectedURI  string
	}{
		"tls disabled": {
			tlsEnabled:  false,
			expectedURI: "mongodb://backup:backup%40pass@cluster-rs0-0.cluster-rs0.ns.svc.cluster.local:27017/",
		},
		"tls enabled": {
			tlsEnabled:   true,
			allowInvalid: false,
			expectedURI:  "mongodb://backup:backup%40pass@cluster-rs0-0.cluster-rs0.ns.svc.cluster.local:27017/?tls=true&tlsCertificateKeyFile=/tmp/ns-cluster-tls.pem&tlsCAFile=/tmp/ns-cluster-ca.crt",
		},
		"allow invalid certificates": {
			tlsEnabled:   true,
			allowInvalid: true,
			expectedURI:  "mongodb://backup:backup%40pass@cluster-rs0-0.cluster-rs0.ns.svc.cluster.local:27017/?tls=true&tlsCertificateKeyFile=/tmp/ns-cluster-tls.pem&tlsCAFile=/tmp/ns-cluster-ca.crt&tlsInsecure=true",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				Name:      "cluster",
				Namespace: "ns",
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Secrets:   &api.SecretsSpec{SSL: "cluster-ssl"},
					Replsets:  []*api.ReplsetSpec{{Name: "rs0"}},
					TLS:       &api.TLSSpec{AllowInvalidCertificates: &tt.allowInvalid},
				},
			}
			usersSecret := &corev1.Secret{
				Name:      api.UserSecretName(cr),
				Namespace: cr.Namespace,
				Data: map[string][]byte{
					"MONGODB_BACKUP_USER":     []byte("backup"),
					"MONGODB_BACKUP_PASSWORD": []byte("backup@pass"),
				},
			}
			sans := psmdbtls.GetCertificateSans(cr)
			caCert, tlsCert, tlsKey, err := psmdbtls.Issue(sans)
			require.NoError(t, err)

			sslSecret := &corev1.Secret{
				Name:      "cluster-ssl",
				Namespace: cr.Namespace,
				Data: map[string][]byte{
					"ca.crt":  caCert,
					"tls.crt": tlsCert,
					"tls.key": tlsKey,
				},
			}
			clientSecret := &corev1.Secret{
				Name:      psmdbtls.CertificatePBMClient(cr).SecretName(),
				Namespace: cr.Namespace,
				Data: map[string][]byte{
					"tls.crt": tlsCert,
					"tls.key": tlsKey,
				},
			}
			cl := buildFakeClient(t, usersSecret, sslSecret, clientSecret)
			t.Cleanup(func() {
				_ = os.Remove("/tmp/ns-cluster-tls.pem")
				_ = os.Remove("/tmp/ns-cluster-ca.crt")
			})

			uri, err := getMongoUri(t.Context(), cl, cr, []string{"cluster-rs0-0.cluster-rs0.ns.svc.cluster.local:27017"}, tt.tlsEnabled)
			require.NoError(t, err)
			require.Equal(t, tt.expectedURI, uri)
		})
	}
}
