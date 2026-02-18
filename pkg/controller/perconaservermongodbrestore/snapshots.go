package perconaservermongodbrestore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func (r *ReconcilePerconaServerMongoDBRestore) reconcileExternalSnapshotRestore(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDBRestore,
	bcp *psmdbv1.PerconaServerMongoDBBackup,
	cluster *psmdbv1.PerconaServerMongoDB,
) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {

	switch cr.Status.State {
	case psmdbv1.RestoreStateNew:
		return r.reconcileSnapshotNew(cr)

	case psmdbv1.RestoreStateWaiting:
		return r.reconcileSnapshotWaiting(ctx, cr, cluster)

	case psmdbv1.RestoreStateRequested:
		return r.reconcileSnapshotRequested(ctx, cr, cluster)

	case psmdbv1.RestoreStateRunning:
		return r.reconcileSnapshotRunning(ctx, cr, cluster, bcp)
	}

	return cr.Status, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) reconcileSnapshotNew(
	restore *psmdbv1.PerconaServerMongoDBRestore,
) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	status := restore.Status
	status.State = psmdbv1.RestoreStateWaiting
	return status, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) reconcileSnapshotWaiting(
	ctx context.Context,
	restore *psmdbv1.PerconaServerMongoDBRestore,
	cluster *psmdbv1.PerconaServerMongoDB,
) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	log := logf.FromContext(ctx)

	status := restore.Status
	if err := r.updatePBMConfigSecret(ctx, cluster); err != nil {
		return status, errors.Wrap(err, "update PBM config secret")
	}

	// TODO: also create a new secret with encryption settings, which will be passed to `--db-config` flag.

	if err := r.prepareStatefulSetsForPhysicalRestore(ctx, cluster); err != nil {
		return status, errors.Wrap(err, "prepare statefulsets for physical restore")
	}

	sfsReady, err := r.checkIfStatefulSetsAreReadyForPhysicalRestore(ctx, cluster)
	if err != nil {
		return status, errors.Wrap(err, "check if statefulsets are ready for physical restore")
	}

	if !sfsReady {
		log.Info("Waiting for statefulsets to be ready before restore", "ready", sfsReady)
		return status, nil
	}

	replsets := cluster.GetAllReplsets()
	rs := replsets[0]

	pod := corev1.Pod{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: rs.PodName(cluster, 0), Namespace: cluster.Namespace}, &pod); err != nil {
		return status, errors.Wrap(err, "get pod")
	}

	restoreCmd := []string{
		"/opt/percona/pbm", "restore",
		"--external", "--out", "json",
	}

	// TODO: support replset remapping?

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	err = retry.OnError(anotherOpBackoff, func(err error) bool {
		return strings.Contains(err.Error(), "another operation") ||
			strings.Contains(err.Error(), "unable to upgrade connection")
	}, func() error {
		log.Info("Starting restore", "command", restoreCmd, "pod", pod.Name)

		stdoutBuf.Reset()
		stderrBuf.Reset()

		err := r.clientcmd.Exec(ctx, &pod, "mongod", restoreCmd, nil, stdoutBuf, stderrBuf, false)
		if err != nil {
			log.Error(nil, "Restore failed to start", "pod", pod.Name, "stderr", stderrBuf.String(), "stdout", stdoutBuf.String())
			return errors.Wrapf(err, "start restore stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
		}

		log.Info("Restore started", "pod", pod.Name)

		return nil
	})
	if err != nil {
		return status, err
	}

	var out struct {
		Name    string `json:"name"`
		Storage string `json:"storage"`
	}
	if err := json.Unmarshal(stdoutBuf.Bytes(), &out); err != nil {
		return status, errors.Wrap(err, "unmarshal PBM restore output")
	}

	status.State = psmdbv1.RestoreStateRequested
	status.PBMname = out.Name
	return status, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) reconcileSnapshotRequested(
	ctx context.Context,
	restore *psmdbv1.PerconaServerMongoDBRestore,
	cluster *psmdbv1.PerconaServerMongoDB,
) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	log := logf.FromContext(ctx)

	replsets := cluster.GetAllReplsets()

	status := restore.Status
	meta := backup.BackupMeta{}

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	err := retry.OnError(retry.DefaultBackoff, func(err error) bool {
		return strings.Contains(err.Error(), "container is not created or running") ||
			strings.Contains(err.Error(), "error dialing backend: No agent available") ||
			strings.Contains(err.Error(), "unable to upgrade connection") ||
			strings.Contains(err.Error(), "unmarshal PBM describe-restore output")
	}, func() error {
		stdoutBuf.Reset()
		stderrBuf.Reset()

		command := []string{
			"/opt/percona/pbm", "describe-restore", status.PBMname,
			"--config", "/etc/pbm/pbm_config.yaml",
			"--out", "json",
		}

		pod := corev1.Pod{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: replsets[0].PodName(cluster, 0), Namespace: cluster.Namespace}, &pod); err != nil {
			return errors.Wrap(err, "get pod")
		}

		if err := r.clientcmd.Exec(ctx, &pod, "mongod", command, nil, stdoutBuf, stderrBuf, false); err != nil {
			return errors.Wrapf(err, "describe restore stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
		}

		return nil
	})
	if err != nil {
		return status, err
	}

	if err := json.Unmarshal(stdoutBuf.Bytes(), &meta); err != nil {
		return status, errors.Wrap(err, "unmarshal PBM describe-restore output")
	}

	if meta.Status != defs.StatusCopyReady {
		log.Info("Waiting for nodes to be copy ready", "status", meta.Status)
		return status, nil
	}

	log.Info("Nodes are ready for snapshot restore", "status", meta.Status)
	status.State = psmdbv1.RestoreStateRunning
	return status, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) reconcileSnapshotRunning(
	ctx context.Context,
	restore *psmdbv1.PerconaServerMongoDBRestore,
	cluster *psmdbv1.PerconaServerMongoDB,
	bcp *psmdbv1.PerconaServerMongoDBBackup,
) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	status := restore.Status
	log := logf.FromContext(ctx)
	if ok, err := r.scaleDownStatefulSetsForSnapshotRestore(ctx, cluster, restore); err != nil {
		return restore.Status, errors.Wrapf(err, "prepare statefulsets for snapshot restore")
	} else if !ok {
		log.Info("Waiting for statefulsets to be scaled down", "ready", ok)
		return status, nil
	}

	if ok, err := r.reconcilePVCsForSnapshotRestore(ctx, cluster, restore, bcp); err != nil {
		return restore.Status, errors.Wrapf(err, "reconcile pvcs for snapshot restore")
	} else if !ok {
		log.Info("Waiting for pvcs to be reconciled", "ready", ok)
		return status, nil
	}

	if ok, err := r.scaleUpStatefulSetsForSnapshotRestore(ctx, cluster); err != nil {
		return restore.Status, errors.Wrapf(err, "scale up statefulsets for snapshot restore")
	} else if !ok {
		log.Info("Waiting for statefulsets to be scaled up", "ready", ok)
		return status, nil
	}

	if err := r.runPBMRestoreFinish(ctx, cluster, restore); err != nil {
		return restore.Status, errors.Wrapf(err, "run PBM restore finish")
	}

	// TODO
	// Delete all statefulsets.
	// Resync PBM storage.

	return restore.Status, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) scaleDownStatefulSetsForSnapshotRestore(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	restore *psmdbv1.PerconaServerMongoDBRestore,
) (bool, error) {
	replsets := cluster.GetAllReplsets()

	// Collect all statefulsets that need to be scaled down.
	statefulsets := []types.NamespacedName{}
	for _, rs := range replsets {
		statefulsets = append(statefulsets, types.NamespacedName{Namespace: cluster.Namespace, Name: naming.MongodStatefulSetName(cluster, rs)})
		if rs.NonVoting.Enabled {
			statefulsets = append(statefulsets, types.NamespacedName{Namespace: cluster.Namespace, Name: naming.NonVotingStatefulSetName(cluster, rs)})
		}
		if rs.Hidden.Enabled {
			statefulsets = append(statefulsets, types.NamespacedName{Namespace: cluster.Namespace, Name: naming.HiddenStatefulSetName(cluster, rs)})
		}
	}

	done := true
	for _, nn := range statefulsets {
		if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			sfs := appsv1.StatefulSet{}
			if err := r.client.Get(ctx, nn, &sfs); err != nil {
				return err
			}

			if sfs.Status.ReadyReplicas > 0 {
				done = false
			}

			if sfs.Spec.Replicas != nil && *sfs.Spec.Replicas == 0 {
				return nil
			}

			orig := sfs.DeepCopy()

			// Scale down the statefulset.
			sfs.Spec.Replicas = ptr.To(int32(0))

			// When the pods come up, they should start pbm with the following command:
			sfs.Spec.Template.Spec.Containers[0].Command = []string{"/opt/percona/pbm-agent"}
			sfs.Spec.Template.Spec.Containers[0].Args = []string{
				"restore-finish",
				restore.Status.PBMname,
				"-c", "/etc/pbm/pbm_config.yaml",
				"--rs", "$(MONGODB_REPLSET)",
				"--node", "$(POD_NAME).$(SERVICE_NAME)-$(MONGODB_REPLSET).$(NAMESPACE).svc.cluster.local",
				// "--db-config", "/etc/pbm/db-config.yaml", // TODO
			}
			return r.client.Patch(ctx, &sfs, client.MergeFrom(orig))
		}); err != nil {
			return false, errors.Wrapf(err, "prepare statefulset %s for snapshot restore", nn.Name)
		}
	}
	return done, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) reconcilePVCsForSnapshotRestore(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	restore *psmdbv1.PerconaServerMongoDBRestore,
	backup *psmdbv1.PerconaServerMongoDBBackup,
) (bool, error) {
	replsets := cluster.GetAllReplsets()

	type pvcInfo struct {
		pvcName             string
		snapshotName        string
		volumeClaimTemplate corev1.PersistentVolumeClaimSpec
	}

	getVolumeClaimTemplate := func(sfsName string) (corev1.PersistentVolumeClaimSpec, error) {
		sfs := appsv1.StatefulSet{}
		if err := r.client.Get(ctx, types.NamespacedName{Name: sfsName, Namespace: cluster.Namespace}, &sfs); err != nil {
			return corev1.PersistentVolumeClaimSpec{}, errors.Wrapf(err, "get statefulset %s", sfsName)
		}
		if len(sfs.Spec.VolumeClaimTemplates) == 0 {
			return corev1.PersistentVolumeClaimSpec{}, errors.Errorf("no volume claim templates found for statefulset %s", sfsName)
		}
		for _, vct := range sfs.Spec.VolumeClaimTemplates {
			if vct.Name == config.MongodDataVolClaimName {
				return vct.Spec, nil
			}
		}
		return corev1.PersistentVolumeClaimSpec{}, errors.Errorf("volume claim template %s not found", config.MongodDataVolClaimName)
	}

	// Collect all PVCs that need to be reconciled.
	pvcs := make([]pvcInfo, 0)
	for _, rs := range replsets {
		snapshot := backup.Status.Snapshots.GetSnapshotInfo(rs.Name)
		if snapshot == nil {
			return false, fmt.Errorf("no snapshots found for replset %s", rs.Name)
		}
		vct, err := getVolumeClaimTemplate(naming.MongodStatefulSetName(cluster, rs))
		if err != nil {
			return false, errors.Wrapf(err, "get volume claim template for statefulset %s", naming.MongodStatefulSetName(cluster, rs))
		}
		for podIdx := int32(0); podIdx < rs.Size; podIdx++ {
			pvcs = append(pvcs, pvcInfo{
				pvcName:             config.MongodDataVolClaimName + "-" + rs.PodName(cluster, int(podIdx)),
				volumeClaimTemplate: vct,
				snapshotName:        snapshot.SnapshotName,
			})
		}

		if rs.NonVoting.Enabled {
			vct, err := getVolumeClaimTemplate(naming.NonVotingStatefulSetName(cluster, rs))
			if err != nil {
				return false, errors.Wrapf(err, "get volume claim template for statefulset %s", naming.NonVotingStatefulSetName(cluster, rs))
			}
			pvcs = append(pvcs, pvcInfo{
				pvcName:             config.MongodDataVolClaimName + "-" + naming.NonVotingStatefulSetName(cluster, rs) + "-0",
				volumeClaimTemplate: vct,
				snapshotName:        snapshot.SnapshotName,
			})
		}
		if rs.Hidden.Enabled {
			vct, err := getVolumeClaimTemplate(naming.HiddenStatefulSetName(cluster, rs))
			if err != nil {
				return false, errors.Wrapf(err, "get volume claim template for statefulset %s", naming.HiddenStatefulSetName(cluster, rs))
			}
			pvcs = append(pvcs, pvcInfo{
				pvcName:             config.MongodDataVolClaimName + "-" + naming.HiddenStatefulSetName(cluster, rs) + "-0",
				volumeClaimTemplate: vct,
				snapshotName:        snapshot.SnapshotName,
			})
		}
	}

	done := true
	for _, info := range pvcs {
		if ready, err := r.reconcilePVCForSnapshotRestore(ctx, info.pvcName, info.snapshotName,
			info.volumeClaimTemplate, restore); err != nil {
			return false, errors.Wrapf(err, "reconcile pvc %s for snapshot restore", info.pvcName)
		} else if !ready {
			done = false
		}
	}
	return done, nil
}

func generatePVCFromSnapshot(
	pvc *corev1.PersistentVolumeClaim,
	spec corev1.PersistentVolumeClaimSpec,
	snapshotName string,
) {
	pvc.Spec = spec
	pvc.Spec.DataSource = &corev1.TypedLocalObjectReference{
		APIGroup: ptr.To(volumesnapshotv1.SchemeGroupVersion.Group),
		Kind:     "VolumeSnapshot",
		Name:     snapshotName,
	}
	pvc.SetAnnotations(map[string]string{
		naming.AnnotationRestoreName: snapshotName,
	})
}

func (r *ReconcilePerconaServerMongoDBRestore) reconcilePVCForSnapshotRestore(
	ctx context.Context,
	pvcName string,
	snapshotName string,
	volumeClaimTemplate corev1.PersistentVolumeClaimSpec,
	restore *psmdbv1.PerconaServerMongoDBRestore,
) (bool, error) {
	observedPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pvcName,
			Namespace: restore.GetNamespace(),
		},
	}
	err := r.client.Get(ctx, client.ObjectKeyFromObject(observedPVC), observedPVC)
	if k8sErrors.IsNotFound(err) {
		generatePVCFromSnapshot(observedPVC, volumeClaimTemplate, snapshotName)
		if err := r.client.Create(ctx, observedPVC); err != nil {
			return false, errors.Wrapf(err, "create pvc %s", pvcName)
		}
		return true, nil
	} else if err != nil {
		return false, errors.Wrapf(err, "get observed pvc %s", pvcName)
	}

	restoreName, ok := observedPVC.GetAnnotations()[naming.AnnotationRestoreName]
	if ok && restoreName == restore.Name {
		return true, nil
	}

	if !observedPVC.GetDeletionTimestamp().IsZero() {
		return false, nil
	}

	if err := r.client.Delete(ctx, observedPVC); err != nil {
		return false, errors.Wrapf(err, "delete pvc %s", pvcName)
	}
	return false, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) scaleUpStatefulSetsForSnapshotRestore(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
) (bool, error) {
	replsets := cluster.GetAllReplsets()

	// Collect all statefulsets that need to be scaled up.
	sfsInfos := make(map[types.NamespacedName]int32)
	for _, rs := range replsets {
		sfsInfos[types.NamespacedName{Namespace: cluster.Namespace, Name: naming.MongodStatefulSetName(cluster, rs)}] = rs.Size
		if rs.NonVoting.Enabled {
			sfsInfos[types.NamespacedName{Namespace: cluster.Namespace, Name: naming.NonVotingStatefulSetName(cluster, rs)}] = 1
		}
		if rs.Hidden.Enabled {
			sfsInfos[types.NamespacedName{Namespace: cluster.Namespace, Name: naming.HiddenStatefulSetName(cluster, rs)}] = 1
		}
	}

	done := true
	for nn, replicas := range sfsInfos {
		if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			sfs := appsv1.StatefulSet{}
			if err := r.client.Get(ctx, nn, &sfs); err != nil {
				return err
			}

			if sfs.Status.ReadyReplicas != replicas {
				done = false
			}

			if sfs.Spec.Replicas != nil && *sfs.Spec.Replicas == replicas {
				return nil
			}

			orig := sfs.DeepCopy()

			// Scale down the statefulset.
			sfs.Spec.Replicas = ptr.To(replicas)
			return r.client.Patch(ctx, &sfs, client.MergeFrom(orig))
		}); err != nil {
			return false, errors.Wrapf(err, "prepare statefulset %s for snapshot restore", nn.Name)
		}
	}
	return done, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) runPBMRestoreFinish(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	restore *psmdbv1.PerconaServerMongoDBRestore,
) error {
	if _, ok := cluster.GetAnnotations()[naming.AnnotationRestoreFinished]; ok {
		return nil
	}

	replsets := cluster.GetAllReplsets()

	pod := corev1.Pod{}
	if err := r.client.Get(ctx, types.NamespacedName{Name: replsets[0].PodName(cluster, 0), Namespace: cluster.Namespace}, &pod); err != nil {
		return errors.Wrap(err, "get pod")
	}

	restoreFinishCmd := []string{
		"/opt/percona/pbm", "restore-finish", restore.Status.PBMname, "-c", "/etc/pbm/pbm_config.yaml",
	}

	log := logf.FromContext(ctx)

	stdoutBuf := &bytes.Buffer{}
	stderrBuf := &bytes.Buffer{}
	err := retry.OnError(anotherOpBackoff, func(err error) bool {
		return strings.Contains(err.Error(), "another operation") ||
			strings.Contains(err.Error(), "unable to upgrade connection")
	}, func() error {
		log.Info("Finishing restore", "command", restoreFinishCmd, "pod", pod.Name)

		stdoutBuf.Reset()
		stderrBuf.Reset()

		err := r.clientcmd.Exec(ctx, &pod, "mongod", restoreFinishCmd, nil, stdoutBuf, stderrBuf, false)
		if err != nil {
			log.Error(nil, "Failed to finish restore", "pod", pod.Name, "stderr", stderrBuf.String(), "stdout", stdoutBuf.String())
			return errors.Wrapf(err, "restore-finish stderr: %s stdout: %s", stderrBuf.String(), stdoutBuf.String())
		}

		log.Info("Restore finished", "pod", pod.Name)

		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to finish restore: %w", err)
	}

	// Annotate the cluster so that we don't run this again.
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		observedCluster := &psmdbv1.PerconaServerMongoDB{}
		if err := r.client.Get(ctx, client.ObjectKeyFromObject(cluster), observedCluster); err != nil {
			return errors.Wrapf(err, "get observed cluster")
		}
		orig := observedCluster.DeepCopy()
		annots := observedCluster.GetAnnotations()
		if annots == nil {
			annots = make(map[string]string)
		}
		annots[naming.AnnotationRestoreFinished] = "true"
		observedCluster.SetAnnotations(annots)

		return r.client.Patch(ctx, observedCluster, client.MergeFrom(orig))
	}); err != nil {
		return errors.Wrapf(err, "set restore finished annotation")
	}
	return nil
}
