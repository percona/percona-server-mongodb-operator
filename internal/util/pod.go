package util

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PodList returns a v1.PodList object
func PodList() *corev1.PodList {
	return &corev1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
	}
}

// GetPodContainer returns a container, if it exists
func GetPodContainer(pod *corev1.Pod, containerName string) *corev1.Container {
	for _, cont := range pod.Spec.Containers {
		if cont.Name == containerName {
			return &cont
		}
	}
	return nil
}

// GetPodSpecContainer returns a container from a pod spec, if it exists
func GetPodSpecContainer(spec *corev1.PodSpec, containerName string) *corev1.Container {
	for i, c := range spec.Containers {
		if c.Name != containerName {
			continue
		}
		return &spec.Containers[i]
	}
	return nil
}

// GetPodContainerStatus returns a container status from a pod status, if it exists
func GetPodContainerStatus(status *corev1.PodStatus, containerName string) *corev1.ContainerStatus {
	for i, c := range status.ContainerStatuses {
		if c.Name != containerName {
			continue
		}
		return &status.ContainerStatuses[i]
	}
	return nil
}

// GetPodNames returns the pod names of the array of pods passed in
func GetPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

// ReplsetStatusHasPod returns a boolean reflecting if a ReplsetSTatus contains a
// pod name
func ReplsetStatusHasPod(status *v1alpha1.ReplsetStatus, podName string) bool {
	for _, pod := range status.Pods {
		if pod == podName {
			return true
		}
	}
	return false
}

// IsPodReady returns a boolean reflecting if a pod is in a "ready" state
func IsPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		if condition.Type == corev1.PodReady {
			return true
		}
	}
	return false
}
