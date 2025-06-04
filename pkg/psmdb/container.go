package psmdb

import (
	"context"
	"fmt"
	"math"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func container(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, name string, resources corev1.ResourceRequirements,
	ikeyName string, useConfigFile bool, livenessProbe *api.LivenessProbeExtended, readinessProbe *corev1.Probe,
	containerSecurityContext *corev1.SecurityContext,
) (corev1.Container, error) {
	fvar := false

	volumes := []corev1.VolumeMount{
		{
			Name:      config.MongodDataVolClaimName,
			MountPath: config.MongodContainerDataDir,
		},
		{
			Name:      ikeyName,
			MountPath: config.MongodSecretsDir,
			ReadOnly:  true,
		},
		{
			Name:      "ssl",
			MountPath: config.SSLDir,
			ReadOnly:  true,
		},
		{
			Name:      "ssl-internal",
			MountPath: config.SSLInternalDir,
			ReadOnly:  true,
		},
	}

	if cr.CompareVersion("1.9.0") >= 0 && useConfigFile {
		volumes = append(volumes, corev1.VolumeMount{
			Name:      "config",
			MountPath: config.MongodConfigDir,
		})
	}

	if cr.CompareVersion("1.14.0") >= 0 {
		volumes = append(volumes, corev1.VolumeMount{Name: config.BinVolumeName, MountPath: config.BinMountPath})
	}

	if cr.CompareVersion("1.16.0") >= 0 && cr.Spec.Secrets.LDAPSecret != "" {
		volumes = append(volumes, []corev1.VolumeMount{
			{
				Name:      config.LDAPTLSVolClaimName,
				MountPath: config.LDAPTLSDir,
				ReadOnly:  true,
			},
			{
				Name:      config.LDAPConfVolClaimName,
				MountPath: config.LDAPConfDir,
			},
		}...)
	}

	encryptionEnabled, err := replset.IsEncryptionEnabled()
	if err != nil {
		return corev1.Container{}, err
	}
	if encryptionEnabled {
		if len(cr.Spec.Secrets.Vault) != 0 {
			volumes = append(volumes,
				corev1.VolumeMount{
					Name:      cr.Spec.Secrets.Vault,
					MountPath: config.VaultDir,
					ReadOnly:  true,
				},
			)
		} else {
			volumes = append(volumes,
				corev1.VolumeMount{
					Name:      cr.Spec.Secrets.EncryptionKey,
					MountPath: api.MongodRESTencryptDir,
					ReadOnly:  true,
				},
			)
		}
	}

	if cr.CompareVersion("1.8.0") >= 0 {
		volumes = append(volumes, corev1.VolumeMount{
			Name:      "users-secret-file",
			MountPath: "/etc/users-secret",
		})
	}

	rsName := replset.Name
	if name, err := replset.CustomReplsetName(); err == nil {
		rsName = name
	}

	container := corev1.Container{
		Name:            name,
		Image:           cr.Spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Args:            containerArgs(ctx, cr, replset, resources, useConfigFile),
		Ports: []corev1.ContainerPort{
			{
				Name:          config.MongodPortName,
				HostPort:      int32(0),
				ContainerPort: replset.GetPort(),
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
				Value: strconv.Itoa(int(replset.GetPort())),
			},
			{
				Name:  "MONGODB_REPLSET",
				Value: rsName,
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
		WorkingDir:      config.MongodContainerDataDir,
		LivenessProbe:   &livenessProbe.Probe,
		ReadinessProbe:  readinessProbe,
		Resources:       resources,
		SecurityContext: containerSecurityContext,
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

	if cr.CompareVersion("1.14.0") >= 0 {
		container.Command = []string{config.BinMountPath + "/ps-entry.sh"}
	}

	if cr.CompareVersion("1.21.0") >= 0 {
		if cr.IsLogCollectorEnabled() {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  "LOGCOLLECTOR_ENABLED",
				Value: "true",
			})
		}
	}

	return container, nil
}

// containerArgs returns the args to pass to the mSpec container
func containerArgs(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, resources corev1.ResourceRequirements, useConfigFile bool) []string {
	// TODO(andrew): in the safe mode `sslAllowInvalidCertificates` should be set only with the external services
	args := []string{
		"--bind_ip_all",
		"--auth",
		"--dbpath=" + config.MongodContainerDataDir,
		"--port=" + strconv.Itoa(int(replset.GetPort())),
		"--replSet=" + replset.Name,
		"--storageEngine=" + string(replset.Storage.Engine),
		"--relaxPermChecks",
	}

	name, err := replset.CustomReplsetName()
	if err == nil {
		args[4] = "--replSet=" + name
	}

	if *cr.Spec.TLS.AllowInvalidCertificates || cr.CompareVersion("1.16.0") < 0 {
		args = append(args, "--sslAllowInvalidCertificates")
	}

	if cr.Spec.Secrets.InternalKey != "" || (cr.TLSEnabled() && cr.Spec.TLS.Mode == api.TLSModeAllow) || (!cr.TLSEnabled() && cr.UnsafeTLSDisabled()) {
		args = append(args,
			"--clusterAuthMode=keyFile",
			"--keyFile="+config.MongodSecretsDir+"/mongodb-key",
		)
	} else if cr.TLSEnabled() {
		args = append(args, "--clusterAuthMode=x509")
	}

	if cr.CompareVersion("1.16.0") >= 0 {
		args = append(args, "--tlsMode="+string(cr.Spec.TLS.Mode))
	}

	// sharding
	switch replset.ClusterRole {
	case api.ClusterRoleConfigSvr:
		args = append(args, "--configsvr")
	case api.ClusterRoleShardSvr:
		args = append(args, "--shardsvr")
	}

	encryptionEnabled, err := replset.IsEncryptionEnabled()
	if err != nil {
		logf.FromContext(ctx).Error(err, "failed to check if mongo encryption enabled")
	}

	if cr.CompareVersion("1.12.0") >= 0 && encryptionEnabled && !replset.Configuration.VaultEnabled() {
		args = append(args, "--enableEncryption",
			"--encryptionKeyFile="+api.MongodRESTencryptDir+"/"+api.EncryptionKeyName,
		)
	}

	// storage
	if replset.Storage != nil {
		switch replset.Storage.Engine {
		case api.StorageEngineWiredTiger:
			if limit, ok := resources.Limits[corev1.ResourceMemory]; ok && !limit.IsZero() {
				args = append(args, fmt.Sprintf(
					"--wiredTigerCacheSizeGB=%.2f",
					getWiredTigerCacheSizeGB(resources.Limits, replset.Storage.WiredTiger.EngineConfig.CacheSizeRatio.Float64(), true),
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
				getWiredTigerCacheSizeGB(resources.Limits, replset.Storage.InMemory.EngineConfig.InMemorySizeRatio.Float64(), false),
			))
		}
		if replset.Storage.DirectoryPerDB {
			args = append(args, "--directoryperdb")
		}
		if replset.Storage.SyncPeriodSecs > 0 {
			args = append(args, "--syncdelay="+strconv.Itoa(replset.Storage.SyncPeriodSecs))
		}
	}

	if cr.CompareVersion("1.9.0") >= 0 && useConfigFile {
		args = append(args, fmt.Sprintf("--config=%s/mongod.conf", config.MongodConfigDir))
	}

	if cr.CompareVersion("1.16.0") >= 0 && replset.Configuration.QuietEnabled() {
		args = append(args, "--quiet")
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
func getWiredTigerCacheSizeGB(resourceList corev1.ResourceList, cacheRatio float64, subtract1GB bool) float64 {
	maxMemory := resourceList[corev1.ResourceMemory]
	var size float64
	if subtract1GB {
		size = math.Floor(cacheRatio * float64(maxMemory.Value()-config.GigaByte))
	} else {
		size = math.Floor(cacheRatio * float64(maxMemory.Value()))
	}
	sizeGB := size / float64(config.GigaByte)
	if sizeGB < config.MinWiredTigerCacheSizeGB {
		sizeGB = config.MinWiredTigerCacheSizeGB
	}
	return sizeGB
}
