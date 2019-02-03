package psmdb

const (
	gigaByte                 int64   = 1024 * 1024 * 1024
	minWiredTigerCacheSizeGB float64 = 0.25

	mongodDataVolClaimName = "mongod-data"
	mongodContainerDataDir = "/data/db"
	mongodSecretsDir       = "/etc/mongodb-secrets"
	mongodPortName         = "mongodb"
)
