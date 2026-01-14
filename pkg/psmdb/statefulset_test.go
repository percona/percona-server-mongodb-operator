package psmdb

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestCollectStorageCABundles(t *testing.T) {
	tests := []struct {
		name     string
		cr       *api.PerconaServerMongoDB
		expected []api.SecretKeySelector
	}{
		{
			name: "no storages",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup:    api.BackupSpec{},
				},
			},
			expected: nil,
		},
		{
			name: "storage without CA bundle",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									Bucket: "backups",
								},
							},
						},
					},
				},
			},
			expected: nil,
		},
		{
			name: "single CA bundle",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "minio-ca",
										},
										Key: "ca.crt",
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "minio-ca",
					Key:  "ca.crt",
				},
			},
		},
		{
			name: "default key to ca.crt",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "minio-ca",
										},
										// Key not specified
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "minio-ca",
					Key:  "ca.crt", // defaulted
				},
			},
		},
		{
			name: "deduplicate same CA",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio1": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "shared-ca",
										},
										Key: "ca.crt",
									},
								},
							},
							"minio2": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "shared-ca",
										},
										Key: "ca.crt",
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "shared-ca",
					Key:  "ca.crt",
				},
			},
		},
		{
			name: "different keys from same secret",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Backup: api.BackupSpec{
						Storages: map[string]api.BackupStorageSpec{
							"minio1": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "multi-ca",
										},
										Key: "ca1.crt",
									},
								},
							},
							"minio2": {
								Type: api.BackupStorageMinio,
								Minio: api.BackupStorageMinioSpec{
									CABundle: &corev1.SecretKeySelector{
										LocalObjectReference: corev1.LocalObjectReference{
											Name: "multi-ca",
										},
										Key: "ca2.crt",
									},
								},
							},
						},
					},
				},
			},
			expected: []api.SecretKeySelector{
				{
					Name: "multi-ca",
					Key:  "ca1.crt",
				},
				{
					Name: "multi-ca",
					Key:  "ca2.crt",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := collectStorageCABundles(tt.cr)

			if tt.expected == nil {
				assert.Nil(t, result)
				return
			}

			require.Len(t, result, len(tt.expected))

			for i, expected := range tt.expected {
				assert.Equal(t, expected.Name, result[i].Name)
				assert.Equal(t, expected.Key, result[i].Key)
			}
		})
	}
}

func TestGetCAVolumeMounts(t *testing.T) {
	mounts := getCAVolumeMounts()

	tests := []struct {
		name     string
		index    int
		wantName string
		wantPath string
		wantRO   bool
	}{
		{
			name:     "input mount",
			index:    0,
			wantName: naming.BackupStorageCAInputVolumeName,
			wantPath: "/etc/s3/certs-in",
			wantRO:   true,
		},
		{
			name:     "output mount",
			index:    1,
			wantName: naming.BackupStorageCAFileVolumeName,
			wantPath: "/etc/s3/certs",
			wantRO:   false,
		},
	}

	require.Len(t, mounts, len(tests))

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mount := mounts[tt.index]
			assert.Equal(t, tt.wantName, mount.Name)
			assert.Equal(t, tt.wantPath, mount.MountPath)
			assert.Equal(t, tt.wantRO, mount.ReadOnly)
		})
	}
}

func TestGetCAVolumes(t *testing.T) {
	tests := []struct {
		name string
		cas  []api.SecretKeySelector
	}{
		{
			name: "single CA",
			cas: []api.SecretKeySelector{
				{
					Name: "minio-ca",
					Key:  "ca.crt",
				},
			},
		},
		{
			name: "multiple CAs",
			cas: []api.SecretKeySelector{
				{Name: "minio-ca", Key: "ca.crt"},
				{Name: "s3-ca", Key: "root.crt"},
				{Name: "custom-ca", Key: "my-ca.crt"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			volumes := getCAVolumes(tt.cas)

			require.Len(t, volumes, 2)

			inputVol := volumes[0]
			assert.Equal(t, naming.BackupStorageCAInputVolumeName, inputVol.Name)
			require.NotNil(t, inputVol.Projected)
			require.Len(t, inputVol.Projected.Sources, len(tt.cas))

			for i, ca := range tt.cas {
				secretProj := inputVol.Projected.Sources[i].Secret
				require.NotNil(t, secretProj, "source %d", i)
				assert.Equal(t, ca.Name, secretProj.Name, "source %d name", i)
				require.Len(t, secretProj.Items, 1, "source %d items", i)
				assert.Equal(t, ca.Key, secretProj.Items[0].Key, "source %d key", i)
				assert.Equal(t, fmt.Sprintf("ca-%d.crt", i), secretProj.Items[0].Path, "source %d path", i)
			}

			outputVol := volumes[1]
			assert.Equal(t, naming.BackupStorageCAFileVolumeName, outputVol.Name)
			assert.NotNil(t, outputVol.EmptyDir)
		})
	}
}
