package psmdb

const (
	gigaByte                 int64   = 1 << 30
	minWiredTigerCacheSizeGB float64 = 0.25

	// MongodDataVolClaimName is a PVC Claim name
	MongodDataVolClaimName = "mongod-data"
	// MongodContainerDataDir is a mondo data path in container
	MongodContainerDataDir = "/data/db"

	sslDir               = "/etc/mongodb-ssl"
	sslInternalDir       = "/etc/mongodb-ssl-internal"
	mongodSecretsDir     = "/etc/mongodb-secrets"
	mongodRESTencryptDir = "/etc/mongodb-rest-encrypt"
	mongodPortName       = "mongodb"
)
