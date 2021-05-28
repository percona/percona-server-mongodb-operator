package psmdb

import api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"

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
