package stub

import (
	"fmt"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

// handleReplsetInit exec the replset initiation steps on the first
// running mongod pod using a 'mongo' shell from within the container,
// required for using localhostAuthBypass when MongoDB auth is enabled
func (h *Handler) handleReplsetInit(m *v1alpha1.PerconaServerMongoDB, replsetName string, pods []corev1.Pod) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerAndPodRunning(pod, mongodContainerName) {
			continue
		}

		logrus.Infof("Initiating replset %s on running pod: %s", replsetName, pod.Name)

		// Run the k8s-mongodb-initiator from within the first running container
		// this must be ran from within the running container to utilise the MongoDB
		// Localhost Exeception.
		//
		// See: https://docs.mongodb.com/manual/core/security-users/#localhost-exception
		//
		err := execCommandInContainer(pod, mongodContainerName, []string{
			"/mongodb/k8s-mongodb-initiator",
			"init",
		})
		if err != nil {
			return err
		}

		return nil
	}
	return fmt.Errorf("no %s containers in running state", mongodContainerName)
}
