package config

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

var (
	DefaultVersion                        = "3.6"
	DefaultRunUID                   int64 = 1001
	DefaultKeySecretName                  = "percona-server-mongodb-key"
	DefaultUsersSecretName                = "percona-server-mongodb-users"
	DefaultMongodSize               int32 = 3
	DefaultReplsetName                    = "rs"
	DefaultStorageEngine                  = v1alpha1.StorageEngineWiredTiger
	DefaultMongodPort               int32 = 27017
	DefaultWiredTigerCacheSizeRatio       = 0.5
	DefaultInMemorySizeRatio              = 0.9
	DefaultOperationProfilingMode         = v1alpha1.OperationProfilingModeSlowOp
	DefaultImagePullPolicy                = corev1.PullAlways
)
