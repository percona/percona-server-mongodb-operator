package v1alpha1

import (
	"fmt"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/percona/percona-server-mongodb-operator/version"
)

var (
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
	defaultBackupS3SecretName    = "percona-server-mongodb-backup-s3"
)

// CheckNSetDefaults sets default options, overwrites wrong settings
// and checks if other options' values valid
func (cr *PerconaServerMongoDB) CheckNSetDefaults(platform version.Platform, log logr.Logger) error {
	if cr.Spec.Image == "" {
		return fmt.Errorf("Required value for spec.image")
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
	}

	for _, replset := range cr.Spec.Replsets {
		if cr.Spec.Pause {
			replset.Size = 0
			replset.Arbiter.Enabled = false
			continue
		}
		err := replset.SetDefauts(cr.Spec.UnsafeConf, log)
		if err != nil {
			return err
		}
	}

	if cr.Spec.RunUID == 0 && platform != version.PlatformOpenshift {
		cr.Spec.RunUID = defaultRunUID
	}

	// there is shouldn't be any backups while pause
	if cr.Spec.Pause {
		cr.Spec.Backup.Enabled = false
	}

	if cr.Spec.Backup.Enabled {
		if cr.Spec.Backup.RestartOnFailure == nil {
			t := true
			cr.Spec.Backup.RestartOnFailure = &t
		}
		for _, bkpTask := range cr.Spec.Backup.Tasks {
			if bkpTask.CompressionType == "" {
				bkpTask.CompressionType = BackupCompressionGzip
			}
		}
		cr.Spec.Backup.Coordinator.MultiAZ.reconcileOpts()
	}

	cr.Status.Replsets = make(map[string]*ReplsetStatus)

	return nil
}

// SetDefauts set default options for the replset
func (rs *ReplsetSpec) SetDefauts(unsafe bool, log logr.Logger) error {
	if rs.VolumeSpec == nil {
		return fmt.Errorf("replset %s: volumeSpec should be specified", rs.Name)
	}

	err := rs.VolumeSpec.reconcileOpts()
	if err != nil {
		return fmt.Errorf("replset %s VolumeSpec: %v", rs.Name, err)
	}

	if rs.Expose.Enabled && rs.Expose.ExposeType == "" {
		rs.Expose.ExposeType = corev1.ServiceTypeClusterIP
	}

	rs.MultiAZ.reconcileOpts()

	if rs.Arbiter.Enabled {
		rs.Arbiter.MultiAZ.reconcileOpts()
	}

	if !unsafe {
		rs.setSafeDefauts(log)
	}

	return nil
}

func (rs *ReplsetSpec) setSafeDefauts(log logr.Logger) {
	loginfo := func(msg string, args ...interface{}) {
		log.Info(msg, args...)
		log.Info("Set allowUnsafeConfigurations=true to disable safe configuration")
	}

	// Replset size can't be 0 or 1.
	// But 2 + the Arbiter is possible.
	if rs.Size < 2 {
		loginfo(fmt.Sprintf("Replset size will be changed from %d to %d due to safe config", rs.Size, defaultMongodSize))
		rs.Size = defaultMongodSize
	}

	if rs.Arbiter.Enabled {
		if rs.Arbiter.Size != 1 {
			loginfo(fmt.Sprintf("Arbiter size will be changed from %d to 1 due to safe config", rs.Arbiter.Size))
			rs.Arbiter.Size = 1
		}
		if rs.Size%2 != 0 {
			loginfo(fmt.Sprintf("Arbiter will be switched off. There is no need in arbiter with odd replset size (%d)", rs.Size))
			rs.Arbiter.Enabled = false
			rs.Arbiter.Size = 0
		}
	} else {
		if rs.Size%2 == 0 {
			loginfo(fmt.Sprintf("Replset size will be increased from %d to %d", rs.Size, rs.Size+1))
			rs.Size++
		}
	}
}

func (m *MultiAZ) reconcileOpts() {
	m.reconcileAffinityOpts()

	if m.PodDisruptionBudget == nil {
		defaultMaxUnavailable := intstr.FromInt(1)
		m.PodDisruptionBudget = &PodDisruptionBudgetSpec{MaxUnavailable: &defaultMaxUnavailable}
	}
}

var affinityValidTopologyKeys = map[string]struct{}{
	AffinityOff:                                struct{}{},
	"kubernetes.io/hostname":                   struct{}{},
	"failure-domain.beta.kubernetes.io/zone":   struct{}{},
	"failure-domain.beta.kubernetes.io/region": struct{}{},
}

var defaultAffinityTopologyKey = "kubernetes.io/hostname"

const AffinityOff = "none"

// reconcileAffinityOpts ensures that the affinity is set to the valid values.
// - if the affinity doesn't set at all - set topology key to `defaultAffinityTopologyKey`
// - if topology key is set and the value not the one of `affinityValidTopologyKeys` - set to `defaultAffinityTopologyKey`
// - if topology key set to valuse of `AffinityOff` - disable the affinity at all
// - if `Advanced` affinity is set - leave everything as it is and set topology key to nil (Advanced options has a higher priority)
func (m *MultiAZ) reconcileAffinityOpts() {
	switch {
	case m.Affinity == nil:
		m.Affinity = &PodAffinity{
			TopologyKey: &defaultAffinityTopologyKey,
		}

	case m.Affinity.TopologyKey == nil:
		m.Affinity.TopologyKey = &defaultAffinityTopologyKey

	case m.Affinity.Advanced != nil:
		m.Affinity.TopologyKey = nil

	case m.Affinity != nil && m.Affinity.TopologyKey != nil:
		if _, ok := affinityValidTopologyKeys[*m.Affinity.TopologyKey]; !ok {
			m.Affinity.TopologyKey = &defaultAffinityTopologyKey
		}
	}
}

func (v *VolumeSpec) reconcileOpts() error {
	if v.PersistentVolumeClaim != nil {
		_, ok := v.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
		if !ok {
			return fmt.Errorf("volume.resources.storage can't be empty")
		}

		if v.PersistentVolumeClaim.AccessModes == nil || len(v.PersistentVolumeClaim.AccessModes) == 0 {
			v.PersistentVolumeClaim.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
		}
	}

	return nil
}
