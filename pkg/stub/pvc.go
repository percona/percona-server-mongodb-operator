package stub

import (
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

// persistentVolumeClaimReaper removes Kubernetes Persistent Volume Claims
// from pods that have scaled down
func (h *Handler) persistentVolumeClaimReaper(m *v1alpha1.PerconaServerMongoDB, pods []corev1.Pod, replset *v1alpha1.ReplsetSpec, replsetStatus *v1alpha1.ReplsetStatus) error {
	var runningPods int
	for _, pod := range pods {
		if util.IsPodReady(pod) && util.IsContainerAndPodRunning(pod, mongod.MongodContainerName) {
			runningPods++
		}
	}
	if runningPods < 1 {
		return nil
	}

	pvcs, err := util.GetPersistentVolumeClaims(h.client, m, replset)
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
		if !strings.Contains(pvc.Name, mongod.MongodDataVolClaimName) {
			continue
		}
		pvcPodName := strings.Replace(pvc.Name, mongod.MongodDataVolClaimName+"-", "", 1)
		if util.ReplsetStatusHasPod(replsetStatus, pvcPodName) {
			continue
		}
		err = util.DeletePersistentVolumeClaim(h.client, m, pvc.Name)
		if err != nil {
			logrus.Errorf("failed to delete persistent volume claim %s: %v", pvc.Name, err)
			return err
		}
		logrus.Infof("deleted stale Persistent Volume Claim for replset %s: %s", replset.Name, pvc.Name)
	}

	return nil
}
