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

// handleReplsetInit exec the replset initiation steps on the first
// running mongod pod using a 'mongo' shell from within the container,
// required for using localhostAuthBypass when MongoDB auth is enabled
func (h *Handler) handleReplsetInit(m *v1alpha1.PerconaServerMongoDB, pods []corev1.Pod, replset *v1alpha1.PerconaServerMongoDBReplset) error {
	for _, pod := range pods {
		if !isMongodPod(pod) || pod.Status.Phase != corev1.PodRunning {
			continue
		}

		var containerRunning bool
		for _, container := range pod.Status.ContainerStatuses {
			if container.Name == mongodContainerName {
				containerRunning = container.State.Running != nil
				break
			}
		}
		if !containerRunning {
			continue
		}

		logrus.Infof("Initiating replset %s on pod: %s", replset.Name, pod.Name)

		// init replset and add the first admin user (with localhostBypassAuth exception)
		initCmds := []string{
			fmt.Sprintf("rs.initiate({_id:\"%s\", version:1, members:[{_id:0, host:\"%s\"}]})",
				replset.Name,
				pod.Name+"."+m.Name+"."+m.Namespace+".svc.cluster.local:"+strconv.Itoa(int(m.Spec.Mongod.Port)),
			),
			"db.createUser({ user: \"userAdmin\", pwd: \"admin123456\", roles: [{ db: \"admin\", role: \"root\" }]})",
		}
		for _, cmd := range initCmds {
			err := execMongoCommandsInContainer(pod, mongodContainerName, cmd, "", "")
			if err != nil {
				return err
			}
		}

		// add other users using the new admin user
		addUserCmds := []string{
			"db.createUser({ user: \"clusterAdmin\", pwd: \"admin123456\", roles: [{ db: \"admin\", role: \"clusterAdmin\" }]})",
			"db.createUser({ user: \"clusterMonitor\", pwd: \"admin123456\", roles: [{ db: \"admin\", role: \"clusterMonitor\" }]})",
		}
		for _, cmd := range addUserCmds {
			err := execMongoCommandsInContainer(pod, mongodContainerName, cmd, "userAdmin", "admin123456")
			if err != nil {
				return err
			}
		}

		m.Status.Initialised = true
		err := sdk.Update(m)
		if err != nil {
			return fmt.Errorf("failed to update psmdb status: %v", err)
		}
		h.initialised[replset.Name] = true

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
