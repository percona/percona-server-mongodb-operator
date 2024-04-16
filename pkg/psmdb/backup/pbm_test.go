package backup

import (
	"context"
	"os"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	// "k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestGetPBMConfig(t *testing.T) {
	cr := expectedCR(t)

	storage := cr.Spec.Backup.Storages["test-s3-storage"]

	cli := buildFakeClient(t)

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

func buildFakeClient(t *testing.T) client.WithWatch {
	t.Helper()

	s := scheme.Scheme
	if err := scheme.AddToScheme(s); err != nil {
		t.Fatal(err, "failed to add client-go scheme")
	}

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
	return fake.NewClientBuilder().WithScheme(s).WithObjects(secret).Build()
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
