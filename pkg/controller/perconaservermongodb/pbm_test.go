package perconaservermongodb

import (
	"testing"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isResyncNeeded(tt.currentCfg, tt.newCfg); got != tt.expected {
				t.Errorf("%s: got %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}
