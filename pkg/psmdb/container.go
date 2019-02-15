package psmdb

import (
	"fmt"
	"math"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

func container(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, name string, resources corev1.ResourceRequirements, runUID *int64, ikeyName string) corev1.Container {
	fvar := false
	tvar := true

	return corev1.Container{
		Name:            name,
		Image:           m.Spec.Image,
		ImagePullPolicy: m.Spec.ImagePullPolicy,
		Args:            containerArgs(m, replset, resources),
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      m.Spec.Mongod.Net.HostPort,
				ContainerPort: m.Spec.Mongod.Net.Port,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "SERVICE_NAME",
				Value: m.Name,
			},
			{
				Name:  "NAMESPACE",
				Value: m.Namespace,
			},
			{
				Name:  "MONGODB_PORT",
				Value: strconv.Itoa(int(m.Spec.Mongod.Net.Port)),
			},
			{
				Name:  "MONGODB_REPLSET",
				Value: replset.Name,
			},
		},
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.Spec.Secrets.Users,
					},
					Optional: &fvar,
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
		Resources: resources,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot: &tvar,
			RunAsUser:    runUID,
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      ikeyName,
				MountPath: mongodSecretsDir,
				ReadOnly:  true,
			},
		},
	}
}

// containerArgs returns the args to pass to the mSpec container
func containerArgs(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, resources corev1.ResourceRequirements) []string {
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
	case api.ClusterRoleConfigSvr:
		args = append(args, "--configsvr")
	case api.ClusterRoleShardSvr:
		args = append(args, "--shardsvr")
	}

	// operationProfiling
	if mSpec.OperationProfiling != nil {
		switch mSpec.OperationProfiling.Mode {
		case api.OperationProfilingModeAll:
			args = append(args, "--profile=2")
		case api.OperationProfilingModeSlowOp:
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
		case api.StorageEngineWiredTiger:
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
		case api.StorageEngineInMemory:
			args = append(args, fmt.Sprintf(
				"--inMemorySizeGB=%.2f",
				getWiredTigerCacheSizeGB(resources.Limits, mSpec.Storage.InMemory.EngineConfig.InMemorySizeRatio, false),
			))
		case api.StorageEngineMMAPv1:
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
	if mSpec.AuditLog != nil && mSpec.AuditLog.Destination == api.AuditLogDestinationFile {
		if mSpec.AuditLog.Filter == "" {
			mSpec.AuditLog.Filter = "{}"
		}
		args = append(args,
			"--auditDestination=file",
			"--auditFilter="+mSpec.AuditLog.Filter,
			"--auditFormat="+string(mSpec.AuditLog.Format),
		)
		switch mSpec.AuditLog.Format {
		case api.AuditLogFormatBSON:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.bson")
		default:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.json")
		}
	}

	return args
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
