package stub

import (
	"fmt"
	"math"
	"strconv"

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
)

// getPSMDBDockerImageName returns the prefix for the Dockerhub image name.
// This image name should be in the following format:
// perconalab/percona-server-mongodb-operator:<VERSION>-mongod<PSMDB-VERSION>
func getPSMDBDockerImageName(m *v1alpha1.PerconaServerMongoDB) string {
	return "perconalab/percona-server-mongodb-operator:" + version.Version + "-mongod" + m.Spec.Version
}

// getMongodPort returns the mongod port number as a string
func getMongodPort(container *corev1.Container) string {
	for _, port := range container.Ports {
		if port.Name == mongodPortName {
			return strconv.Itoa(int(port.ContainerPort))
		}
	}
	return ""
}

// isContainerAndPodRunning returns a boolean reflecting if
// a container and pod are in a running state
func isContainerAndPodRunning(pod corev1.Pod, containerName string) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == containerName && container.State.Running != nil {
			return true
		}
	}
	return false
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

// newPSMDBContainerEnv returns environment variables for a container
func newPSMDBContainerEnv(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) []corev1.EnvVar {
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

// newPSMDBMongodContainerArgs returns the args to pass to the mongod container
func newPSMDBMongodContainerArgs(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources *corev1.ResourceRequirements) []string {
	mongod := m.Spec.Mongod
	args := []string{
		"--bind_ip_all",
		"--auth",
		"--dbpath=" + mongodContainerDataDir,
		"--keyFile=" + mongodContainerDataDir + "/.mongod.key",
		"--port=" + strconv.Itoa(int(mongod.Net.Port)),
		"--replSet=" + replset.Name,
		"--storageEngine=" + string(mongod.Storage.Engine),
	}

	// sharding
	switch replset.ClusterRole {
	case v1alpha1.ClusterRoleConfigSvr:
		args = append(args, "--configsvr")
	case v1alpha1.ClusterRoleShardSvr:
		args = append(args, "--shardsvr")
	}

	// operationProfiling
	if mongod.OperationProfiling != nil {
		switch mongod.OperationProfiling.Mode {
		case v1alpha1.OperationProfilingModeAll:
			args = append(args, "--profile=2")
		case v1alpha1.OperationProfilingModeSlowOp:
			args = append(args,
				"--slowms="+strconv.Itoa(int(mongod.OperationProfiling.SlowOpThresholdMs)),
				"--profile=1",
			)
		}
		if mongod.OperationProfiling.RateLimit > 0 {
			args = append(args, "--rateLimit="+strconv.Itoa(mongod.OperationProfiling.RateLimit))
		}
	}

	// storage
	if mongod.Storage != nil {
		switch mongod.Storage.Engine {
		case v1alpha1.StorageEngineWiredTiger:
			args = append(args, fmt.Sprintf(
				"--wiredTigerCacheSizeGB=%.2f",
				getWiredTigerCacheSizeGB(resources.Limits, mongod.Storage.WiredTiger.EngineConfig.CacheSizeRatio, true),
			))
			if mongod.Storage.WiredTiger.CollectionConfig != nil {
				if mongod.Storage.WiredTiger.CollectionConfig.BlockCompressor != nil {
					args = append(args,
						"--wiredTigerCollectionBlockCompressor="+string(*mongod.Storage.WiredTiger.CollectionConfig.BlockCompressor),
					)
				}
			}
			if mongod.Storage.WiredTiger.EngineConfig != nil {
				if mongod.Storage.WiredTiger.EngineConfig.JournalCompressor != nil {
					args = append(args,
						"--wiredTigerJournalCompressor="+string(*mongod.Storage.WiredTiger.EngineConfig.JournalCompressor),
					)
				}
				if mongod.Storage.WiredTiger.EngineConfig.DirectoryForIndexes {
					args = append(args, "--wiredTigerDirectoryForIndexes")
				}
			}
		case v1alpha1.StorageEngineInMemory:
			args = append(args, fmt.Sprintf(
				"--inMemorySizeGB=%.2f",
				getWiredTigerCacheSizeGB(resources.Limits, mongod.Storage.InMemory.EngineConfig.InMemorySizeRatio, false),
			))
		case v1alpha1.StorageEngineMMAPv1:
			if mongod.Storage.MMAPv1.NsSize > 0 {
				args = append(args, "--nssize="+strconv.Itoa(mongod.Storage.MMAPv1.NsSize))
			}
			if mongod.Storage.MMAPv1.Smallfiles {
				args = append(args, "--smallfiles")
			}
		}
		if mongod.Storage.DirectoryPerDB {
			args = append(args, "--directoryperdb")
		}
		if mongod.Storage.SyncPeriodSecs > 0 {
			args = append(args, "--syncdelay="+strconv.Itoa(mongod.Storage.SyncPeriodSecs))
		}
	}

	// security
	if mongod.Security != nil && mongod.Security.RedactClientLogData {
		args = append(args, "--redactClientLogData")
	}

	// replication
	if mongod.Replication != nil && mongod.Replication.OplogSizeMB > 0 {
		args = append(args, "--oplogSize="+strconv.Itoa(mongod.Replication.OplogSizeMB))
	}

	// setParameter
	if mongod.SetParameter != nil {
		if mongod.SetParameter.TTLMonitorSleepSecs > 0 {
			args = append(args,
				"--setParameter",
				"ttlMonitorSleepSecs="+strconv.Itoa(mongod.SetParameter.TTLMonitorSleepSecs),
			)
		}
		if mongod.SetParameter.WiredTigerConcurrentReadTransactions > 0 {
			args = append(args,
				"--setParameter",
				"wiredTigerConcurrentReadTransactions="+strconv.Itoa(mongod.SetParameter.WiredTigerConcurrentReadTransactions),
			)
		}
		if mongod.SetParameter.WiredTigerConcurrentWriteTransactions > 0 {
			args = append(args,
				"--setParameter",
				"wiredTigerConcurrentWriteTransactions="+strconv.Itoa(mongod.SetParameter.WiredTigerConcurrentWriteTransactions),
			)
		}
	}

	// auditLog
	if mongod.AuditLog != nil && mongod.AuditLog.Destination == v1alpha1.AuditLogDestinationFile {
		if mongod.AuditLog.Filter == "" {
			mongod.AuditLog.Filter = "{}"
		}
		args = append(args,
			"--auditDestination=file",
			"--auditFilter="+mongod.AuditLog.Filter,
			"--auditFormat="+string(mongod.AuditLog.Format),
		)
		switch mongod.AuditLog.Format {
		case v1alpha1.AuditLogFormatBSON:
			args = append(args, "--auditPath="+mongodContainerDataDir+"/auditLog.bson")
		default:
			args = append(args, "--auditPath="+mongodContainerDataDir+"/auditLog.json")
		}
	}

	return args
}

// GetContainerRunUID returns an int64-pointer reflecting the user ID a container
// should run as
func GetContainerRunUID(m *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion) *int64 {
	if getPlatform(m, serverVersion) != v1alpha1.PlatformOpenshift {
		return &m.Spec.RunUID
	}
	return nil
}

func (h *Handler) newPSMDBMongodContainer(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, resources *corev1.ResourceRequirements) corev1.Container {
	return corev1.Container{
		Name:            mongodContainerName,
		Image:           getPSMDBDockerImageName(m),
		ImagePullPolicy: m.Spec.ImagePullPolicy,
		Args:            newPSMDBMongodContainerArgs(m, replset, resources),
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      m.Spec.Mongod.Net.HostPort,
				ContainerPort: m.Spec.Mongod.Net.Port,
			},
		},
		Env: newPSMDBContainerEnv(m, replset),
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.Spec.Secrets.Users,
					},
					Optional: &falseVar,
				},
			},
		},
		WorkingDir: mongodContainerDataDir,
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
			InitialDelaySeconds: int32(45),
			TimeoutSeconds:      int32(2),
			PeriodSeconds:       int32(5),
			FailureThreshold:    int32(5),
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
		Resources: corev1.ResourceRequirements{
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resources.Limits[corev1.ResourceCPU],
				corev1.ResourceMemory: resources.Limits[corev1.ResourceMemory],
			},
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resources.Requests[corev1.ResourceCPU],
				corev1.ResourceMemory: resources.Requests[corev1.ResourceMemory],
			},
		},
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &trueVar,
			RunAsUser:    GetContainerRunUID(m, h.serverVersion),
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      mongodDataVolClaimName,
				MountPath: mongodContainerDataDir,
			},
			{
				Name:      m.Spec.Secrets.Key,
				MountPath: mongoDBSecretsDir,
				ReadOnly:  true,
			},
		},
	}
}
