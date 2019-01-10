package util

import (
	"errors"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

// IsContainerTerminated returns a boolean reflecting if a container has terminated
func IsContainerTerminated(podStatus *corev1.PodStatus, containerName string) (bool, error) {
	status := GetPodContainerStatus(podStatus, containerName)
	if status != nil {
		return status.State.Terminated != nil, nil
	}
	return false, errors.New("container status not found")
}

// GetContainerRunUID returns an int64-pointer reflecting the user ID a container
// should run as
func GetContainerRunUID(m *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion) *int64 {
	if GetPlatform(m, serverVersion) != v1alpha1.PlatformOpenshift {
		return &m.Spec.RunUID
	}
	return nil
}

// GetContainerResourceRequirements returns a corev1.ResourceRequirements with the
// 'storage' type (not needed in corev1.Container) removed
func GetContainerResourceRequirements(reqs corev1.ResourceRequirements) corev1.ResourceRequirements {
	var containerReqs corev1.ResourceRequirements
	reqs.DeepCopyInto(&containerReqs)
	delete(containerReqs.Limits, corev1.ResourceStorage)
	return containerReqs
}

// IsContainerAndPodRunning returns a boolean reflecting if
// a container and pod are in a running state
func IsContainerAndPodRunning(pod corev1.Pod, containerName string) bool {
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
