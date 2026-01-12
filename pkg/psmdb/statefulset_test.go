package psmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestCollectStorageCABundles(t *testing.T) {
	t.Run("no storages", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: "1.22.0",
				Backup:    api.BackupSpec{},
			},
		}

		cas := collectStorageCABundles(cr)
		assert.Empty(t, cas)
	})

	t.Run("storage without CA bundle", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: "1.22.0",
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
		assert.Empty(t, cas)
	})

	t.Run("single CA bundle", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: "1.22.0",
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
				CRVersion: "1.22.0",
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
		assert.Equal(t, "ca.crt", cas[0].Key, "should default to ca.crt")
	})

	t.Run("deduplicate same CA", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: "1.22.0",
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
	})

	t.Run("different keys from same secret", func(t *testing.T) {
		cr := &api.PerconaServerMongoDB{
			Spec: api.PerconaServerMongoDBSpec{
				CRVersion: "1.22.0",
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
