package vectorsearch

const (
	// grpcPort is the gRPC port mongot listens on. mongod forwards $search /
	// $vectorSearch traffic and createSearchIndex / dropSearchIndex /
	// listSearchIndexes calls here.
	grpcPort     int32 = 27028
	grpcPortName       = "grpc"

	healthCheckPort int32 = 8080

	metricsPort     int32 = 9946
	metricsPortName       = "metrics"

	// dataVolumeName is the name of the PVC template that holds mongot's
	// Lucene-style indexes.
	dataVolumeName = "mongot-data"

	// dataMountPath is where DataVolumeName is mounted in the container.
	dataMountPath = "/data/mongot"

	// configMountPath is where the operator-generated mongot.conf is
	// mounted in the container.
	configMountPath = "/etc/mongot"

	// configFileName is the rendered mongot config file name inside the
	// ConfigMap and on the mounted volume.
	configFileName = "mongot.conf"

	// configVolumeName is the pod volume name for the mongot.conf
	// ConfigMap projection.
	configVolumeName = "mongot-config"

	// usersSecretVolumeName is the pod volume name for the
	// internal-users Secret projection. The same name and mount path
	// are used by mongod and mongos containers (see
	// pkg/psmdb/statefulset.go and pkg/psmdb/mongos.go) — keeping them
	// aligned makes the secret-rotation flow uniform across components.
	usersSecretVolumeName = "users-secret-file"

	// usersSecretMountPath is where the internal-users Secret is
	// projected. Files under this path are named after the Secret
	// keys, e.g. MONGODB_SEARCH_USER, MONGODB_SEARCH_PASSWORD.
	usersSecretMountPath = "/etc/users-secret"
)
