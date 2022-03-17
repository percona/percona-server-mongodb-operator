package v1

import (
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/percona/percona-server-mongodb-operator/version"
)

// DefaultDNSSuffix is a default dns suffix for the cluster service
const DefaultDNSSuffix = "svc.cluster.local"

// MultiClusterDefaultDNSSuffix is a default dns suffix for multi-cluster service
const MultiClusterDefaultDNSSuffix = "svc.clusterset.local"

const (
	MongodRESTencryptDir = "/etc/mongodb-encryption"
	EncryptionKeyName    = "encryption-key"
)

// ConfigReplSetName is the only possible name for config replica set
const (
	ConfigReplSetName = "cfg"
	WorkloadSA        = "default"
)

var (
	defaultRunUID                   int64 = 1001
	defaultUsersSecretName                = "percona-server-mongodb-users"
	defaultMongodSize               int32 = 3
	defaultReplsetName                    = "rs"
	defaultStorageEngine                  = StorageEngineWiredTiger
	DefaultMongodPort               int32 = 27017
	defaultWiredTigerCacheSizeRatio       = 0.5
	defaultInMemorySizeRatio              = 0.9
	defaultOperationProfilingMode         = OperationProfilingModeSlowOp
	defaultImagePullPolicy                = corev1.PullAlways
)

const (
	minSafeMongosSize                = 2
	minSafeReplicasetSizeWithArbiter = 4
	clusterNameMaxLen                = 50
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
	if cr.CompareVersion("1.12.0") < 0 {
		if cr.Spec.Mongod.Net == nil {
			cr.Spec.Mongod.Net = &MongodSpecNet{}
		}
		if cr.Spec.Mongod.Net.Port == 0 {
			cr.Spec.Mongod.Net.Port = DefaultMongodPort
		}
		if cr.Spec.Mongod.Storage == nil {
			cr.Spec.Mongod.Storage = &MongodSpecStorage{}
		}
		if cr.Spec.Mongod.Storage.Engine == "" {
			cr.Spec.Mongod.Storage.Engine = defaultStorageEngine
		}
		if cr.Spec.Mongod.OperationProfiling == nil {
			cr.Spec.Mongod.OperationProfiling = &MongodSpecOperationProfiling{
				Mode: defaultOperationProfilingMode,
			}
		}
		if cr.Spec.Mongod.Security == nil {
			cr.Spec.Mongod.Security = &MongodSpecSecurity{}
		}
		if cr.Spec.Mongod.Security.EnableEncryption == nil {
			is120 := cr.CompareVersion("1.2.0") >= 0
			cr.Spec.Mongod.Security.EnableEncryption = &is120
		}
	}

	if cr.Spec.EncryptionKeySecretName() == "" {
		is1120 := cr.CompareVersion("1.12.0") >= 0
		if is1120 || (!is1120 && *cr.Spec.Mongod.Security.EnableEncryption) {
			cr.Spec.Secrets.EncryptionKey = cr.Name + "-mongodb-encryption-key"
		}
	}

	if cr.Spec.Secrets.SSL == "" {
		cr.Spec.Secrets.SSL = cr.Name + "-ssl"
	}

	if cr.Spec.Secrets.SSLInternal == "" {
		cr.Spec.Secrets.SSLInternal = cr.Name + "-ssl-internal"
	}

	if cr.Spec.TLS == nil {
		cr.Spec.TLS = &TLSSpec{
			CertValidityDuration: metav1.Duration{Duration: time.Hour * 24 * 90},
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

	timeoutSecondsDefault := int32(5)
	initialDelaySecondsDefault := int32(90)
	periodSecondsDefault := int32(10)
	failureThresholdDefault := int32(12)
	if cr.CompareVersion("1.4.0") >= 0 {
		initialDelaySecondsDefault = int32(60)
		periodSecondsDefault = int32(30)
		failureThresholdDefault = int32(4)
	}
	if cr.CompareVersion("1.10.0") >= 0 {
		timeoutSecondsDefault = int32(10)
	}
	startupDelaySecondsFlag := "--startupDelaySeconds"

	if !cr.Spec.Sharding.Enabled {
		for i := range cr.Spec.Replsets {
			cr.Spec.Replsets[i].ClusterRole = ""
		}
	}

	if cr.Spec.Sharding.Enabled {
		if cr.Spec.Sharding.ConfigsvrReplSet == nil {
			return errors.New("config replica set should be specified")
		}

		if cr.Spec.Sharding.Mongos == nil {
			return errors.New("mongos should be specified")
		}

		if cr.Spec.Pause {
			cr.Spec.Sharding.Mongos.Size = 0
		} else {
			if !cr.Spec.UnsafeConf && cr.Spec.Sharding.Mongos.Size < minSafeMongosSize {
				log.Info(fmt.Sprintf("Mongos size will be changed from %d to %d due to safe config", cr.Spec.Sharding.Mongos.Size, minSafeMongosSize))
				log.Info("Set allowUnsafeConfigurations=true to disable safe configuration")
				cr.Spec.Sharding.Mongos.Size = minSafeMongosSize
			}
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
			cr.Spec.Sharding.Mongos.LivenessProbe = new(LivenessProbeExtended)
		}

		if cr.Spec.Sharding.Mongos.LivenessProbe.StartupDelaySeconds < 1 {
			cr.Spec.Sharding.Mongos.LivenessProbe.StartupDelaySeconds = 10
		}

		if cr.Spec.Sharding.Mongos.LivenessProbe.Exec == nil {
			cr.Spec.Sharding.Mongos.LivenessProbe.Exec = &corev1.ExecAction{
				Command: []string{
					"/data/db/mongodb-healthcheck",
					"k8s", "liveness",
					"--component", "mongos",
				},
			}

			if cr.CompareVersion("1.7.0") >= 0 {
				cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command =
					append(cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command,
						"--ssl", "--sslInsecure",
						"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
						"--sslPEMKeyFile", "/tmp/tls.pem")
			}

			if cr.CompareVersion("1.11.0") >= 0 && !cr.Spec.Sharding.Mongos.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
				cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command = append(
					cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command,
					startupDelaySecondsFlag, strconv.Itoa(cr.Spec.Sharding.Mongos.LivenessProbe.StartupDelaySeconds))
			}
		}

		if cr.Spec.Sharding.Mongos.LivenessProbe.InitialDelaySeconds < 1 {
			cr.Spec.Sharding.Mongos.LivenessProbe.InitialDelaySeconds = initialDelaySecondsDefault
		}
		if cr.Spec.Sharding.Mongos.LivenessProbe.TimeoutSeconds < 1 {
			cr.Spec.Sharding.Mongos.LivenessProbe.TimeoutSeconds = timeoutSecondsDefault
		}
		if cr.Spec.Sharding.Mongos.LivenessProbe.PeriodSeconds < 1 {
			cr.Spec.Sharding.Mongos.LivenessProbe.PeriodSeconds = periodSecondsDefault
		}
		if cr.Spec.Sharding.Mongos.LivenessProbe.FailureThreshold < 1 {
			cr.Spec.Sharding.Mongos.LivenessProbe.FailureThreshold = failureThresholdDefault
		}

		if cr.Spec.Sharding.Mongos.ReadinessProbe == nil {
			cr.Spec.Sharding.Mongos.ReadinessProbe = &corev1.Probe{}
		}

		if cr.Spec.Sharding.Mongos.ReadinessProbe.Exec == nil {
			cr.Spec.Sharding.Mongos.ReadinessProbe.Exec = &corev1.ExecAction{
				Command: []string{
					"/data/db/mongodb-healthcheck",
					"k8s", "readiness",
					"--component", "mongos",
				},
			}

			if cr.CompareVersion("1.7.0") >= 0 {
				cr.Spec.Sharding.Mongos.ReadinessProbe.Exec.Command =
					append(cr.Spec.Sharding.Mongos.ReadinessProbe.Exec.Command,
						"--ssl", "--sslInsecure",
						"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
						"--sslPEMKeyFile", "/tmp/tls.pem")
			}
		}

		if cr.Spec.Sharding.Mongos.ReadinessProbe.InitialDelaySeconds < 1 {
			cr.Spec.Sharding.Mongos.ReadinessProbe.InitialDelaySeconds = 10
		}
		if cr.Spec.Sharding.Mongos.ReadinessProbe.TimeoutSeconds < 1 {
			cr.Spec.Sharding.Mongos.ReadinessProbe.TimeoutSeconds = 2
			if cr.CompareVersion("1.11.0") >= 0 {
				cr.Spec.Sharding.Mongos.ReadinessProbe.TimeoutSeconds = 1
			}
		}
		if cr.Spec.Sharding.Mongos.ReadinessProbe.PeriodSeconds < 1 {
			cr.Spec.Sharding.Mongos.ReadinessProbe.PeriodSeconds = 1
		}
		if cr.CompareVersion("1.11.0") >= 0 && cr.Spec.Sharding.Mongos.ReadinessProbe.SuccessThreshold == 0 {
			cr.Spec.Sharding.Mongos.ReadinessProbe.SuccessThreshold = 1
		} else if cr.Spec.Sharding.Mongos.ReadinessProbe.SuccessThreshold < 0 {
			// skip "0" for compartibility but still not allow invalid value
			cr.Spec.Sharding.Mongos.ReadinessProbe.SuccessThreshold = 1
		}
		if cr.Spec.Sharding.Mongos.ReadinessProbe.FailureThreshold < 1 {
			cr.Spec.Sharding.Mongos.ReadinessProbe.FailureThreshold = 3
		}

		cr.Spec.Sharding.Mongos.reconcileOpts()

		if err := cr.Spec.Sharding.Mongos.Configuration.SetDefaults(); err != nil {
			return errors.Wrap(err, "failed to set configuration defaults")
		}

		if cr.Spec.Sharding.Mongos.Expose.ExposeType == "" {
			cr.Spec.Sharding.Mongos.Expose.ExposeType = corev1.ServiceTypeClusterIP
		}
	}

	repls := cr.Spec.Replsets
	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		cr.Spec.Sharding.ConfigsvrReplSet.Arbiter.Enabled = false
		for _, rs := range repls {
			if len(rs.ExternalNodes) > 0 && len(cr.Spec.Sharding.ConfigsvrReplSet.ExternalNodes) < 1 {
				return errors.Errorf("ConfigsvrReplSet must have externalNodes if replset %s has", rs.Name)
			}
			if len(cr.Spec.Sharding.ConfigsvrReplSet.ExternalNodes) > 0 && len(rs.ExternalNodes) < 1 {
				return errors.Errorf("replset %s must have externalNodes if ConfigsvrReplSet has", rs.Name)
			}
		}

		repls = append(repls, cr.Spec.Sharding.ConfigsvrReplSet)
	}

	for _, replset := range repls {
		if len(cr.Name+replset.Name) > clusterNameMaxLen {
			return errors.Errorf("cluster name (%s) + replset name (%s) is too long, must be no more than %d characters", cr.Name, replset.Name, clusterNameMaxLen)
		}

		if replset.Storage == nil {
			if cr.CompareVersion("1.12.0") >= 0 {
				replset.Storage = new(MongodSpecStorage)
				replset.Storage.Engine = defaultStorageEngine
			} else {
				replset.Storage = cr.Spec.Mongod.Storage
			}
		}

		switch replset.Storage.Engine {
		case StorageEngineInMemory:
			if replset.Storage.InMemory == nil {
				replset.Storage.InMemory = &MongodSpecInMemory{}
			}
			if replset.Storage.InMemory.EngineConfig == nil {
				replset.Storage.InMemory.EngineConfig = &MongodSpecInMemoryEngineConfig{}
			}
			if replset.Storage.InMemory.EngineConfig.InMemorySizeRatio == 0 {
				replset.Storage.InMemory.EngineConfig.InMemorySizeRatio = defaultInMemorySizeRatio
			}
		case StorageEngineWiredTiger:
			if replset.Storage.WiredTiger == nil {
				replset.Storage.WiredTiger = &MongodSpecWiredTiger{}
			}
			if replset.Storage.WiredTiger.CollectionConfig == nil {
				replset.Storage.WiredTiger.CollectionConfig = &MongodSpecWiredTigerCollectionConfig{}
			}
			if replset.Storage.WiredTiger.EngineConfig == nil {
				replset.Storage.WiredTiger.EngineConfig = &MongodSpecWiredTigerEngineConfig{}
			}
			if replset.Storage.WiredTiger.EngineConfig.CacheSizeRatio == 0 {
				replset.Storage.WiredTiger.EngineConfig.CacheSizeRatio = defaultWiredTigerCacheSizeRatio
			}
			if replset.Storage.WiredTiger.IndexConfig == nil {
				replset.Storage.WiredTiger.IndexConfig = &MongodSpecWiredTigerIndexConfig{
					PrefixCompression: true,
				}
			}
		}

		if replset.Storage.Engine == StorageEngineMMAPv1 {
			return errors.Errorf("%s storage engine is not supported", StorageEngineMMAPv1)
		}
		if cr.Spec.Sharding.Enabled && replset.ClusterRole == ClusterRoleConfigSvr && replset.Storage.Engine != StorageEngineWiredTiger {
			return errors.Errorf("%s storage engine is not supported for config server replica set", replset.Storage.Engine)
		}

		if replset.LivenessProbe == nil {
			replset.LivenessProbe = new(LivenessProbeExtended)
		}

		if replset.LivenessProbe.StartupDelaySeconds == 0 {
			replset.LivenessProbe.StartupDelaySeconds = 2 * 60 * 60
		}
		if replset.LivenessProbe.Exec == nil {
			replset.LivenessProbe.Exec = &corev1.ExecAction{
				Command: []string{"mongodb-healthcheck", "k8s", "liveness"},
			}

			if cr.CompareVersion("1.6.0") >= 0 {
				replset.LivenessProbe.Probe.Exec.Command[0] = "/data/db/mongodb-healthcheck"
				if cr.CompareVersion("1.7.0") >= 0 {
					replset.LivenessProbe.Probe.Exec.Command =
						append(replset.LivenessProbe.Probe.Exec.Command,
							"--ssl", "--sslInsecure",
							"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
							"--sslPEMKeyFile", "/tmp/tls.pem")
				}
			}

			if cr.CompareVersion("1.4.0") >= 0 && !replset.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
				replset.LivenessProbe.Exec.Command = append(
					replset.LivenessProbe.Exec.Command,
					startupDelaySecondsFlag, strconv.Itoa(replset.LivenessProbe.StartupDelaySeconds))
			}
		}

		if replset.LivenessProbe.InitialDelaySeconds < 1 {
			replset.LivenessProbe.InitialDelaySeconds = initialDelaySecondsDefault
		}
		if replset.LivenessProbe.TimeoutSeconds < 1 {
			replset.LivenessProbe.TimeoutSeconds = timeoutSecondsDefault
		}
		if replset.LivenessProbe.PeriodSeconds < 1 {
			replset.LivenessProbe.PeriodSeconds = periodSecondsDefault
		}
		if replset.LivenessProbe.FailureThreshold < 1 {
			replset.LivenessProbe.FailureThreshold = failureThresholdDefault
		}

		if replset.ReadinessProbe == nil {
			replset.ReadinessProbe = &corev1.Probe{}
		}

		if replset.ReadinessProbe.TCPSocket == nil {
			replset.ReadinessProbe.TCPSocket = &corev1.TCPSocketAction{
				Port: intstr.FromInt(int(MongodPort(cr))),
			}
		}

		if replset.ReadinessProbe.InitialDelaySeconds < 1 {
			replset.ReadinessProbe.InitialDelaySeconds = 10
		}
		if replset.ReadinessProbe.TimeoutSeconds < 1 {
			replset.ReadinessProbe.TimeoutSeconds = 2
		}
		if replset.ReadinessProbe.PeriodSeconds < 1 {
			replset.ReadinessProbe.PeriodSeconds = 3
		}
		if replset.ReadinessProbe.SuccessThreshold < 1 {
			replset.ReadinessProbe.SuccessThreshold = 1
		}
		if replset.ReadinessProbe.FailureThreshold < 1 {
			if cr.CompareVersion("1.11.0") >= 0 && replset.Name == ConfigReplSetName {
				replset.ReadinessProbe.FailureThreshold = 3
			} else {
				replset.ReadinessProbe.FailureThreshold = 8
			}
		}

		if cr.CompareVersion("1.6.0") >= 0 && len(replset.ServiceAccountName) == 0 {
			replset.ServiceAccountName = WorkloadSA
		}

		if cr.Spec.Unmanaged && !replset.Expose.Enabled {
			return errors.Errorf("replset %s needs to be exposed if cluster is unmanaged", replset.Name)
		}

		err := replset.SetDefauts(platform, cr.Spec.UnsafeConf, log)
		if err != nil {
			return err
		}

		if err := replset.NonVoting.SetDefaults(cr, replset); err != nil {
			return errors.Wrap(err, "set nonvoting defaults")
		}

		if cr.Spec.Pause {
			replset.Size = 0
			replset.Arbiter.Enabled = false
			replset.NonVoting.Enabled = false
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

	if !cr.Spec.Backup.Enabled {
		cr.Spec.Backup.PITR.Enabled = false
	}

	if cr.Spec.Backup.PITR.Enabled {
		if len(cr.Spec.Backup.Storages) != 1 {
			cr.Spec.Backup.PITR.Enabled = false
			log.Info("Point-in-time recovery can be enabled only if one bucket is used in spec.backup.storages")
		}

		if cr.Spec.Backup.PITR.OplogSpanMin == 0 {
			cr.Spec.Backup.PITR.OplogSpanMin = 10
		}
	}

	if cr.Status.Replsets == nil {
		cr.Status.Replsets = make(map[string]*ReplsetStatus)
	}

	if len(cr.Spec.ClusterServiceDNSSuffix) == 0 {
		cr.Spec.ClusterServiceDNSSuffix = DefaultDNSSuffix
	}

	if cr.Spec.Unmanaged && cr.Spec.Backup.Enabled {
		return errors.New("backup.enabled must be false on unmanaged clusters")
	}

	if cr.Spec.Unmanaged && cr.Spec.UpdateStrategy == SmartUpdateStatefulSetStrategyType {
		return errors.New("SmartUpdate is not allowed on unmanaged clusters, set updateStrategy to RollingUpdate or OnDelete")
	}

	if cr.Spec.MultiCluster.Enabled && len(cr.Spec.MultiCluster.DNSSuffix) == 0 {
		cr.Spec.MultiCluster.DNSSuffix = MultiClusterDefaultDNSSuffix
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

	if err := rs.Configuration.SetDefaults(); err != nil {
		return errors.Wrap(err, "failed to set configuration defaults")
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

	if len(rs.ExternalNodes) > 0 && !rs.Expose.Enabled {
		return errors.Errorf("replset %s must be exposed to add external nodes", rs.Name)
	}

	for _, extNode := range rs.ExternalNodes {
		if extNode.Port == 0 {
			extNode.Port = 27017
		}
		if extNode.Votes < 0 || extNode.Votes > 1 {
			return errors.Errorf("invalid votes for %s: votes must be 0 or 1", extNode.Host)
		}
		if extNode.Priority < 0 || extNode.Priority > 1000 {
			return errors.Errorf("invalid priority for %s: priority must be between 0 and 1000", extNode.Host)
		}
		if extNode.Votes == 0 && extNode.Priority != 0 {
			return errors.Errorf("invalid priority for %s: non-voting members must have priority 0", extNode.Host)
		}
	}

	return nil
}

func (nv *NonVotingSpec) SetDefaults(cr *PerconaServerMongoDB, rs *ReplsetSpec) error {
	if !nv.Enabled {
		return nil
	}

	if nv.VolumeSpec != nil {
		if err := nv.VolumeSpec.reconcileOpts(); err != nil {
			return errors.Wrapf(err, "reconcile volumes for replset %s nonVoting", rs.Name)
		}
	} else {
		nv.VolumeSpec = rs.VolumeSpec
	}

	startupDelaySecondsFlag := "--startupDelaySeconds"

	if nv.LivenessProbe == nil {
		nv.LivenessProbe = new(LivenessProbeExtended)
	}
	if nv.LivenessProbe.InitialDelaySeconds < 1 {
		nv.LivenessProbe.InitialDelaySeconds = rs.LivenessProbe.InitialDelaySeconds
	}
	if nv.LivenessProbe.TimeoutSeconds < 1 {
		nv.LivenessProbe.TimeoutSeconds = rs.LivenessProbe.TimeoutSeconds
	}
	if nv.LivenessProbe.PeriodSeconds < 1 {
		nv.LivenessProbe.PeriodSeconds = rs.LivenessProbe.PeriodSeconds
	}
	if nv.LivenessProbe.FailureThreshold < 1 {
		nv.LivenessProbe.FailureThreshold = rs.LivenessProbe.FailureThreshold
	}
	if nv.LivenessProbe.StartupDelaySeconds < 1 {
		nv.LivenessProbe.StartupDelaySeconds = rs.LivenessProbe.StartupDelaySeconds
	}
	if nv.LivenessProbe.ProbeHandler.Exec == nil {
		nv.LivenessProbe.Probe.ProbeHandler.Exec = &corev1.ExecAction{
			Command: []string{
				"/data/db/mongodb-healthcheck",
				"k8s",
				"liveness",
				"--ssl", "--sslInsecure",
				"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
				"--sslPEMKeyFile", "/tmp/tls.pem",
			},
		}
	}
	if !nv.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
		nv.LivenessProbe.ProbeHandler.Exec.Command = append(
			nv.LivenessProbe.ProbeHandler.Exec.Command,
			startupDelaySecondsFlag, strconv.Itoa(nv.LivenessProbe.StartupDelaySeconds))
	}

	if nv.ReadinessProbe == nil {
		nv.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(int(MongodPort(cr))),
				},
			},
		}
	}
	if nv.ReadinessProbe.InitialDelaySeconds < 1 {
		nv.ReadinessProbe.InitialDelaySeconds = rs.ReadinessProbe.InitialDelaySeconds
	}
	if nv.ReadinessProbe.TimeoutSeconds < 1 {
		nv.ReadinessProbe.TimeoutSeconds = rs.ReadinessProbe.TimeoutSeconds
	}
	if nv.ReadinessProbe.PeriodSeconds < 1 {
		nv.ReadinessProbe.PeriodSeconds = rs.ReadinessProbe.PeriodSeconds
	}
	if nv.ReadinessProbe.FailureThreshold < 1 {
		nv.ReadinessProbe.FailureThreshold = rs.ReadinessProbe.FailureThreshold
	}

	if len(nv.ServiceAccountName) == 0 {
		nv.ServiceAccountName = WorkloadSA
	}

	nv.MultiAZ.reconcileOpts()

	if nv.ContainerSecurityContext == nil {
		nv.ContainerSecurityContext = rs.ContainerSecurityContext
	}

	if nv.PodSecurityContext == nil {
		nv.PodSecurityContext = rs.PodSecurityContext
	}

	if err := nv.Configuration.SetDefaults(); err != nil {
		return errors.Wrap(err, "failed to set configuration defaults")
	}

	return nil
}

func (rs *ReplsetSpec) setSafeDefauts(log logr.Logger) {
	loginfo := func(msg string, args ...interface{}) {
		log.Info(msg, args...)
		log.Info("Set allowUnsafeConfigurations=true to disable safe configuration")
	}

	if rs.Arbiter.Enabled {
		if rs.Arbiter.Size != 1 {
			loginfo(fmt.Sprintf("Arbiter size will be changed from %d to 1 due to safe config", rs.Arbiter.Size))
			rs.Arbiter.Size = 1
		}
		if rs.Size < minSafeReplicasetSizeWithArbiter {
			loginfo(fmt.Sprintf("Replset size will be changed from %d to %d due to safe config", rs.Size, minSafeReplicasetSizeWithArbiter))
			rs.Size = minSafeReplicasetSizeWithArbiter
		}
		if rs.Size%2 != 0 {
			loginfo(fmt.Sprintf("Arbiter will be switched off. There is no need in arbiter with odd replset size (%d)", rs.Size))
			rs.Arbiter.Enabled = false
			rs.Arbiter.Size = 0
		}
	} else {
		if rs.Size < 2 {
			loginfo(fmt.Sprintf("Replset size will be changed from %d to %d due to safe config", rs.Size, defaultMongodSize))
			rs.Size = defaultMongodSize
		}
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

func MongodPort(cr *PerconaServerMongoDB) int32 {
	if cr.CompareVersion("1.12.0") >= 0 {
		return defaultMongodPort
	}
	return cr.Spec.Mongod.Net.Port
}
