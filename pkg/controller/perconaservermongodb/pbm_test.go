package perconaservermongodb

import (
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/gcs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/mio"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
)

func TestIsResyncNeeded(t *testing.T) {
	tests := []struct {
		name       string
		currentCfg *config.Config
		newCfg     *config.Config
		expected   bool
	}{
		{
			"storage type changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			true,
		},
		{
			"s3: bucket changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing-1",
						Region: "us-east-1",
					},
				},
			},
			true,
		},
		{
			"s3: region changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-2",
					},
				},
			},
			true,
		},
		{
			"s3: endpointUrl changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:      "operator-testing",
						Region:      "us-east-1",
						EndpointURL: "https://s3.us-east-1.amazonaws.com",
					},
				},
			},
			true,
		},
		{
			"s3: prefix changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:      "operator-testing",
						Region:      "us-east-1",
						EndpointURL: "https://s3.amazonaws.com",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:      "operator-testing",
						Region:      "us-east-1",
						EndpointURL: "https://s3.amazonaws.com",
						Prefix:      "prefix",
					},
				},
			},
			true,
		},
		{
			"s3: maxUploadParts changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:      "operator-testing",
						Region:      "us-east-1",
						EndpointURL: "https://s3.amazonaws.com",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:         "operator-testing",
						Region:         "us-east-1",
						EndpointURL:    "https://s3.amazonaws.com",
						MaxUploadParts: 2000,
					},
				},
			},
			false,
		},
		{
			"gcs: bucket changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing-1",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing-2",
					},
				},
			},
			true,
		},
		{
			"gcs: prefix changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing",
						Prefix: "prefix",
					},
				},
			},
			true,
		},
		{
			"azure: endpointUrl changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName-1.blob.core.windows.net",
					},
				},
			},
			true,
		},
		{
			"azure: container changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing-1",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			true,
		},
		{
			"azure: account changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account-1",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			true,
		},
		{
			"fs: path changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Filesystem,
					Filesystem: &fs.Config{
						Path: "/mnt/backups",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Filesystem,
					Filesystem: &fs.Config{
						Path: "/mnt/backups-1",
					},
				},
			},
			true,
		},
		{
			"minio: bucket changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Endpoint: "operator-testing.com",
						Bucket:   "operator-testing",
						Region:   "us-east-1",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Endpoint: "operator-testing.com",
						Bucket:   "operator-testing-1",
						Region:   "us-east-1",
					},
				},
			},
			true,
		},
		{
			"minio: region changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket: "operator-testing",
						Region: "us-east-2",
					},
				},
			},
			true,
		},
		{
			"minio: endpoint changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:   "operator-testing",
						Region:   "us-east-1",
						Endpoint: "operator-testing.com",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:   "operator-testing",
						Region:   "us-east-1",
						Endpoint: "operator-testing-1.com",
					},
				},
			},
			true,
		},
		{
			"minio: prefix changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:   "operator-testing",
						Region:   "us-east-1",
						Endpoint: "operator-testing.com",
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:   "operator-testing",
						Region:   "us-east-1",
						Endpoint: "operator-testing.com",
						Prefix:   "prefix",
					},
				},
			},
			true,
		},
		{
			"minio: nothing changed",
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:                "operator-testing",
						Region:                "us-east-1",
						Endpoint:              "operator-testing.com",
						Secure:                true,
						InsecureSkipTLSVerify: false,
					},
				},
			},
			&config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:                "operator-testing",
						Region:                "us-east-1",
						Endpoint:              "operator-testing.com",
						Secure:                true,
						InsecureSkipTLSVerify: false,
					},
				},
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isResyncNeeded(tt.currentCfg, tt.newCfg); got != tt.expected {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}
