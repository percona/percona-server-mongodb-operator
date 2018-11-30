package stub

import (
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal"
	sdk "github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	opSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newPSMDBMongodVolumeClaims returns a Persistent Volume Claims for Mongod pod
func newPSMDBMongodVolumeClaims(m *v1alpha1.PerconaServerMongoDB, resources *corev1.ResourceRequirements, claimName, storageClass string) []corev1.PersistentVolumeClaim {
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
	if storageClass != "" {
		vc.Spec.StorageClassName = &storageClass
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
func getPersistentVolumeClaims(m *v1alpha1.PerconaServerMongoDB, client sdk.Client, replset *v1alpha1.ReplsetSpec) ([]corev1.PersistentVolumeClaim, error) {
	pvcList := persistentVolumeClaimList()
	err := client.List(m.Namespace, pvcList, opSdk.WithListOptions(
		internal.GetLabelSelectorListOpts(m, replset),
	))
	return pvcList.Items, err
}

// isStatefulSetUpdating returns a boolean reflecting if a StatefulSet is updating or
// scaling. If the currentRevision is different than the updateRevision or if the
// number of readyReplicas is different than currentReplicas, the set is updating
func isStatefulSetUpdating(set *appsv1.StatefulSet) bool {
	if set.Status.CurrentRevision != set.Status.UpdateRevision {
		return true
	}
	return set.Status.ReadyReplicas != set.Status.CurrentReplicas
}

// deletePersistentVolumeClaim deletes a Persistent Volume Claim
func deletePersistentVolumeClaim(m *v1alpha1.PerconaServerMongoDB, client sdk.Client, pvcName string) error {
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
func (h *Handler) persistentVolumeClaimReaper(m *v1alpha1.PerconaServerMongoDB, pods []corev1.Pod, replset *v1alpha1.ReplsetSpec, replsetStatus *v1alpha1.ReplsetStatus) error {
	var runningPods int
	for _, pod := range pods {
		if isPodReady(pod) && isContainerAndPodRunning(pod, mongodContainerName) {
			runningPods++
		}
	}
	if runningPods < 1 {
		return nil
	}

	pvcs, err := getPersistentVolumeClaims(m, h.client, replset)
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
		err = deletePersistentVolumeClaim(m, h.client, pvc.Name)
		if err != nil {
			logrus.Errorf("failed to delete persistent volume claim %s: %v", pvc.Name, err)
			return err
		}
		logrus.Infof("deleted stale Persistent Volume Claim for replset %s: %s", replset.Name, pvc.Name)
	}

	return nil
}
