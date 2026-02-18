package perconaservermongodbrestore

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
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
		return r.reconcileSnapshotRunning(ctx, cr, cluster)
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

	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}
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

	replsets := cluster.Spec.Replsets
	if cluster.Spec.Sharding.Enabled {
		replsets = append(replsets, cluster.Spec.Sharding.ConfigsvrReplSet)
	}

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
) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {

	// The exact steps that need to take place here:
	// 1. Scale down cfg and rs statefulsets and wait for it to be scaled down.
	// 2. Update the StatefulSet volumeClaimTemplates to use the snapshot as data source
	// 3. Update the StatefulSet pbm-agent arg as follows:
	//		```
	//		 pbm-agent restore-finish <restore_name> -c <pbm-config.yaml> --rs <rs_name> --node <node_name> --db-config <db-config.yaml>
	// 		```
	//   *  rs:        Specifed in MONGODB_REPLSET environment variable
	//   *  node_name: Specifed as $POD_NAME.$SERVICE_NAME-MONGODB_REPLSET.$NAMESPACE.svc.cluster.local
	// 4. Scale up cfg and rs statefulsets and wait
	// 5. Exec the following command to finish the restore:
	//		```
	// 		/opt/percona/pbm restore-finish <restore_name> -c <pbm-config.yaml>
	// 		```
	// 6. Delete all statefulsets
	// 7. Add resync storage annotation
	return restore.Status, nil
}
