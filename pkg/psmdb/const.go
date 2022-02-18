package psmdb

import (
	"crypto/md5"
	"encoding/json"
	"fmt"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/pkg/errors"
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

	sslDir           = "/etc/mongodb-ssl"
	sslInternalDir   = "/etc/mongodb-ssl-internal"
	mongodConfigDir  = "/etc/mongodb-config"
	mongosConfigDir  = "/etc/mongos-config"
	mongodSecretsDir = "/etc/mongodb-secrets"
	mongodPortName   = "mongodb"
	mongosPortName   = "mongos"
)

func InternalKey(cr *api.PerconaServerMongoDB) string {
	return cr.Name + "-mongodb-keyfile"
}

type CustomConfig struct {
	Type    VolumeSourceType
	HashHex string
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

type HashableObject interface {
	GetRuntimeObject() runtime.Object
	GetHashHex() (string, error)
}

func VolumeSourceTypeToObj(s VolumeSourceType) HashableObject {
	switch s {
	case VolumeSourceConfigMap:
		return &hashableConfigMap{}
	case VolumeSourceSecret:
		return &hashableSecret{}
	default:
		return nil
	}
}

type hashableConfigMap struct {
	corev1.ConfigMap
}

func (cm *hashableConfigMap) GetRuntimeObject() runtime.Object {
	return &cm.ConfigMap
}

func (cm *hashableConfigMap) GetHashHex() (string, error) {
	return getCustomConfigHashHex(cm.Data, cm.BinaryData)
}

type hashableSecret struct {
	corev1.Secret
}

func (s *hashableSecret) GetRuntimeObject() runtime.Object {
	return &s.Secret
}

func (s *hashableSecret) GetHashHex() (string, error) {
	return getCustomConfigHashHex(s.StringData, s.Data)
}

func getCustomConfigHashHex(strData map[string]string, binData map[string][]byte) (string, error) {
	var content = struct {
		StrData map[string]string `json:"str_data,omitempty"`
		BinData map[string][]byte `json:"bin_data,omitempty"`
	}{
		StrData: strData,
		BinData: binData,
	}

	allData, err := json.Marshal(content)
	if err != nil {
		return "", errors.Wrap(err, "failed to concat data for config hash")
	}

	hashHex := fmt.Sprintf("%x", md5.Sum(allData))

	return hashHex, nil
}
