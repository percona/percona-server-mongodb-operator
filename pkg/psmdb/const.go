package psmdb

import (
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

const (
	gigaByte                 int64   = 1 << 30
	minWiredTigerCacheSizeGB float64 = 0.25

	// MongodDataVolClaimName is a PVC Claim name
	MongodDataVolClaimName = "mongod-data"
	// MongodContainerDataDir is a mondo data path in container
	MongodContainerDataDir = "/data/db"

	sslDir               = "/etc/mongodb-ssl"
	sslInternalDir       = "/etc/mongodb-ssl-internal"
	mongodConfigDir      = "/etc/mongodb-config"
	mongosConfigDir      = "/etc/mongos-config"
	mongodSecretsDir     = "/etc/mongodb-secrets"
	mongodRESTencryptDir = "/etc/mongodb-encryption"
	EncryptionKeyName    = "encryption-key"
	mongodPortName       = "mongodb"
	mongosPortName       = "mongos"
)

func InternalKey(cr *api.PerconaServerMongoDB) string {
	return cr.Name + "-mongodb-keyfile"
}

type VolumeSourceType int

const (
	VolumeSourceNone VolumeSourceType = iota
	VolumeSourceConfigMap
	VolumeSourceSecret
)

func (s VolumeSourceType) IsUsable() bool {
	return s != VolumeSourceNone
}

func (s VolumeSourceType) String() string {
	switch s {
	case VolumeSourceConfigMap:
		return "ConfigMap"
	case VolumeSourceSecret:
		return "Secret"
	default:
		return ""
	}
}

func (s VolumeSourceType) VolumeSource(name string) corev1.VolumeSource {
	t := true
	switch s {
	case VolumeSourceConfigMap:
		return corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: name,
				},
				Optional: &t,
			},
		}
	case VolumeSourceSecret:
		return corev1.VolumeSource{
			Secret: &corev1.SecretVolumeSource{
				SecretName: name,
				Optional:   &t,
			},
		}
	default:
		return corev1.VolumeSource{}
	}
}

func VolumeSourceTypeToObj(s VolumeSourceType) runtime.Object {
	switch s {
	case VolumeSourceConfigMap:
		return &corev1.ConfigMap{}
	case VolumeSourceSecret:
		return &corev1.Secret{}
	default:
		return nil
	}
}
