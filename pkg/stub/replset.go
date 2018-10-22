package stub

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

// isContainerRunning returns a boolean reflecting if a container
// (and pod) are in a running state
func isContainerRunning(pod corev1.Pod, containerName string) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, container := range pod.Status.ContainerStatuses {
		if container.Name == containerName && container.State.Running != nil {
			return true
		}
	}
	return false
}

// handleReplsetInit exec the replset initiation steps on the first
// running mongod pod using a 'mongo' shell from within the container,
// required for using localhostAuthBypass when MongoDB auth is enabled
func (h *Handler) handleReplsetInit(m *v1alpha1.PerconaServerMongoDB, pods []corev1.Pod) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || !isContainerRunning(pod, mongodContainerName) {
			continue
		}

		logrus.Infof("Initiating replset on pod: %s", pod.Name)

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

		// update status after replset init
		m.Status.Initialised = true
		err = sdk.Update(m)
		if err != nil {
			return fmt.Errorf("failed to update psmdb status: %v", err)
		}
		h.initialised = true

		return nil
	}
	return fmt.Errorf("could not initiate replset")
}

// isMongodPod returns a boolean reflecting if a pod
// is running a mongod container
func isMongodPod(pod corev1.Pod) bool {
	for _, container := range pod.Spec.Containers {
		if container.Name == mongodContainerName {
			return true
		}
	}
	return false
}

// getMongoURI returns the mongodb uri containing the host/port of each pod
func getMongoURI(pods []corev1.Pod, portName string) string {
	var hosts []string
	for _, pod := range pods {
		if pod.Status.HostIP == "" && len(pod.Spec.Containers) >= 1 {
			continue
		}
		for _, container := range pod.Spec.Containers {
			if container.Name != mongodContainerName {
				continue
			}
			for _, port := range container.Ports {
				if port.Name != portName {
					continue
				}
				mongoPort := strconv.Itoa(int(port.HostPort))
				hosts = append(hosts, pod.Status.HostIP+":"+mongoPort)
				break
			}
			break
		}
	}
	if len(hosts) > 0 {
		return "mongodb://" + strings.Join(hosts, ",")
	}
	return ""
}
