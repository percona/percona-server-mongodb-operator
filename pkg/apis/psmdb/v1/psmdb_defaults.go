package v1

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/go-logr/logr"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/compress"

	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
	"github.com/percona/percona-server-mongodb-operator/pkg/util/numstr"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

// DefaultDNSSuffix is a default dns suffix for the cluster service
const DefaultDNSSuffix = "svc.cluster.local"

// MultiClusterDefaultDNSSuffix is a default dns suffix for multi-cluster service
const MultiClusterDefaultDNSSuffix = "svc.clusterset.local"

const (
	MongodRESTencryptDir = "/etc/mongodb-encryption"
	InternalKeyName      = "mongodb-key"
	EncryptionKeyName    = "encryption-key"
)

// ConfigReplSetName is the only possible name for config replica set
const (
	ConfigReplSetName = "cfg"
	WorkloadSA        = "default"
)

var (
	defaultUsersSecretName                = "percona-server-mongodb-users"
	defaultMongodSize               int32 = 3
	defaultReplsetName                    = "rs"
	defaultStorageEngine                  = StorageEngineWiredTiger
	defaultWiredTigerCacheSizeRatio       = numstr.MustParse("0.5")
	defaultInMemorySizeRatio              = numstr.MustParse("0.9")
	defaultImagePullPolicy                = corev1.PullAlways

	DefaultMongoPort int32 = 27017
)

const (
	minSafeMongosSize                = 2
	minSafeReplicasetSizeWithArbiter = 4
	clusterNameMaxLen                = 50
)

// CheckNSetDefaults sets default options, overwrites wrong settings
// and checks if other options' values valid
func (cr *PerconaServerMongoDB) CheckNSetDefaults(ctx context.Context, platform version.Platform) error {
	log := logf.FromContext(ctx)

	if cr.Spec.Replsets == nil {
		return errors.New("at least one replica set should be specified")
	}

	if cr.Spec.Image == "" {
		return errors.New("Required value for spec.image")
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

	if cr.Spec.Secrets.EncryptionKey == "" {
		cr.Spec.Secrets.EncryptionKey = cr.Name + "-mongodb-encryption-key"
	}

	if cr.Spec.Secrets.SSL == "" {
		cr.Spec.Secrets.SSL = cr.Name + "-ssl"
	}

	if cr.Spec.Secrets.SSLInternal == "" {
		cr.Spec.Secrets.SSLInternal = cr.Name + "-ssl-internal"
	}

	t := true
	if cr.Spec.TLS == nil {
		cr.Spec.TLS = &TLSSpec{
			Mode:                     TLSModePrefer,
			AllowInvalidCertificates: &t,
			CertValidityDuration:     metav1.Duration{Duration: time.Hour * 24 * 90},
		}
	}

	if cr.Spec.TLS.Mode == "" {
		cr.Spec.TLS.Mode = TLSModePrefer
	}

	if cr.Spec.TLS.CertValidityDuration.Duration == 0 {
		cr.Spec.TLS.CertValidityDuration = metav1.Duration{Duration: time.Hour * 24 * 90}
	}

	if cr.Spec.TLS.AllowInvalidCertificates == nil {
		cr.Spec.TLS.AllowInvalidCertificates = &t
	}

	if cr.Spec.UnsafeConf {
		cr.Spec.Unsafe = UnsafeFlags{
			TLS:                    true,
			ReplsetSize:            true,
			MongosSize:             true,
			BackupIfUnhealthy:      true,
			TerminationGracePeriod: true,
		}
		cr.Spec.TLS.Mode = TLSModeDisabled
	}

	if !cr.TLSEnabled() && !cr.Spec.Unsafe.TLS {
		return errors.New("TLS must be enabled. Set spec.unsafeFlags.tls to true to disable this check")
	}

	if len(cr.Spec.Replsets) == 0 {
		cr.Spec.Replsets = []*ReplsetSpec{
			{
				Name: defaultReplsetName + "0",
				Size: defaultMongodSize,
			},
		}
	} else {
		for i := 0; i != len(cr.Spec.Replsets); i++ {
			if rs := cr.Spec.Replsets[i]; rs.Name == "" {
				rs.Name = defaultReplsetName + strconv.Itoa(i)
			}
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

		if cr.CompareVersion("1.16.0") < 0 {
			if !cr.Spec.Pause && cr.DeletionTimestamp == nil {
				if !cr.Spec.UnsafeConf && cr.Spec.Sharding.Mongos.Size < minSafeMongosSize {
					log.Info("Safe config set, updating mongos size",
						"oldSize", cr.Spec.Sharding.Mongos.Size, "newSize", minSafeMongosSize)
					cr.Spec.Sharding.Mongos.Size = minSafeMongosSize
				}
			}
		}

		if cr.CompareVersion("1.15.0") >= 0 {
			var fsgroup *int64
			if platform == version.PlatformKubernetes {
				var tp int64 = 1001
				fsgroup = &tp
			}

			if cr.Spec.Sharding.Mongos.ContainerSecurityContext == nil {
				tvar := true
				cr.Spec.Sharding.Mongos.ContainerSecurityContext = &corev1.SecurityContext{
					RunAsNonRoot: &tvar,
					RunAsUser:    fsgroup,
				}
			}

			if cr.Spec.Sharding.Mongos.PodSecurityContext == nil {
				cr.Spec.Sharding.Mongos.PodSecurityContext = &corev1.PodSecurityContext{
					FSGroup: fsgroup,
				}
			}
		}
		cr.Spec.Sharding.ConfigsvrReplSet.Name = ConfigReplSetName

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

			if cr.TLSEnabled() {
				cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command = append(cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command,
					"--ssl", "--sslInsecure",
					"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
					"--sslPEMKeyFile", "/tmp/tls.pem")
			}

			if cr.CompareVersion("1.11.0") >= 0 && !cr.Spec.Sharding.Mongos.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
				cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command = append(
					cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command,
					startupDelaySecondsFlag, strconv.Itoa(cr.Spec.Sharding.Mongos.LivenessProbe.StartupDelaySeconds))
			}

			if cr.CompareVersion("1.14.0") >= 0 {
				cr.Spec.Sharding.Mongos.LivenessProbe.Exec.Command[0] = "/opt/percona/mongodb-healthcheck"
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

			if cr.TLSEnabled() {
				cr.Spec.Sharding.Mongos.ReadinessProbe.Exec.Command = append(cr.Spec.Sharding.Mongos.ReadinessProbe.Exec.Command,
					"--ssl", "--sslInsecure",
					"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
					"--sslPEMKeyFile", "/tmp/tls.pem")
			}

			if cr.CompareVersion("1.14.0") >= 0 {
				cr.Spec.Sharding.Mongos.ReadinessProbe.Exec.Command[0] = "/opt/percona/mongodb-healthcheck"
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

		if err := cr.Spec.Sharding.Mongos.reconcileOpts(cr); err != nil {
			return errors.Wrap(err, "reconcile mongos options")
		}

		if err := cr.Spec.Sharding.Mongos.Configuration.SetDefaults(); err != nil {
			return errors.Wrap(err, "failed to set configuration defaults")
		}

		if cr.Spec.Sharding.Mongos.Expose.ExposeType == "" {
			cr.Spec.Sharding.Mongos.Expose.ExposeType = corev1.ServiceTypeClusterIP

			if deprecatedType := cr.Spec.Sharding.Mongos.Expose.DeprecatedExposeType; deprecatedType != "" {
				cr.Spec.Sharding.Mongos.Expose.ExposeType = deprecatedType
			}
		}
		if len(cr.Spec.Sharding.Mongos.Expose.DeprecatedServiceLabels) > 0 {
			cr.Spec.Sharding.Mongos.Expose.ServiceLabels = util.MapMerge(cr.Spec.Sharding.Mongos.Expose.DeprecatedServiceLabels, cr.Spec.Sharding.Mongos.Expose.ServiceLabels)
		}
		if len(cr.Spec.Sharding.Mongos.Expose.DeprecatedServiceAnnotations) > 0 {
			cr.Spec.Sharding.Mongos.Expose.ServiceAnnotations = util.MapMerge(cr.Spec.Sharding.Mongos.Expose.DeprecatedServiceAnnotations, cr.Spec.Sharding.Mongos.Expose.ServiceAnnotations)
		}

		if len(cr.Spec.Sharding.Mongos.ServiceAccountName) == 0 && cr.CompareVersion("1.16.0") >= 0 {
			cr.Spec.Sharding.Mongos.ServiceAccountName = WorkloadSA
		}
	}

	if mongos := cr.Spec.Sharding.Mongos; mongos != nil && cr.CompareVersion("1.18.0") >= 0 && cr.Status.State == AppStateInit {
		if mongos.Expose.DeprecatedExposeType != "" {
			log.Info("Field `.spec.sharding.mongos.expose.exposeType` was deprecated in 1.18.0. Consider using `.spec.sharding.mongos.expose.type` instead", "cluster", cr.Name, "namespace", cr.Namespace)
		}
		if len(mongos.Expose.DeprecatedServiceLabels) > 0 {
			log.Info("Field `.spec.sharding.mongos.expose.serviceLabels` was deprecated in 1.18.0. Consider using `.spec.sharding.mongos.expose.labels` instead", "cluster", cr.Name, "namespace", cr.Namespace)
		}
		if len(mongos.Expose.DeprecatedServiceAnnotations) > 0 {
			log.Info("Field `.spec.sharding.mongos.expose.serviceAnnotations` was deprecated in 1.18.0. Consider using `.spec.sharding.mongos.expose.annotations` instead", "cluster", cr.Name, "namespace", cr.Namespace)
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
			replset.Storage = new(MongodSpecStorage)
			replset.Storage.Engine = defaultStorageEngine
		}
		if replset.Storage.Engine == "" {
			replset.Storage.Engine = defaultStorageEngine
		}

		switch replset.Storage.Engine {
		case StorageEngineInMemory:
			if replset.Storage.InMemory == nil {
				replset.Storage.InMemory = &MongodSpecInMemory{}
			}
			if replset.Storage.InMemory.EngineConfig == nil {
				replset.Storage.InMemory.EngineConfig = &MongodSpecInMemoryEngineConfig{}
			}
			if replset.Storage.InMemory.EngineConfig.InMemorySizeRatio.Float64() == 0 {
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
			if replset.Storage.WiredTiger.EngineConfig.CacheSizeRatio.Float64() == 0 {
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

			replset.LivenessProbe.Probe.Exec.Command[0] = "/data/db/mongodb-healthcheck"
			if cr.TLSEnabled() {
				replset.LivenessProbe.Probe.Exec.Command = append(replset.LivenessProbe.Probe.Exec.Command,
					"--ssl", "--sslInsecure",
					"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
					"--sslPEMKeyFile", "/tmp/tls.pem")
			}

			if cr.CompareVersion("1.4.0") >= 0 && !replset.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
				replset.LivenessProbe.Exec.Command = append(
					replset.LivenessProbe.Exec.Command,
					startupDelaySecondsFlag, strconv.Itoa(replset.LivenessProbe.StartupDelaySeconds))
			}

			if cr.CompareVersion("1.14.0") >= 0 {
				replset.LivenessProbe.Exec.Command[0] = "/opt/percona/mongodb-healthcheck"
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

		if replset.ReadinessProbe.TCPSocket == nil && replset.ReadinessProbe.Exec == nil {
			replset.ReadinessProbe.Exec = &corev1.ExecAction{
				Command: []string{
					"/opt/percona/mongodb-healthcheck",
					"k8s", "readiness",
					"--component", "mongod",
				},
			}

			if cr.CompareVersion("1.15.0") < 0 {
				replset.ReadinessProbe.Exec = nil
				replset.ReadinessProbe.TCPSocket = &corev1.TCPSocketAction{
					Port: intstr.FromInt(int(replset.GetPort())),
				}
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
			log.Info("Replset is not exposed. Make sure each pod in the replset can reach each other.", "replset", replset.Name)
		}

		err := replset.SetDefaults(platform, cr, log)
		if err != nil {
			return err
		}

		if err := replset.NonVoting.SetDefaults(cr, replset); err != nil {
			return errors.Wrap(err, "set nonvoting defaults")
		}

		if err := replset.Hidden.SetDefaults(cr, replset); err != nil {
			return errors.Wrap(err, "set nonvoting defaults")
		}
	}

	if cr.Spec.Backup.Enabled {
		for i := range cr.Spec.Backup.Tasks {
			bkpTask := &cr.Spec.Backup.Tasks[i]

			if bkpTask.Name == "" {
				return errors.Errorf("backup task %d should have a name", i)
			}
			if string(bkpTask.CompressionType) == "" {
				bkpTask.CompressionType = compress.CompressionTypeGZIP
			}

			if cr.CompareVersion("1.21.0") >= 0 && bkpTask.Keep > 0 && bkpTask.Retention != nil && bkpTask.Retention.Count == 0 {
				log.Info(".spec.backup.tasks[].keep will be deprecated in the future. Consider using .spec.backup.tasks[].retention.count instead", "task", bkpTask.Name)
				continue
			}
		}
		if len(cr.Spec.Backup.ServiceAccountName) == 0 && cr.CompareVersion("1.15.0") < 0 {
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

		if cr.Spec.PMM.ContainerSecurityContext == nil {
			tvar := true
			cr.Spec.PMM.ContainerSecurityContext = &corev1.SecurityContext{
				RunAsNonRoot: &tvar,
				RunAsUser:    fsgroup,
			}
		}

		if cr.CompareVersion("1.20.0") < 0 && cr.Spec.Backup.PITR.Enabled {
			if len(cr.Spec.Backup.Storages) != 1 {
				cr.Spec.Backup.PITR.Enabled = false
				log.Info("Point-in-time recovery can be enabled only if one bucket is used in spec.backup.storages")
			}
		}

		if cr.CompareVersion("1.20.0") >= 0 && len(cr.Spec.Backup.Storages) > 1 {
			main := 0
			for _, stg := range cr.Spec.Backup.Storages {
				if stg.Main {
					main += 1
				}
			}

			if main == 0 {
				return errors.New("main backup storage is not specified")
			}

			if main > 1 {
				return errors.New("multiple main backup storages are specified")
			}
		}

		for _, stg := range cr.Spec.Backup.Storages {
			if stg.Type != BackupStorageS3 {
				continue
			}

			if len(stg.S3.ServerSideEncryption.SSECustomerAlgorithm) != 0 &&
				len(stg.S3.ServerSideEncryption.SSEAlgorithm) != 0 {
				return errors.New("For S3 storage only one encryption method can be used. Set either (sseAlgorithm and kmsKeyID) or (sseCustomerAlgorithm and sseCustomerKey)")
			}
		}
	}

	if !cr.Spec.Backup.Enabled {
		cr.Spec.Backup.PITR.Enabled = false
	}

	if cr.Spec.Backup.PITR.Enabled && cr.Spec.Backup.PITR.OplogSpanMin.Float64() == 0 {
		cr.Spec.Backup.PITR.OplogSpanMin = numstr.MustParse("10")
	}

	if cr.Status.Replsets == nil {
		cr.Status.Replsets = make(map[string]ReplsetStatus)
	}

	if len(cr.Spec.ClusterServiceDNSSuffix) == 0 {
		cr.Spec.ClusterServiceDNSSuffix = DefaultDNSSuffix
	}

	if cr.Spec.ClusterServiceDNSMode == "" {
		cr.Spec.ClusterServiceDNSMode = DNSModeInternal
	}

	if len(cr.Spec.UpdateStrategy) == 0 {
		cr.Spec.UpdateStrategy = SmartUpdateStatefulSetStrategyType
	}

	if cr.Spec.Unmanaged && cr.Spec.UpdateStrategy == SmartUpdateStatefulSetStrategyType {
		return errors.New("SmartUpdate is not allowed on unmanaged clusters, set updateStrategy to RollingUpdate or OnDelete")
	}

	if cr.Spec.UpgradeOptions.VersionServiceEndpoint == "" {
		cr.Spec.UpgradeOptions.VersionServiceEndpoint = DefaultVersionServiceEndpoint
	}

	if cr.Spec.UpgradeOptions.Apply == "" {
		cr.Spec.UpgradeOptions.Apply = UpgradeStrategyDisabled
	}

	if len(cr.Spec.MultiCluster.DNSSuffix) == 0 {
		cr.Spec.MultiCluster.DNSSuffix = MultiClusterDefaultDNSSuffix
	}

	if !mcs.IsAvailable() && cr.Spec.MultiCluster.Enabled {
		return errors.New("MCS is not available on this cluster")
	}

	return nil
}

func (rs *ReplsetSpec) IsEncryptionEnabled() (bool, error) {
	enabled, err := rs.Configuration.isEncryptionEnabled()
	if err != nil {
		return false, errors.Wrap(err, "failed to parse replset configuration")
	}

	if enabled == nil {
		if rs.Storage.Engine == StorageEngineInMemory {
			return false, nil // disabled for inMemory engine by default
		}
		return true, nil // true by default
	}
	return *enabled, nil
}

// SetDefaults set default options for the replset
func (rs *ReplsetSpec) SetDefaults(platform version.Platform, cr *PerconaServerMongoDB, log logr.Logger) error {
	if rs.VolumeSpec == nil {
		return fmt.Errorf("replset %s: volumeSpec should be specified", rs.Name)
	}

	err := rs.VolumeSpec.reconcileOpts()
	if err != nil {
		return fmt.Errorf("replset %s VolumeSpec: %v", rs.Name, err)
	}

	if rs.Expose.Enabled {
		if rs.Expose.ExposeType == "" {
			rs.Expose.ExposeType = corev1.ServiceTypeClusterIP

			if rs.Expose.DeprecatedExposeType != "" {
				rs.Expose.ExposeType = rs.Expose.DeprecatedExposeType
			}
		}
		if len(rs.Expose.DeprecatedServiceLabels) > 0 {
			rs.Expose.ServiceLabels = util.MapMerge(rs.Expose.DeprecatedServiceLabels, rs.Expose.ServiceLabels)
		}
		if len(rs.Expose.DeprecatedServiceAnnotations) > 0 {
			rs.Expose.ServiceAnnotations = util.MapMerge(rs.Expose.DeprecatedServiceAnnotations, rs.Expose.ServiceAnnotations)
		}
	}

	if cr.CompareVersion("1.18.0") >= 0 && cr.Status.State == AppStateInit {
		if rs.Expose.DeprecatedExposeType != "" {
			log.Info("Field `.expose.exposeType` was deprecated in 1.18.0. Consider using `.expose.type` instead", "cluster", cr.Name, "namespace", cr.Namespace, "replset", rs.Name)
		}
		if len(rs.Expose.DeprecatedServiceLabels) > 0 {
			log.Info("Field `.expose.serviceLabels` was deprecated in 1.18.0. Consider using `.expose.labels` instead", "cluster", cr.Name, "namespace", cr.Namespace, "replset", rs.Name)
		}
		if len(rs.Expose.DeprecatedServiceAnnotations) > 0 {
			log.Info("Field `.expose.serviceAnnotations` was deprecated in 1.18.0. Consider using `.expose.annotations` instead", "cluster", cr.Name, "namespace", cr.Namespace, "replset", rs.Name)
		}
	}

	if err := rs.MultiAZ.reconcileOpts(cr); err != nil {
		return errors.Wrapf(err, "reconcile multiAZ options for replset %s", rs.Name)
	}

	if rs.Arbiter.Enabled {
		if err := rs.Arbiter.MultiAZ.reconcileOpts(cr); err != nil {
			return errors.Wrapf(err, "reconcile multiAZ options for arbiter in replset %s", rs.Name)
		}
	}

	if cr.CompareVersion("1.16.0") >= 0 && cr.DeletionTimestamp == nil && !cr.Spec.Pause {
		if err := rs.checkSafeDefaults(cr.Spec.Unsafe); err != nil {
			return errors.Wrap(err, "check safe defaults")
		}
	}

	if cr.CompareVersion("1.16.0") < 0 && !cr.Spec.UnsafeConf && (cr.DeletionTimestamp == nil && !cr.Spec.Pause) {
		rs.setSafeDefaults(log)
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
		log.Info("Replset is not exposed. Make sure each pod in the replset can reach each other.", "replset", rs.Name)
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

	if rs.Storage != nil && rs.Storage.Engine == StorageEngineInMemory {
		encryptionEnabled, err := rs.IsEncryptionEnabled()
		if err != nil {
			return errors.Wrap(err, "failed to parse replset configuration")
		}
		if encryptionEnabled {
			return errors.New("inMemory storage engine doesn't support encryption")
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
			Command: []string{"/data/db/mongodb-healthcheck", "k8s", "liveness"},
		}

		if cr.TLSEnabled() {
			nv.LivenessProbe.Probe.ProbeHandler.Exec.Command = append(
				nv.LivenessProbe.Probe.ProbeHandler.Exec.Command,
				"--ssl", "--sslInsecure", "--sslCAFile", "/etc/mongodb-ssl/ca.crt", "--sslPEMKeyFile", "/tmp/tls.pem",
			)
		}

		if cr.CompareVersion("1.14.0") >= 0 {
			nv.LivenessProbe.Probe.ProbeHandler.Exec.Command[0] = "/opt/percona/mongodb-healthcheck"
		}
	}
	if !nv.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
		nv.LivenessProbe.ProbeHandler.Exec.Command = append(
			nv.LivenessProbe.ProbeHandler.Exec.Command,
			startupDelaySecondsFlag, strconv.Itoa(nv.LivenessProbe.StartupDelaySeconds))
	}

	if nv.ReadinessProbe == nil {
		nv.ReadinessProbe = &corev1.Probe{}
	}

	if nv.ReadinessProbe.TCPSocket == nil && nv.ReadinessProbe.Exec == nil {
		nv.ReadinessProbe.Exec = &corev1.ExecAction{
			Command: []string{
				"/opt/percona/mongodb-healthcheck",
				"k8s", "readiness",
				"--component", "mongod",
			},
		}

		if cr.CompareVersion("1.15.0") < 0 {
			nv.ReadinessProbe.Exec = nil
			nv.ReadinessProbe.TCPSocket = &corev1.TCPSocketAction{
				Port: intstr.FromInt(int(rs.GetPort())),
			}
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

	//nolint:staticcheck
	if err := nv.MultiAZ.reconcileOpts(cr); err != nil {
		return errors.Wrapf(err, "reconcile multiAZ options for replset %s nonVoting", rs.Name)
	}

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

func (h *HiddenSpec) setLivenessProbe(cr *PerconaServerMongoDB, rs *ReplsetSpec) {
	if h.LivenessProbe == nil {
		h.LivenessProbe = new(LivenessProbeExtended)
	}
	if h.LivenessProbe.InitialDelaySeconds < 1 {
		h.LivenessProbe.InitialDelaySeconds = rs.LivenessProbe.InitialDelaySeconds
	}
	if h.LivenessProbe.TimeoutSeconds < 1 {
		h.LivenessProbe.TimeoutSeconds = rs.LivenessProbe.TimeoutSeconds
	}
	if h.LivenessProbe.PeriodSeconds < 1 {
		h.LivenessProbe.PeriodSeconds = rs.LivenessProbe.PeriodSeconds
	}
	if h.LivenessProbe.FailureThreshold < 1 {
		h.LivenessProbe.FailureThreshold = rs.LivenessProbe.FailureThreshold
	}
	if h.LivenessProbe.StartupDelaySeconds < 1 {
		h.LivenessProbe.StartupDelaySeconds = rs.LivenessProbe.StartupDelaySeconds
	}
	if h.LivenessProbe.Exec == nil {
		h.LivenessProbe.Exec = &corev1.ExecAction{
			Command: []string{"/opt/percona/mongodb-healthcheck", "k8s", "liveness"},
		}

		if cr.TLSEnabled() {
			h.LivenessProbe.Exec.Command = append(
				h.LivenessProbe.Exec.Command,
				"--ssl", "--sslInsecure", "--sslCAFile", "/etc/mongodb-ssl/ca.crt", "--sslPEMKeyFile", "/tmp/tls.pem",
			)
		}
	}
	startupDelaySecondsFlag := "--startupDelaySeconds"
	if !h.LivenessProbe.CommandHas(startupDelaySecondsFlag) {
		h.LivenessProbe.Exec.Command = append(
			h.LivenessProbe.Exec.Command,
			startupDelaySecondsFlag, strconv.Itoa(h.LivenessProbe.StartupDelaySeconds))
	}
}

func (h *HiddenSpec) setReadinessProbe(rs *ReplsetSpec) {
	if h.ReadinessProbe == nil {
		h.ReadinessProbe = &corev1.Probe{}
	}

	if h.ReadinessProbe.TCPSocket == nil && h.ReadinessProbe.Exec == nil {
		h.ReadinessProbe.Exec = &corev1.ExecAction{
			Command: []string{
				"/opt/percona/mongodb-healthcheck",
				"k8s", "readiness",
				"--component", "mongod",
			},
		}
	}
	if h.ReadinessProbe.InitialDelaySeconds < 1 {
		h.ReadinessProbe.InitialDelaySeconds = rs.ReadinessProbe.InitialDelaySeconds
	}
	if h.ReadinessProbe.TimeoutSeconds < 1 {
		h.ReadinessProbe.TimeoutSeconds = rs.ReadinessProbe.TimeoutSeconds
	}
	if h.ReadinessProbe.PeriodSeconds < 1 {
		h.ReadinessProbe.PeriodSeconds = rs.ReadinessProbe.PeriodSeconds
	}
	if h.ReadinessProbe.FailureThreshold < 1 {
		h.ReadinessProbe.FailureThreshold = rs.ReadinessProbe.FailureThreshold
	}
}

func (h *HiddenSpec) SetDefaults(cr *PerconaServerMongoDB, rs *ReplsetSpec) error {
	if !h.Enabled {
		return nil
	}

	if h.VolumeSpec != nil {
		if err := h.VolumeSpec.reconcileOpts(); err != nil {
			return errors.Wrapf(err, "reconcile volumes for replset %s nonVoting", rs.Name)
		}
	} else {
		h.VolumeSpec = rs.VolumeSpec
	}

	h.setLivenessProbe(cr, rs)
	h.setReadinessProbe(rs)

	if len(h.ServiceAccountName) == 0 {
		h.ServiceAccountName = WorkloadSA
	}

	//nolint:staticcheck
	if err := h.MultiAZ.reconcileOpts(cr); err != nil {
		return errors.Wrapf(err, "reconcile multiAZ options for replset %s-hidden", rs.Name)
	}

	if h.ContainerSecurityContext == nil {
		h.ContainerSecurityContext = rs.ContainerSecurityContext
	}

	if h.PodSecurityContext == nil {
		h.PodSecurityContext = rs.PodSecurityContext
	}

	if err := h.Configuration.SetDefaults(); err != nil {
		return errors.Wrap(err, "failed to set configuration defaults")
	}

	return nil
}

func (rs *ReplsetSpec) setSafeDefaults(log logr.Logger) {
	if rs.Arbiter.Enabled {
		if rs.Arbiter.Size != 1 {
			log.Info("Setting safe defaults, updating arbiter size", "oldSize", rs.Arbiter.Size, "newSize", 1)
			rs.Arbiter.Size = 1
		}
		if rs.Size < minSafeReplicasetSizeWithArbiter {
			log.Info("Setting safe defaults, updating replset size",
				"oldSize", rs.Size, "newSize", minSafeReplicasetSizeWithArbiter)
			rs.Size = minSafeReplicasetSizeWithArbiter
		}
		if rs.Size%2 != 0 {
			log.Info("Setting safe defaults, disabling arbiter due to odd replset size", "size", rs.Size)
			rs.Arbiter.Enabled = false
			rs.Arbiter.Size = 0
		}
	} else {
		if rs.Size < 2 {
			log.Info("Setting safe defaults, updating replset size to meet the minimum number of replicas",
				"oldSize", rs.Size, "newSize", defaultMongodSize)
			rs.Size = defaultMongodSize
		}
		if rs.Size%2 == 0 {
			log.Info("Setting safe defaults, increasing replset size to have a odd number of replicas",
				"oldSize", rs.Size, "newSize", rs.Size+1)
			rs.Size++
		}
	}
}

func (rs *ReplsetSpec) checkSafeDefaults(unsafe UnsafeFlags) error {
	if !unsafe.ReplsetSize {
		if rs.Arbiter.Enabled {
			if rs.Arbiter.Size != 1 {
				return errors.New("arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check")
			}
			if rs.Size < minSafeReplicasetSizeWithArbiter {
				return errors.Errorf("replset size must be at least %d with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check", minSafeReplicasetSizeWithArbiter)
			}
			if rs.Size%2 != 0 {
				return errors.New("arbiter must disabled due to odd replset size. Set spec.unsafeFlags.replsetSize to true to disable this check")
			}
		} else {
			if rs.Size < 2 {
				return errors.Errorf("replset size must be at least %d. Set spec.unsafeFlags.replsetSize to true to disable this check", defaultMongodSize)
			}
			if rs.Size%2 == 0 {
				return errors.New("replset size must be odd. Set spec.unsafeFlags.replsetSize to true to disable this check")
			}
		}
	}

	mode, err := rs.Configuration.GetTLSMode()
	if err != nil {
		return errors.Wrap(err, "get tls mode")
	}

	if mode != "" {
		return errors.New("tlsMode must be set using spec.tls.mode")
	}

	return nil
}

func (m *MultiAZ) reconcileOpts(cr *PerconaServerMongoDB) error {
	m.reconcileAffinityOpts(cr)
	m.reconcileTopologySpreadConstraints(cr)
	if cr.CompareVersion("1.15.0") >= 0 {
		if m.TerminationGracePeriodSeconds == nil {
			m.TerminationGracePeriodSeconds = new(int64)
			*m.TerminationGracePeriodSeconds = 60
		}
	}
	if cr.CompareVersion("1.15.0") == 0 {
		if !cr.Spec.UnsafeConf && *m.TerminationGracePeriodSeconds < 30 {
			m.TerminationGracePeriodSeconds = new(int64)
			*m.TerminationGracePeriodSeconds = 60
		}
	}
	if cr.CompareVersion("1.16.0") >= 0 {
		if *m.TerminationGracePeriodSeconds < 30 && !cr.Spec.Unsafe.TerminationGracePeriod {
			return errors.New("terminationGracePeriodSeconds must be at least 30 seconds for safe configuration. Set spec.unsafeFlags.terminationGracePeriod to true to disable this check")
		}
	}
	if m.PodDisruptionBudget == nil {
		defaultMaxUnavailable := intstr.FromInt(1)
		m.PodDisruptionBudget = &PodDisruptionBudgetSpec{MaxUnavailable: &defaultMaxUnavailable}
	}

	return nil
}

var affinityValidTopologyKeys = map[string]struct{}{
	AffinityOff:                     {},
	"kubernetes.io/hostname":        {},
	"topology.kubernetes.io/zone":   {},
	"topology.kubernetes.io/region": {},
}

var defaultAffinityTopologyKey = "kubernetes.io/hostname"

const AffinityOff = "none"

// reconcileAffinityOpts ensures that the affinity is set to the valid values.
// - if the affinity doesn't set at all - set topology key to `defaultAffinityTopologyKey`
// - if topology key is set and the value not the one of `affinityValidTopologyKeys` - set to `defaultAffinityTopologyKey`
// - if topology key set to valuse of `AffinityOff` - disable the affinity at all
// - if `Advanced` affinity is set - leave everything as it is and set topology key to nil (Advanced options has a higher priority)
func (m *MultiAZ) reconcileAffinityOpts(cr *PerconaServerMongoDB) {
	if cr.CompareVersion("1.16.0") < 0 {
		affinityValidTopologyKeys = map[string]struct{}{
			AffinityOff:                                {},
			"kubernetes.io/hostname":                   {},
			"failure-domain.beta.kubernetes.io/zone":   {},
			"failure-domain.beta.kubernetes.io/region": {},
		}
	}
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

func (m *MultiAZ) reconcileTopologySpreadConstraints(cr *PerconaServerMongoDB) {
	if cr.CompareVersion("1.15.0") < 0 {
		return
	}

	for i := range m.TopologySpreadConstraints {
		if m.TopologySpreadConstraints[i].MaxSkew == 0 {
			m.TopologySpreadConstraints[i].MaxSkew = 1
		}
		if m.TopologySpreadConstraints[i].TopologyKey == "" {
			m.TopologySpreadConstraints[i].TopologyKey = defaultAffinityTopologyKey
		}
		if m.TopologySpreadConstraints[i].WhenUnsatisfiable == "" {
			m.TopologySpreadConstraints[i].WhenUnsatisfiable = corev1.DoNotSchedule
		}
	}
}

func (v *VolumeSpec) reconcileOpts() error {
	if v.EmptyDir == nil && v.HostPath == nil && v.PersistentVolumeClaim.PersistentVolumeClaimSpec == nil {
		v.PersistentVolumeClaim.PersistentVolumeClaimSpec = &corev1.PersistentVolumeClaimSpec{}
	}

	if v.PersistentVolumeClaim.PersistentVolumeClaimSpec != nil {
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
