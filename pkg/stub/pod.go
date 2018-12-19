package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"

	corev1 "k8s.io/api/core/v1"
)

// isMongodPod returns a boolean reflecting if a pod
// is running a mongod container
func isMongodPod(pod corev1.Pod) bool {
	container := util.GetPodContainer(&pod, mongod.MongodContainerName)
	return container != nil
}
