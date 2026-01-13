package psmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestCollectStorageCABundles(t *testing.T) {
	t.Run("no storages", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: version.Version(),
				Backup:    api.BackupSpec{},
			},
		}

		cas := collectStorageCABundles(cr)
		assert.Nil(t, cas)
	})

	t.Run("storage without CA bundle", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: version.Version(),
				Backup: api.BackupSpec{
					Storages: map[string]api.BackupStorageSpec{
						"minio": {
							Type: api.BackupStorageMinio,
							Minio: api.BackupStorageMinioSpec{
								Bucket: "backups",
								// No CABundle
							},
						},
					},
				},
			},
		}

		cas := collectStorageCABundles(cr)
		assert.Nil(t, cas)
	})

	t.Run("single CA bundle", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
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
		}

		cas := collectStorageCABundles(cr)
		require.Len(t, cas, 1)
		assert.Equal(t, "minio-ca", cas[0].Name)
		assert.Equal(t, "ca.crt", cas[0].Key)
	})

	t.Run("default key to ca.crt", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
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
		}

		cas := collectStorageCABundles(cr)
		require.Len(t, cas, 1)
		assert.Equal(t, "minio-ca", cas[0].Name)
		assert.Equal(t, "ca.crt", cas[0].Key, "should default to ca.crt")
	})

	t.Run("deduplicate same CA", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
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
		}

		cas := collectStorageCABundles(cr)
		assert.Len(t, cas, 1, "should deduplicate same CA")
		assert.Equal(t, "shared-ca", cas[0].Name)
		assert.Equal(t, "ca.crt", cas[0].Key)

	})

	t.Run("different keys from same secret", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
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
		}

		cas := collectStorageCABundles(cr)
		assert.Len(t, cas, 2, "different keys should not be deduplicated")
	})
}

func TestGetCAVolumeMounts(t *testing.T) {
	mounts := getCAVolumeMounts()

	require.Len(t, mounts, 2)

	t.Run("input mount", func(t *testing.T) {
		mount := mounts[0]
		assert.Equal(t, naming.BackupStorageCAInputVolumeName, mount.Name)
		assert.Equal(t, "/etc/s3/certs-in", mount.MountPath)
		assert.True(t, mount.ReadOnly, "input mount should be read-only")
	})

	t.Run("output mount", func(t *testing.T) {
		mount := mounts[1]
		assert.Equal(t, naming.BackupStorageCAFileVolumeName, mount.Name)
		assert.Equal(t, "/etc/s3/certs", mount.MountPath)
		assert.False(t, mount.ReadOnly, "output mount should be read-write")
	})
}

func TestGetCAVolumes(t *testing.T) {
	t.Run("single CA", func(t *testing.T) {
		cas := []api.SecretKeySelector{
			{
				Name: "minio-ca",
				Key:  "ca.crt",
			},
		}

		volumes := getCAVolumes(cas)

		require.Len(t, volumes, 2)

		inputVol := volumes[0]
		assert.Equal(t, naming.BackupStorageCAInputVolumeName, inputVol.Name)
		require.NotNil(t, inputVol.Projected)
		require.Len(t, inputVol.Projected.Sources, 1)

		secretProj := inputVol.Projected.Sources[0].Secret
		require.NotNil(t, secretProj)
		assert.Equal(t, "minio-ca", secretProj.Name)
		require.Len(t, secretProj.Items, 1)
		assert.Equal(t, "ca.crt", secretProj.Items[0].Key)
		assert.Equal(t, "ca-0.crt", secretProj.Items[0].Path)

		outputVol := volumes[1]
		assert.Equal(t, naming.BackupStorageCAFileVolumeName, outputVol.Name)
		assert.NotNil(t, outputVol.EmptyDir)
	})

	t.Run("multiple CAs", func(t *testing.T) {
		cas := []api.SecretKeySelector{
			{
				Name: "minio-ca",
				Key:  "ca.crt",
			},
			{
				Name: "custom-ca",
				Key:  "my-ca.crt",
			},
		}

		volumes := getCAVolumes(cas)

		require.Len(t, volumes, 2)

		inputVol := volumes[0]
		require.NotNil(t, inputVol.Projected)
		require.Len(t, inputVol.Projected.Sources, 2)

		expectedMappings := []struct {
			name string
			key  string
			path string
		}{
			{"minio-ca", "ca.crt", "ca-0.crt"},
			{"custom-ca", "my-ca.crt", "ca-1.crt"},
		}

		for i, expected := range expectedMappings {
			secretProj := inputVol.Projected.Sources[i].Secret
			require.NotNil(t, secretProj, "source %d", i)
			assert.Equal(t, expected.name, secretProj.Name, "source %d name", i)
			require.Len(t, secretProj.Items, 1, "source %d items", i)
			assert.Equal(t, expected.key, secretProj.Items[0].Key, "source %d key", i)
			assert.Equal(t, expected.path, secretProj.Items[0].Path, "source %d path", i)
		}
	})
}
