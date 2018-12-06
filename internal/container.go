package internal

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

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
