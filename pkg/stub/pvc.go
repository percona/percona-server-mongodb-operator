package stub

import (
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	pkgSdk "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/sdk"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
func getPersistentVolumeClaims(m *v1alpha1.PerconaServerMongoDB, client pkgSdk.Client, replset *v1alpha1.ReplsetSpec) ([]corev1.PersistentVolumeClaim, error) {
	pvcList := persistentVolumeClaimList()
	err := client.List(m.Namespace, pvcList, sdk.WithListOptions(getLabelSelectorListOpts(m, replset)))
	return pvcList.Items, err
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

// persistentVolumeClaimReaper removes Kubernetes Persistent Volume Claims
// from pods that have scaled down
func persistentVolumeClaimReaper(m *v1alpha1.PerconaServerMongoDB, client pkgSdk.Client, podList *corev1.PodList, replset *v1alpha1.ReplsetSpec, replsetStatus *v1alpha1.ReplsetStatus) error {
	var runningPods int
	for _, pod := range podList.Items {
		if isPodReady(pod) && isContainerAndPodRunning(pod, mongodContainerName) {
			runningPods++
		}
	}
	if runningPods < 1 {
		return nil
	}

	pvcs, err := getPersistentVolumeClaims(m, client, replset)
	if err != nil {
		logrus.Errorf("failed to get persistent volume claims: %v", err)
		return err
	}
	if len(pvcs) <= minPersistentVolumeClaims {
		return nil
	}
	for _, pvc := range pvcs {
		if pvc.Status.Phase != corev1.ClaimBound {
			continue
		}
		pvcPodName := strings.Replace(pvc.Name, mongodDataVolClaimName+"-", "", 1)
		if statusHasPod(replsetStatus, pvcPodName) {
			continue
		}
		err = deletePersistentVolumeClaim(m, client, pvc.Name)
		if err != nil {
			logrus.Errorf("failed to delete persistent volume claim %s: %v", pvc.Name, err)
			return err
		}
		logrus.Infof("deleted stale Persistent Volume Claim for replset %s: %s", replset.Name, pvc.Name)
	}

	return nil
}
