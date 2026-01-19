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
		skip       bool
	}{
		{
			name: "storage type changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			expected: true,
		},
		{
			name: "s3: bucket changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing-1",
						Region: "us-east-1",
					},
				},
			},
			expected: true,
		},
		{
			name: "s3: region changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-2",
					},
				},
			},
			expected: true,
		},
		{
			name: "s3: endpointUrl changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:      "operator-testing",
						Region:      "us-east-1",
						EndpointURL: "https://s3.us-east-1.amazonaws.com",
					},
				},
			},
			expected: true,
			skip:     true, // TODO: remove this when we have PBM 2.13.0
		},
		{
			name: "s3: prefix changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:      "operator-testing",
						Region:      "us-east-1",
						EndpointURL: "https://s3.amazonaws.com",
					},
				},
			},
			newCfg: &config.Config{
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
			expected: true,
		},
		{
			name: "s3: maxUploadParts changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.S3,
					S3: &s3.Config{
						Bucket:      "operator-testing",
						Region:      "us-east-1",
						EndpointURL: "https://s3.amazonaws.com",
					},
				},
			},
			newCfg: &config.Config{
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
			expected: false,
		},
		{
			name: "gcs: bucket changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing-1",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing-2",
					},
				},
			},
			expected: true,
		},
		{
			name: "gcs: prefix changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.GCS,
					GCS: &gcs.Config{
						Bucket: "operator-testing",
						Prefix: "prefix",
					},
				},
			},
			expected: true,
		},
		{
			name: "azure: endpointUrl changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName-1.blob.core.windows.net",
					},
				},
			},
			expected: true,
			skip:     true, // TODO: remove this when we have PBM 2.13.0
		},
		{
			name: "azure: container changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing-1",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			expected: true,
		},
		{
			name: "azure: account changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Azure,
					Azure: &azure.Config{
						Account:     "operator-account-1",
						Container:   "operator-testing",
						EndpointURL: "https://accountName.blob.core.windows.net",
					},
				},
			},
			expected: true,
		},
		{
			name: "fs: path changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Filesystem,
					Filesystem: &fs.Config{
						Path: "/mnt/backups",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Filesystem,
					Filesystem: &fs.Config{
						Path: "/mnt/backups-1",
					},
				},
			},
			expected: true,
		},
		{
			name: "minio: bucket changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Endpoint: "operator-testing.com",
						Bucket:   "operator-testing",
						Region:   "us-east-1",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Endpoint: "operator-testing.com",
						Bucket:   "operator-testing-1",
						Region:   "us-east-1",
					},
				},
			},
			expected: true,
		},
		{
			name: "minio: region changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket: "operator-testing",
						Region: "us-east-1",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket: "operator-testing",
						Region: "us-east-2",
					},
				},
			},
			expected: true,
		},
		{
			name: "minio: endpoint changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:   "operator-testing",
						Region:   "us-east-1",
						Endpoint: "operator-testing.com",
					},
				},
			},
			newCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:   "operator-testing",
						Region:   "us-east-1",
						Endpoint: "operator-testing-1.com",
					},
				},
			},
			expected: true,
		},
		{
			name: "minio: prefix changed",
			currentCfg: &config.Config{
				Storage: config.StorageConf{
					Type: storage.Minio,
					Minio: &mio.Config{
						Bucket:   "operator-testing",
						Region:   "us-east-1",
						Endpoint: "operator-testing.com",
					},
				},
			},
			newCfg: &config.Config{
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
			expected: true,
		},
		{
			name: "minio: nothing changed",
			currentCfg: &config.Config{
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
			newCfg: &config.Config{
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
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skipf("skipping test %s", tt.name)
			}
			if got := isResyncNeeded(tt.currentCfg, tt.newCfg); got != tt.expected {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}
