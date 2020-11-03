package v1

import (
	"fmt"
	"strconv"

	"github.com/go-logr/logr"
	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/percona/percona-server-mongodb-operator/version"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// DefaultDNSSuffix is a default dns suffix for the cluster service
const DefaultDNSSuffix = "svc.cluster.local"

// ConfigReplSetName is the only possible name for config replica set
const ConfigReplSetName = "cfg"

var (
	defaultRunUID                   int64 = 1001
	defaultUsersSecretName                = "percona-server-mongodb-users"
	defaultMongodSize               int32 = 3
	defaultReplsetName                    = "rs"
	defaultStorageEngine                  = StorageEngineWiredTiger
	defaultMongodPort               int32 = 27017
	defaultWiredTigerCacheSizeRatio       = 0.5
	defaultInMemorySizeRatio              = 0.9
	defaultOperationProfilingMode         = OperationProfilingModeSlowOp
	defaultImagePullPolicy                = corev1.PullAlways
)

// CheckNSetDefaults sets default options, overwrites wrong settings
// and checks if other options' values valid
func (cr *PerconaServerMongoDB) CheckNSetDefaults(platform version.Platform, log logr.Logger) error {
	err := cr.setVersion()
	if err != nil {
		return errors.Wrap(err, "set version")
	}

	if cr.Spec.Replsets == nil {
		return errors.New("at least one replica set should be specified")
	}

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
	if cr.Spec.Mongod.Security == nil {
		cr.Spec.Mongod.Security = &MongodSpecSecurity{}
	}
	if cr.Spec.Mongod.Security.EnableEncryption == nil {
		is120 := cr.CompareVersion("1.2.0") >= 0
		cr.Spec.Mongod.Security.EnableEncryption = &is120
	}

	if *cr.Spec.Mongod.Security.EnableEncryption &&
		cr.Spec.Mongod.Security.EncryptionKeySecret == "" {
		cr.Spec.Mongod.Security.EncryptionKeySecret = cr.Name + "-mongodb-encryption-key"
	}

	if cr.Spec.Secrets.SSL == "" {
		cr.Spec.Secrets.SSL = cr.Name + "-ssl"
	}

	if cr.Spec.Secrets.SSLInternal == "" {
		cr.Spec.Secrets.SSLInternal = cr.Name + "-ssl-internal"
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

	gte140 := cr.CompareVersion("1.4.0") >= 0

	timeoutSecondsDefault := int32(5)
	initialDelaySecondsDefault := int32(90)
	periodSecondsDeafult := int32(10)
	failureThresholdDefault := int32(12)
	if gte140 {
		initialDelaySecondsDefault = int32(60)
		periodSecondsDeafult = int32(30)
		failureThresholdDefault = int32(4)
	}
	startupDelaySecondsFlag := "--startupDelaySeconds"

	if cr.Spec.Sharding.Enabled {
		if cr.Spec.Sharding.ConfigsvrReplSet == nil {
			return errors.New("config replica set should be specified")
		}

		if cr.Spec.Sharding.Mongos == nil {
			return errors.New("mongos should be specified")
		}

		cr.Spec.Sharding.ConfigsvrReplSet.Name = ConfigReplSetName

		if cr.Spec.Sharding.Mongos.Port == 0 {
			cr.Spec.Sharding.Mongos.Port = 27017
		}

		for i := range cr.Spec.Replsets {
			cr.Spec.Replsets[i].ClusterRole = ClusterRoleShardSvr
		}

		cr.Spec.Sharding.ConfigsvrReplSet.ClusterRole = ClusterRoleConfigSvr

		if cr.Spec.Sharding.Mongos.LivenessProbe == nil {
			if cr.Spec.Sharding.Mongos.LivenessProbe == nil {
				cr.Spec.Sharding.Mongos.LivenessProbe = new(LivenessProbeExtended)
				cr.Spec.Sharding.Mongos.LivenessProbe.Probe = corev1.Probe{
					Handler: corev1.Handler{
						TCPSocket: &corev1.TCPSocketAction{
							Port: intstr.FromInt(int(cr.Spec.Sharding.Mongos.Port)),
						},
					},
				}
			}

			if cr.Spec.Sharding.Mongos.LivenessProbe.InitialDelaySeconds == 0 {
				cr.Spec.Sharding.Mongos.LivenessProbe.InitialDelaySeconds = initialDelaySecondsDefault
			}
			if cr.Spec.Sharding.Mongos.LivenessProbe.TimeoutSeconds == 0 {
				cr.Spec.Sharding.Mongos.LivenessProbe.TimeoutSeconds = timeoutSecondsDefault
			}
			if cr.Spec.Sharding.Mongos.LivenessProbe.PeriodSeconds == 0 {
				cr.Spec.Sharding.Mongos.LivenessProbe.PeriodSeconds = periodSecondsDeafult
			}
			if cr.Spec.Sharding.Mongos.LivenessProbe.FailureThreshold == 0 {
				cr.Spec.Sharding.Mongos.LivenessProbe.FailureThreshold = failureThresholdDefault
			}
			if cr.Spec.Sharding.Mongos.LivenessProbe.StartupDelaySeconds == 0 {
				cr.Spec.Sharding.Mongos.LivenessProbe.StartupDelaySeconds = 10
			}
		}

		if cr.Spec.Sharding.Mongos.ReadinessProbe == nil {
			if cr.Spec.Sharding.Mongos.ReadinessProbe == nil {
				cr.Spec.Sharding.Mongos.ReadinessProbe = &corev1.Probe{
					Handler: corev1.Handler{
						TCPSocket: &corev1.TCPSocketAction{
							Port: intstr.FromInt(int(cr.Spec.Sharding.Mongos.Port)),
						},
					},
				}
			}
			if cr.Spec.Sharding.Mongos.ReadinessProbe.InitialDelaySeconds == 0 {
				cr.Spec.Sharding.Mongos.ReadinessProbe.InitialDelaySeconds = int32(10)
			}
			if cr.Spec.Sharding.Mongos.ReadinessProbe.TimeoutSeconds == 0 {
				cr.Spec.Sharding.Mongos.ReadinessProbe.TimeoutSeconds = int32(2)
			}
			if cr.Spec.Sharding.Mongos.ReadinessProbe.PeriodSeconds == 0 {
				cr.Spec.Sharding.Mongos.ReadinessProbe.PeriodSeconds = int32(3)
			}
			if cr.Spec.Sharding.Mongos.ReadinessProbe.FailureThreshold == 0 {
				cr.Spec.Sharding.Mongos.ReadinessProbe.FailureThreshold = int32(8)
			}
		}
	}

	repls := cr.Spec.Replsets
	if cr.Spec.Sharding.Enabled {
		repls = append(repls, cr.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, replset := range repls {
		if replset.LivenessProbe == nil {
			replset.LivenessProbe = new(LivenessProbeExtended)
		}

		if replset.LivenessProbe.InitialDelaySeconds == 0 {
			replset.LivenessProbe.InitialDelaySeconds = initialDelaySecondsDefault
		}
		if replset.LivenessProbe.TimeoutSeconds == 0 {
			replset.LivenessProbe.TimeoutSeconds = timeoutSecondsDefault
		}
		if replset.LivenessProbe.PeriodSeconds == 0 {
			replset.LivenessProbe.PeriodSeconds = periodSecondsDeafult
		}
		if replset.LivenessProbe.FailureThreshold == 0 {
			replset.LivenessProbe.FailureThreshold = failureThresholdDefault
		}
		if replset.LivenessProbe.StartupDelaySeconds == 0 {
			replset.LivenessProbe.StartupDelaySeconds = 2 * 60 * 60
		}
		if replset.LivenessProbe.Handler.Exec == nil {
			replset.LivenessProbe.Probe.Handler.Exec = &corev1.ExecAction{
				Command: []string{
					"mongodb-healthcheck",
					"k8s",
					"liveness",
				},
			}
		}

		if gte140 && !replset.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
			replset.LivenessProbe.Handler.Exec.Command = append(
				replset.LivenessProbe.Handler.Exec.Command,
				startupDelaySecondsFlag, strconv.Itoa(replset.LivenessProbe.StartupDelaySeconds))
		}

		if replset.ReadinessProbe == nil {
			replset.ReadinessProbe = &corev1.Probe{
				Handler: corev1.Handler{
					TCPSocket: &corev1.TCPSocketAction{
						Port: intstr.FromInt(int(cr.Spec.Mongod.Net.Port)),
					},
				},
			}
		}
		if replset.ReadinessProbe.InitialDelaySeconds == 0 {
			replset.ReadinessProbe.InitialDelaySeconds = int32(10)
		}
		if replset.ReadinessProbe.TimeoutSeconds == 0 {
			replset.ReadinessProbe.TimeoutSeconds = int32(2)
		}
		if replset.ReadinessProbe.PeriodSeconds == 0 {
			replset.ReadinessProbe.PeriodSeconds = int32(3)
		}
		if replset.ReadinessProbe.FailureThreshold == 0 {
			replset.ReadinessProbe.FailureThreshold = int32(8)
		}

		err := replset.SetDefauts(platform, cr.Spec.UnsafeConf, log)
		if err != nil {
			return err
		}
		if cr.Spec.Pause {
			replset.Size = 0
			replset.Arbiter.Enabled = false
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
		for _, bkpTask := range cr.Spec.Backup.Tasks {
			if string(bkpTask.CompressionType) == "" {
				bkpTask.CompressionType = pbm.CompressionTypeGZIP
			}
		}
		if len(cr.Spec.Backup.ServiceAccountName) == 0 {
			cr.Spec.Backup.ServiceAccountName = "percona-server-mongodb-operator"
		}

		var fsgroup *int64
		if platform == version.PlatformKubernetes {
			var tp int64 = 1001
			fsgroup = &tp
		}

		if cr.Spec.Backup.ContainerSecurityContext == nil {
			tvar := true
			cr.Spec.Backup.ContainerSecurityContext = &corev1.SecurityContext{
				RunAsNonRoot: &tvar,
				RunAsUser:    fsgroup,
			}
		}
		if cr.Spec.Backup.PodSecurityContext == nil {
			cr.Spec.Backup.PodSecurityContext = &corev1.PodSecurityContext{
				FSGroup: fsgroup,
			}
		}
	}

	if cr.Status.Replsets == nil {
		cr.Status.Replsets = make(map[string]*ReplsetStatus)
	}

	if len(cr.Spec.ClusterServiceDNSSuffix) == 0 {
		cr.Spec.ClusterServiceDNSSuffix = DefaultDNSSuffix
	}

	return nil
}

// SetDefauts set default options for the replset
func (rs *ReplsetSpec) SetDefauts(platform version.Platform, unsafe bool, log logr.Logger) error {
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

	var fsgroup *int64
	if platform == version.PlatformKubernetes {
		var tp int64 = 1001
		fsgroup = &tp
	}

	if rs.ContainerSecurityContext == nil {
		tvar := true
		rs.ContainerSecurityContext = &corev1.SecurityContext{
			RunAsNonRoot: &tvar,
			RunAsUser:    fsgroup,
		}
	}
	if rs.PodSecurityContext == nil {
		rs.PodSecurityContext = &corev1.PodSecurityContext{
			FSGroup: fsgroup,
		}
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
	AffinityOff:                                {},
	"kubernetes.io/hostname":                   {},
	"failure-domain.beta.kubernetes.io/zone":   {},
	"failure-domain.beta.kubernetes.io/region": {},
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
	if v.EmptyDir == nil && v.HostPath == nil && v.PersistentVolumeClaim == nil {
		v.PersistentVolumeClaim = &corev1.PersistentVolumeClaimSpec{}
	}

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
