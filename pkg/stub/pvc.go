package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	pkgSdk "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/sdk"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// newPSMDBMongodVolumeClaims returns a Persistent Volume Claims for Mongod pod
func newPSMDBMongodVolumeClaims(m *v1alpha1.PerconaServerMongoDB, claimName string, resources *corev1.ResourceRequirements) []corev1.PersistentVolumeClaim {
	vc := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: m.Name,
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
	if m.Spec.Mongod.StorageClassName != "" {
		vc.Spec.StorageClassName = &m.Spec.Mongod.StorageClassName
	}
	return []corev1.PersistentVolumeClaim{vc}
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

// getPersistentVolumeClaims returns a list of Persistent Volume Claims for a given replset
func getPersistentVolumeClaims(m *v1alpha1.PerconaServerMongoDB, client pkgSdk.Client, replsetName string) ([]corev1.PersistentVolumeClaim, error) {
	pvcList := persistentVolumeClaimList()
	labelSelector := labels.SelectorFromSet(labelsForPerconaServerMongoDB(m, replsetName)).String()
	listOps := &metav1.ListOptions{LabelSelector: labelSelector}
	err := client.List(m.Namespace, pvcList, sdk.WithListOptions(listOps))
	if err != nil {
		return nil, err
	}
	return pvcList.Items, nil
}

// deletePersistentVolumeClaim deletes a Persistent Volume Claim
func deletePersistentVolumeClaim(m *v1alpha1.PerconaServerMongoDB, client pkgSdk.Client, pvcName string) error {
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

// boundPersistentVolumeClaims returns a list of bound Persistent Volume Claims from a given list
func boundPersistentVolumeClaims(pvcs []corev1.PersistentVolumeClaim) []corev1.PersistentVolumeClaim {
	bound := []corev1.PersistentVolumeClaim{}
	for _, pvc := range pvcs {
		if pvc.Status.Phase != corev1.ClaimBound {
			continue
		}
		bound = append(bound, pvc)
	}
	return bound
}
