package gcs

import (
	"context"
	"fmt"
	"os"
	"testing"

	gcs "cloud.google.com/go/storage"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/go-connections/nat"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/api/option"

	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

func TestGCS(t *testing.T) {
	ctx := context.Background()

	req := testcontainers.ContainerRequest{
		Image:        "fsouza/fake-gcs-server",
		ExposedPorts: []string{"4443/tcp"},
		Cmd:          []string{"-public-host", "localhost:4443", "-scheme", "http", "-port", "4443"},
		WaitingFor:   wait.ForLog("server started at"),
		HostConfigModifier: func(hc *container.HostConfig) {
			hc.PortBindings = nat.PortMap{
				"4443/tcp": {{HostIP: "0.0.0.0", HostPort: "4443"}},
			}
		},
	}

	gcsContainer, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start GCS container: %s", err)
	}
	defer func() {
		if err = testcontainers.TerminateContainer(gcsContainer); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}
	}()

	host, err := gcsContainer.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get container host: %s", err)
	}
	port, err := gcsContainer.MappedPort(ctx, "4443")
	if err != nil {
		t.Fatalf("failed to get mapped port: %s", err)
	}
	endpoint := fmt.Sprintf("http://%s:%s", host, port.Port())
	fmt.Println(endpoint)

	_ = os.Setenv("STORAGE_EMULATOR_HOST", endpoint)
	defer func() {
		_ = os.Unsetenv("STORAGE_EMULATOR_HOST")
	}()

	gcsClient, err := gcs.NewClient(ctx, option.WithoutAuthentication())
	if err != nil {
		t.Fatalf("failed to create GCS client: %s", err)
	}
	defer func() {
		if err = gcsClient.Close(); err != nil {
			t.Fatalf("failed to close client: %s", err)
		}
	}()

	bucketName := "test-bucket"
	bucket := gcsClient.Bucket(bucketName)
	if err = bucket.Create(ctx, "fakeProject", nil); err != nil {
		t.Fatalf("failed to create bucket: %s", err)
	}

	chunkSize := 1024

	opts := &Config{
		Bucket:    bucketName,
		ChunkSize: chunkSize,
		Credentials: Credentials{
			ClientEmail: "email@example.com",
			PrivateKey:  "-----BEGIN PRIVATE KEY-----\nKey\n-----END PRIVATE KEY-----\n",
		},
		Retryer: &Retryer{
			BackoffInitial:    1,
			BackoffMax:        2,
			BackoffMultiplier: 1,
		},
	}

	stg, err := New(opts, "node", nil)
	if err != nil {
		t.Fatalf("failed to create gcs storage: %s", err)
	}

	storage.RunStorageBaseTests(t, stg, storage.GCS)
	storage.RunStorageAPITests(t, stg)
	storage.RunSplitMergeMWTests(t, stg)

	t.Run("with downloader", func(t *testing.T) {
		stg, err := NewWithDownloader(opts, "node", nil, 0, 0, 0)
		if err != nil {
			t.Fatalf("failed to create gcs storage: %s", err)
		}

		storage.RunStorageBaseTests(t, stg, storage.GCS)
		storage.RunStorageAPITests(t, stg)
		storage.RunSplitMergeMWTests(t, stg)
	})

	t.Run("Delete fails", func(t *testing.T) {
		name := "not_found.txt"
		err := stg.Delete(name)

		if err == nil {
			t.Errorf("expected error when deleting non-existing file, got nil")
		}
	})
}
