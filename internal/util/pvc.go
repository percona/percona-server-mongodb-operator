package util

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewPersistentVolumeClaim returns a Persistent Volume Claims for Mongod pod
func NewPersistentVolumeClaim(m *v1alpha1.PerconaServerMongoDB, resources corev1.ResourceRequirements, claimName, storageClass string) corev1.PersistentVolumeClaim {
	vc := corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: m.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resources.Limits[corev1.ResourceStorage],
				},
			},
		},
	}
	if storageClass != "" {
		vc.Spec.StorageClassName = &storageClass
	}
	return vc
}

// persistentVolumeClaimList returns a v1.PersistentVolumeList object
func persistentVolumeClaimList() *corev1.PersistentVolumeClaimList {
	return &corev1.PersistentVolumeClaimList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
	}
}

// GetPersistentVolumeClaims returns a list of Persistent Volume Claims for a given replset
func GetPersistentVolumeClaims(client sdk.Client, m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) ([]corev1.PersistentVolumeClaim, error) {
	pvcList := persistentVolumeClaimList()
	err := client.List(m.Namespace, pvcList, opSdk.WithListOptions(
		GetLabelSelectorListOpts(m, replset),
	))
	return pvcList.Items, err
}

// DeletePersistentVolumeClaim deletes a Persistent Volume Claim
func DeletePersistentVolumeClaim(client sdk.Client, m *v1alpha1.PerconaServerMongoDB, pvcName string) error {
	return client.Delete(&corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: m.Namespace,
		},
	})
}
