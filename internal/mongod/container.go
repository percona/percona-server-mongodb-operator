package mongod

import (
	"fmt"
	"math"
	"strconv"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/version"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	k8sPod "github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

const (
	gigaByte                 int64   = 1024 * 1024 * 1024
	minWiredTigerCacheSizeGB float64 = 0.25

	MongodContainerDataDir     = "/data/db"
	MongodContainerName        = "mongod"
	MongodArbiterContainerName = "mongod-arbiter"
	MongodBackupContainerName  = "mongod-backup"
	MongodDataVolClaimName     = "mongod-data"
	MongodPortName             = "mongodb"
	MongodSecretsDir           = "/etc/mongodb-secrets"
)

// GetPSMDBDockerImageName returns the prefix for the Dockerhub image name.
// This image name should be in the following format:
// percona/percona-server-mongodb-operator:<VERSION>-mongod<PSMDB-VERSION>
func GetPSMDBDockerImageName(m *v1alpha1.PerconaServerMongoDB) string {
	return "percona/percona-server-mongodb-operator:" + version.Version + "-mongod" + m.Spec.Version
}

// GetMongodPort returns the mongod port number as a string
func GetMongodPort(container *corev1.Container) string {
	for _, port := range container.Ports {
		if port.Name == MongodPortName {
			return strconv.Itoa(int(port.ContainerPort))
		}
	}
	return ""
}

// The WiredTiger internal cache, by default, will use the larger of either 50% of
// (RAM - 1 GB), or 256 MB. For example, on a system with a total of 4GB of RAM the
// WiredTiger cache will use 1.5GB of RAM (0.5 * (4 GB - 1 GB) = 1.5 GB).
//
// In normal situations WiredTiger does this default-sizing correctly but under Docker
// containers WiredTiger fails to detect the memory limit of the Docker container. We
// explicitly set the WiredTiger cache size to fix this.
//
// https://docs.mongodb.com/manual/reference/configuration-options/#storage.wiredTiger.engineConfig.cacheSizeGB
//
func getWiredTigerCacheSizeGB(resourceList corev1.ResourceList, cacheRatio float64, subtract1GB bool) float64 {
	maxMemory := resourceList[corev1.ResourceMemory]
	var size float64
	if subtract1GB {
		size = math.Floor(cacheRatio * float64(maxMemory.Value()-gigaByte))
	} else {
		size = math.Floor(cacheRatio * float64(maxMemory.Value()))
	}
	sizeGB := size / float64(gigaByte)
	if sizeGB < minWiredTigerCacheSizeGB {
		sizeGB = minWiredTigerCacheSizeGB
	}
	return sizeGB
}

// newContainerEnv returns environment variables for a container
func newContainerEnv(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) []corev1.EnvVar {
	mSpec := m.Spec.Mongod
	return []corev1.EnvVar{
		{
			Name:  motPkg.EnvServiceName,
			Value: m.Name,
		},
		{
			Name:  k8sPod.EnvNamespace,
			Value: m.Namespace,
		},
		{
			Name:  motPkg.EnvMongoDBPort,
			Value: strconv.Itoa(int(mSpec.Net.Port)),
		},
		{
			Name:  motPkg.EnvMongoDBReplset,
			Value: replset.Name,
		},
	}
}

// NewContainerArgs returns the args to pass to the mSpec container
func NewContainerArgs(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements) []string {
	mSpec := m.Spec.Mongod
	args := []string{
		"--bind_ip_all",
		"--auth",
		"--dbpath=" + MongodContainerDataDir,
		"--port=" + strconv.Itoa(int(mSpec.Net.Port)),
		"--replSet=" + replset.Name,
		"--storageEngine=" + string(mSpec.Storage.Engine),
	}

	// sharding
	switch replset.ClusterRole {
	case v1alpha1.ClusterRoleConfigSvr:
		args = append(args, "--configsvr")
	case v1alpha1.ClusterRoleShardSvr:
		args = append(args, "--shardsvr")
	}

	// operationProfiling
	if mSpec.OperationProfiling != nil {
		switch mSpec.OperationProfiling.Mode {
		case v1alpha1.OperationProfilingModeAll:
			args = append(args, "--profile=2")
		case v1alpha1.OperationProfilingModeSlowOp:
			args = append(args,
				"--slowms="+strconv.Itoa(int(mSpec.OperationProfiling.SlowOpThresholdMs)),
				"--profile=1",
			)
		}
		if mSpec.OperationProfiling.RateLimit > 0 {
			args = append(args, "--rateLimit="+strconv.Itoa(mSpec.OperationProfiling.RateLimit))
		}
	}

	// storage
	if mSpec.Storage != nil {
		switch mSpec.Storage.Engine {
		case v1alpha1.StorageEngineWiredTiger:
			if limit, ok := resources.Limits[corev1.ResourceCPU]; ok && !limit.IsZero() {
				args = append(args, fmt.Sprintf(
					"--wiredTigerCacheSizeGB=%.2f",
					getWiredTigerCacheSizeGB(resources.Limits, mSpec.Storage.WiredTiger.EngineConfig.CacheSizeRatio, true),
				))
			}
			if mSpec.Storage.WiredTiger.CollectionConfig != nil {
				if mSpec.Storage.WiredTiger.CollectionConfig.BlockCompressor != nil {
					args = append(args,
						"--wiredTigerCollectionBlockCompressor="+string(*mSpec.Storage.WiredTiger.CollectionConfig.BlockCompressor),
					)
				}
			}
			if mSpec.Storage.WiredTiger.EngineConfig != nil {
				if mSpec.Storage.WiredTiger.EngineConfig.JournalCompressor != nil {
					args = append(args,
						"--wiredTigerJournalCompressor="+string(*mSpec.Storage.WiredTiger.EngineConfig.JournalCompressor),
					)
				}
				if mSpec.Storage.WiredTiger.EngineConfig.DirectoryForIndexes {
					args = append(args, "--wiredTigerDirectoryForIndexes")
				}
			}
			if mSpec.Storage.WiredTiger.IndexConfig != nil && mSpec.Storage.WiredTiger.IndexConfig.PrefixCompression {
				args = append(args, "--wiredTigerIndexPrefixCompression=true")
			}
		case v1alpha1.StorageEngineInMemory:
			args = append(args, fmt.Sprintf(
				"--inMemorySizeGB=%.2f",
				getWiredTigerCacheSizeGB(resources.Limits, mSpec.Storage.InMemory.EngineConfig.InMemorySizeRatio, false),
			))
		case v1alpha1.StorageEngineMMAPv1:
			if mSpec.Storage.MMAPv1.NsSize > 0 {
				args = append(args, "--nssize="+strconv.Itoa(mSpec.Storage.MMAPv1.NsSize))
			}
			if mSpec.Storage.MMAPv1.Smallfiles {
				args = append(args, "--smallfiles")
			}
		}
		if mSpec.Storage.DirectoryPerDB {
			args = append(args, "--directoryperdb")
		}
		if mSpec.Storage.SyncPeriodSecs > 0 {
			args = append(args, "--syncdelay="+strconv.Itoa(mSpec.Storage.SyncPeriodSecs))
		}
	}

	// security
	if mSpec.Security != nil && mSpec.Security.RedactClientLogData {
		args = append(args, "--redactClientLogData")
	}

	// replication
	if mSpec.Replication != nil && mSpec.Replication.OplogSizeMB > 0 {
		args = append(args, "--oplogSize="+strconv.Itoa(mSpec.Replication.OplogSizeMB))
	}

	// setParameter
	if mSpec.SetParameter != nil {
		if mSpec.SetParameter.TTLMonitorSleepSecs > 0 {
			args = append(args,
				"--setParameter",
				"ttlMonitorSleepSecs="+strconv.Itoa(mSpec.SetParameter.TTLMonitorSleepSecs),
			)
		}
		if mSpec.SetParameter.WiredTigerConcurrentReadTransactions > 0 {
			args = append(args,
				"--setParameter",
				"wiredTigerConcurrentReadTransactions="+strconv.Itoa(mSpec.SetParameter.WiredTigerConcurrentReadTransactions),
			)
		}
		if mSpec.SetParameter.WiredTigerConcurrentWriteTransactions > 0 {
			args = append(args,
				"--setParameter",
				"wiredTigerConcurrentWriteTransactions="+strconv.Itoa(mSpec.SetParameter.WiredTigerConcurrentWriteTransactions),
			)
		}
	}

	// auditLog
	if mSpec.AuditLog != nil && mSpec.AuditLog.Destination == v1alpha1.AuditLogDestinationFile {
		if mSpec.AuditLog.Filter == "" {
			mSpec.AuditLog.Filter = "{}"
		}
		args = append(args,
			"--auditDestination=file",
			"--auditFilter="+mSpec.AuditLog.Filter,
			"--auditFormat="+string(mSpec.AuditLog.Format),
		)
		switch mSpec.AuditLog.Format {
		case v1alpha1.AuditLogFormatBSON:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.bson")
		default:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.json")
		}
	}

	return args
}

func newContainerVolumeMounts(m *v1alpha1.PerconaServerMongoDB) []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      MongodDataVolClaimName,
			MountPath: MongodContainerDataDir,
		},
		{
			Name:      m.Spec.Secrets.Key,
			MountPath: MongodSecretsDir,
			ReadOnly:  true,
		},
	}
}

func newContainer(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, name string, resources corev1.ResourceRequirements, runUID *int64, vms []corev1.VolumeMount) corev1.Container {
	return corev1.Container{
		Name:            name,
		Image:           GetPSMDBDockerImageName(m),
		ImagePullPolicy: m.Spec.ImagePullPolicy,
		Args:            NewContainerArgs(m, replset, resources),
		Ports: []corev1.ContainerPort{
			{
				Name:          MongodPortName,
				HostPort:      m.Spec.Mongod.Net.HostPort,
				ContainerPort: m.Spec.Mongod.Net.Port,
			},
		},
		Env: newContainerEnv(m, replset),
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.Spec.Secrets.Users,
					},
					Optional: &util.FalseVar,
				},
			},
		},
		WorkingDir: MongodContainerDataDir,
		LivenessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"mongodb-healthcheck",
						"k8s",
						"liveness",
					},
				},
			},
			InitialDelaySeconds: int32(60),
			TimeoutSeconds:      int32(5),
			PeriodSeconds:       int32(10),
			FailureThreshold:    int32(12),
		},
		ReadinessProbe: &corev1.Probe{
			Handler: corev1.Handler{
				TCPSocket: &corev1.TCPSocketAction{
					Port: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
				},
			},
			InitialDelaySeconds: int32(10),
			TimeoutSeconds:      int32(2),
			PeriodSeconds:       int32(3),
			FailureThreshold:    int32(8),
		},
		Resources: util.GetContainerResourceRequirements(resources),
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &util.TrueVar,
			RunAsUser:    runUID,
		},
		VolumeMounts: vms,
	}
}

func NewContainer(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements, runUID *int64) corev1.Container {
	return newContainer(m, replset, MongodContainerName, resources, runUID, newContainerVolumeMounts(m))
}

func NewArbiterContainer(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources corev1.ResourceRequirements, runUID *int64) corev1.Container {
	// TODO reduce the resources consumption for arbiter
	return newContainer(m, replset, MongodArbiterContainerName, resources, runUID,
		[]corev1.VolumeMount{
			{
				Name:      m.Spec.Secrets.Key,
				MountPath: MongodSecretsDir,
				ReadOnly:  true,
			},
		},
	)
}
