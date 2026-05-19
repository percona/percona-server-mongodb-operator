package vectorsearch

const (
	// GRPCPort is the gRPC port mongot listens on. mongod forwards $search /
	// $vectorSearch traffic and createSearchIndex / dropSearchIndex /
	// listSearchIndexes calls here.
	GRPCPort     int32 = 27028
	GRPCPortName       = "grpc"

	HealthCheckPort     int32 = 8080
	HealthCheckPortName       = "health"

	MetricsPort     int32 = 9946
	MetricsPortName       = "metrics"

	// DataVolumeName is the name of the PVC template that holds mongot's
	// Lucene-style indexes.
	DataVolumeName = "mongot-data"

	// DataMountPath is where DataVolumeName is mounted in the container.
	DataMountPath = "/data/mongot"

	// ConfigMountPath is where the operator-generated mongot.conf is
	// mounted in the container.
	ConfigMountPath = "/etc/mongot"

	// ConfigFileName is the rendered mongot config file name inside the
	// ConfigMap and on the mounted volume.
	ConfigFileName = "mongot.conf"

	// ConfigVolumeName is the pod volume name for the mongot.conf
	// ConfigMap projection.
	ConfigVolumeName = "mongot-config"

	// UsersSecretVolumeName is the pod volume name for the
	// internal-users Secret projection. The same name and mount path
	// are used by mongod and mongos containers (see
	// pkg/psmdb/statefulset.go and pkg/psmdb/mongos.go) — keeping them
	// aligned makes the secret-rotation flow uniform across components.
	UsersSecretVolumeName = "users-secret-file"

	// UsersSecretMountPath is where the internal-users Secret is
	// projected. Files under this path are named after the Secret
	// keys, e.g. MONGODB_SEARCH_USER, MONGODB_SEARCH_PASSWORD.
	UsersSecretMountPath = "/etc/users-secret"
)
