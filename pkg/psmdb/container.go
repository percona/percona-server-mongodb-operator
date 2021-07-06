package psmdb

import (
	"fmt"
	"math"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func container(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, name string, resources corev1.ResourceRequirements,
	ikeyName string, useConfigFile bool) (corev1.Container, error) {
	fvar := false

	volumes := []corev1.VolumeMount{
		{
			Name:      MongodDataVolClaimName,
			MountPath: MongodContainerDataDir,
		},
		{
			Name:      ikeyName,
			MountPath: mongodSecretsDir,
			ReadOnly:  true,
		},
		{
			Name:      "ssl",
			MountPath: sslDir,
			ReadOnly:  true,
		},
		{
			Name:      "ssl-internal",
			MountPath: sslInternalDir,
			ReadOnly:  true,
		},
	}

	if useConfigFile {
		volumes = append(volumes, corev1.VolumeMount{
			Name:      "config",
			MountPath: mongodConfigDir,
		})
	}

	if *cr.Spec.Mongod.Security.EnableEncryption {
		volumes = append(volumes,
			corev1.VolumeMount{
				Name:      cr.Spec.Mongod.Security.EncryptionKeySecret,
				MountPath: mongodRESTencryptDir,
				ReadOnly:  true,
			},
		)
	}
	if cr.CompareVersion("1.8.0") >= 0 {
		volumes = append(volumes, corev1.VolumeMount{
			Name:      "users-secret-file",
			MountPath: "/etc/users-secret",
		})
	}
	container := corev1.Container{
		Name:            name,
		Image:           cr.Spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Args:            containerArgs(cr, replset, resources, useConfigFile),
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      cr.Spec.Mongod.Net.HostPort,
				ContainerPort: cr.Spec.Mongod.Net.Port,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "SERVICE_NAME",
				Value: cr.Name,
			},
			{
				Name:  "NAMESPACE",
				Value: cr.Namespace,
			},
			{
				Name:  "MONGODB_PORT",
				Value: strconv.Itoa(int(cr.Spec.Mongod.Net.Port)),
			},
		},
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cr.Spec.Secrets.Users,
					},
					Optional: &fvar,
				},
			},
		},
		WorkingDir:      MongodContainerDataDir,
		LivenessProbe:   &replset.LivenessProbe.Probe,
		ReadinessProbe:  replset.ReadinessProbe,
		Resources:       resources,
		SecurityContext: replset.ContainerSecurityContext,
		VolumeMounts:    volumes,
	}

	if cr.CompareVersion("1.5.0") >= 0 {
		container.EnvFrom = []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: api.InternalUserSecretName(cr),
					},
					Optional: &fvar,
				},
			},
		}
		container.Command = []string{"/data/db/ps-entry.sh"}
	}

	if !cr.Spec.Unmanaged {
		container.Env = append(container.Env, corev1.EnvVar{Name: "MONGODB_REPLSET", Value: replset.Name})
	}

	return container, nil
}

// containerArgs returns the args to pass to the mSpec container
func containerArgs(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, resources corev1.ResourceRequirements,
	useConfigFile bool) []string {
	mSpec := m.Spec.Mongod
	// TODO(andrew): in the safe mode `sslAllowInvalidCertificates` should be set only with the external services
	args := []string{
		"--bind_ip_all",
		"--auth",
		"--dbpath=" + MongodContainerDataDir,
		"--port=" + strconv.Itoa(int(mSpec.Net.Port)),
		"--replSet=" + replset.Name,
		"--storageEngine=" + string(replset.Storage.Engine),
		"--relaxPermChecks",
		"--sslAllowInvalidCertificates",
	}

	if m.Spec.UnsafeConf {
		args = append(args,
			"--clusterAuthMode=keyFile",
			"--keyFile="+mongodSecretsDir+"/mongodb-key",
		)
	} else {
		args = append(args,
			"--sslMode=preferSSL",
			"--clusterAuthMode=x509",
		)
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
	if replset.Storage != nil {
		switch replset.Storage.Engine {
		case api.StorageEngineWiredTiger:
			if *m.Spec.Mongod.Security.EnableEncryption {
				args = append(args,
					"--enableEncryption",
					"--encryptionKeyFile="+mongodRESTencryptDir+"/"+EncryptionKeyName,
				)
				if m.Spec.Mongod.Security.EncryptionCipherMode != api.MongodChiperModeUnset {
					args = append(args,
						"--encryptionCipherMode="+string(m.Spec.Mongod.Security.EncryptionCipherMode),
					)
				}
			}
			if limit, ok := resources.Limits[corev1.ResourceCPU]; ok && !limit.IsZero() {
				args = append(args, fmt.Sprintf(
					"--wiredTigerCacheSizeGB=%.2f",
					getWiredTigerCacheSizeGB(resources.Limits, replset.Storage.WiredTiger.EngineConfig.CacheSizeRatio, true),
				))
			}
			if replset.Storage.WiredTiger.CollectionConfig != nil {
				if replset.Storage.WiredTiger.CollectionConfig.BlockCompressor != nil {
					args = append(args,
						"--wiredTigerCollectionBlockCompressor="+string(*replset.Storage.WiredTiger.CollectionConfig.BlockCompressor),
					)
				}
			}
			if replset.Storage.WiredTiger.EngineConfig != nil {
				if replset.Storage.WiredTiger.EngineConfig.JournalCompressor != nil {
					args = append(args,
						"--wiredTigerJournalCompressor="+string(*replset.Storage.WiredTiger.EngineConfig.JournalCompressor),
					)
				}
				if replset.Storage.WiredTiger.EngineConfig.DirectoryForIndexes {
					args = append(args, "--wiredTigerDirectoryForIndexes")
				}
			}
			if replset.Storage.WiredTiger.IndexConfig != nil && replset.Storage.WiredTiger.IndexConfig.PrefixCompression {
				args = append(args, "--wiredTigerIndexPrefixCompression=true")
			}
		case api.StorageEngineInMemory:
			args = append(args, fmt.Sprintf(
				"--inMemorySizeGB=%.2f",
				getWiredTigerCacheSizeGB(resources.Limits, replset.Storage.InMemory.EngineConfig.InMemorySizeRatio, false),
			))
		}
		if replset.Storage.DirectoryPerDB {
			args = append(args, "--directoryperdb")
		}
		if replset.Storage.SyncPeriodSecs > 0 {
			args = append(args, "--syncdelay="+strconv.Itoa(replset.Storage.SyncPeriodSecs))
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
		if mSpec.SetParameter.CursorTimeoutMillis > 0 {
			args = append(args,
				"--setParameter",
				"cursorTimeoutMillis="+strconv.Itoa(mSpec.SetParameter.CursorTimeoutMillis),
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

	if useConfigFile {
		args = append(args, fmt.Sprintf("--config=%s/mongod.conf", mongodConfigDir))
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
