package v1alpha1

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/version"
	corev1 "k8s.io/api/core/v1"
)

var (
	defaultVersion                        = "3.6"
	defaultRunUID                   int64 = 1001
	defaultKeySecretName                  = "percona-server-mongodb-key"
	defaultUsersSecretName                = "percona-server-mongodb-users"
	defaultMongodSize               int32 = 3
	defaultReplsetName                    = "rs"
	defaultStorageEngine                  = StorageEngineWiredTiger
	defaultMongodPort               int32 = 27017
	defaultWiredTigerCacheSizeRatio       = 0.5
	defaultInMemorySizeRatio              = 0.9
	defaultOperationProfilingMode         = OperationProfilingModeSlowOp
	defaultImagePullPolicy                = corev1.PullAlways

	defaultBackupDestinationType = BackupDestinationS3
	defaultBackupVersion         = "0.2.0"
	defaultBackupS3SecretName    = "percona-server-mongodb-backup-s3"
)

// CheckNSetDefaults sets default options, overwrites wrong settings
// and checks if other options' values valid
func (cr *PerconaServerMongoDB) CheckNSetDefaults(platform version.Platform) error {
	if cr.Spec.Version == "" {
		cr.Spec.Version = defaultVersion
	}
	if cr.Spec.ImagePullPolicy == "" {
		cr.Spec.ImagePullPolicy = defaultImagePullPolicy
	}
	if cr.Spec.Secrets == nil {
		cr.Spec.Secrets = &SecretsSpec{}
	}
	if cr.Spec.Secrets.Users == "" {
		cr.Spec.Secrets.Users = defaultUsersSecretName
	}
	if cr.Spec.Mongod == nil {
		cr.Spec.Mongod = &MongodSpec{}
	}
	if cr.Spec.Mongod.Net == nil {
		cr.Spec.Mongod.Net = &MongodSpecNet{}
	}
	if cr.Spec.Mongod.Net.Port == 0 {
		cr.Spec.Mongod.Net.Port = defaultMongodPort
	}
	if cr.Spec.Mongod.Storage == nil {
		cr.Spec.Mongod.Storage = &MongodSpecStorage{}
	}
	if cr.Spec.Mongod.Storage.Engine == "" {
		cr.Spec.Mongod.Storage.Engine = defaultStorageEngine
	}

	switch cr.Spec.Mongod.Storage.Engine {
	case StorageEngineInMemory:
		if cr.Spec.Mongod.Storage.InMemory == nil {
			cr.Spec.Mongod.Storage.InMemory = &MongodSpecInMemory{}
		}
		if cr.Spec.Mongod.Storage.InMemory.EngineConfig == nil {
			cr.Spec.Mongod.Storage.InMemory.EngineConfig = &MongodSpecInMemoryEngineConfig{}
		}
		if cr.Spec.Mongod.Storage.InMemory.EngineConfig.InMemorySizeRatio == 0 {
			cr.Spec.Mongod.Storage.InMemory.EngineConfig.InMemorySizeRatio = defaultInMemorySizeRatio
		}
	case StorageEngineWiredTiger:
		if cr.Spec.Mongod.Storage.WiredTiger == nil {
			cr.Spec.Mongod.Storage.WiredTiger = &MongodSpecWiredTiger{}
		}
		if cr.Spec.Mongod.Storage.WiredTiger.CollectionConfig == nil {
			cr.Spec.Mongod.Storage.WiredTiger.CollectionConfig = &MongodSpecWiredTigerCollectionConfig{}
		}
		if cr.Spec.Mongod.Storage.WiredTiger.EngineConfig == nil {
			cr.Spec.Mongod.Storage.WiredTiger.EngineConfig = &MongodSpecWiredTigerEngineConfig{}
		}
		if cr.Spec.Mongod.Storage.WiredTiger.EngineConfig.CacheSizeRatio == 0 {
			cr.Spec.Mongod.Storage.WiredTiger.EngineConfig.CacheSizeRatio = defaultWiredTigerCacheSizeRatio
		}
		if cr.Spec.Mongod.Storage.WiredTiger.IndexConfig == nil {
			cr.Spec.Mongod.Storage.WiredTiger.IndexConfig = &MongodSpecWiredTigerIndexConfig{
				PrefixCompression: true,
			}
		}
	}
	if cr.Spec.Mongod.OperationProfiling == nil {
		cr.Spec.Mongod.OperationProfiling = &MongodSpecOperationProfiling{
			Mode: defaultOperationProfilingMode,
		}
	}
	if len(cr.Spec.Replsets) == 0 {
		cr.Spec.Replsets = []*ReplsetSpec{
			{
				Name: defaultReplsetName,
				Size: defaultMongodSize,
			},
		}
	} else {
		for _, replset := range cr.Spec.Replsets {
			replset.SetDefauts()
		}
	}
	if cr.Spec.RunUID == 0 && platform != version.PlatformOpenshift {
		cr.Spec.RunUID = defaultRunUID
	}

	if cr.Spec.Backup != nil && cr.Spec.Backup.Enabled {
		if cr.Spec.Backup.RestartOnFailure == nil {
			t := true
			cr.Spec.Backup.RestartOnFailure = &t
		}
		if cr.Spec.Backup.Version == "" {
			cr.Spec.Backup.Version = defaultBackupVersion
		}
		if cr.Spec.Backup.Coordinator == nil {
			cr.Spec.Backup.Coordinator = &BackupCoordinatorSpec{}
		}
		if cr.Spec.Backup.S3 == nil {
			cr.Spec.Backup.S3 = &BackupS3Spec{}
		}
		if cr.Spec.Backup.S3.Secret == "" {
			cr.Spec.Backup.S3.Secret = defaultBackupS3SecretName
		}
		for _, bkpTask := range cr.Spec.Backup.Tasks {
			if bkpTask.DestinationType == "" {
				bkpTask.DestinationType = defaultBackupDestinationType
			}
		}
	}

	cr.Status.Replsets = make(map[string]*ReplsetStatus)

	return nil
}

// SetDefauts set defaults options for the replset
func (rs *ReplsetSpec) SetDefauts() {
	if rs.Size == 0 {
		rs.Size = defaultMongodSize
	}
	if rs.Expose != nil && rs.Expose.Enabled && rs.Expose.ExposeType == "" {
		rs.Expose.ExposeType = corev1.ServiceTypeClusterIP
	}
}
