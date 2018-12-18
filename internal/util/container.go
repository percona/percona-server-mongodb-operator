package util

import (
	corev1 "k8s.io/api/core/v1"
)

// GetContainerResourceRequirements returns a corev1.ResourceRequirements with the
// 'storage' type (not needed in corev1.Container) removed
func GetContainerResourceRequirements(reqs corev1.ResourceRequirements) corev1.ResourceRequirements {
	var containerReqs corev1.ResourceRequirements
	reqs.DeepCopyInto(&containerReqs)
	delete(containerReqs.Limits, corev1.ResourceStorage)
	return containerReqs
}
