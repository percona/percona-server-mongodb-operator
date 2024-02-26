package perconaservermongodbrestore

import (
	"context"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pbm"
	"github.com/percona/percona-server-mongodb-operator/version"
)

func (r *ReconcilePerconaServerMongoDBRestore) reconcileLogicalRestore(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore, bcp *psmdbv1.PerconaServerMongoDBBackup) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	log := logf.FromContext(ctx)

	status := cr.Status

	cluster := &psmdbv1.PerconaServerMongoDB{}
	err := r.client.Get(ctx, types.NamespacedName{Name: cr.Spec.ClusterName, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return status, errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.ClusterName)
	}

	if cluster.Spec.Unmanaged {
		return status, errors.New("cluster is unmanaged")
	}

	svr, err := version.Server()
	if err != nil {
		return status, errors.Wrapf(err, "fetch server version")
	}

	if err := cluster.CheckNSetDefaults(svr.Platform, log); err != nil {
		return status, errors.Wrapf(err, "set defaults for %s/%s", cluster.Namespace, cluster.Name)
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cluster.Name + "-" + cluster.Spec.Replsets[0].Name + "-0",
			Namespace: cluster.Namespace,
		},
	}
	err = r.client.Get(ctx, client.ObjectKeyFromObject(pod), pod)
	if err != nil {
		return status, errors.Wrapf(err, "get pod %s", client.ObjectKeyFromObject(pod))
	}

	running, err := pbm.GetRunningOperation(ctx, r.clientcmd, pod)
	if err != nil {
		return status, errors.Wrap(err, "check for concurrent jobs")
	}
	// for some reason PBM returns backup name for restore operation
	if running.Name != bcp.Status.PBMName && running.Name != "" {
		if cr.Status.State != psmdbv1.RestoreStateWaiting {
			log.Info("waiting to finish another backup/restore.", "running", running.Name, "type", running.Type, "opid", running.OpID)
		}
		status.State = psmdbv1.RestoreStateWaiting
		return status, nil
	}

	var (
		backupName = bcp.Status.PBMName
		// storageName = bcp.Spec.StorageName
	)

	if cluster.Spec.Sharding.Enabled {
		mongos := appsv1.Deployment{}
		err = r.client.Get(ctx, cluster.MongosNamespacedName(), &mongos)
		if err != nil && !k8serrors.IsNotFound(err) {
			return status, errors.Wrapf(err, "failed to get mongos")
		}

		if err == nil {
			log.Info("waiting for mongos termination")

			status.State = psmdbv1.RestoreStateWaiting
			return status, nil
		}
	}

	if status.State == psmdbv1.RestoreStateNew || status.State == psmdbv1.RestoreStateWaiting {
		err = pbm.DisablePITR(ctx, r.clientcmd, pod)
		if err != nil {
			return status, errors.Wrap(err, "set pbm config")
		}

		isBlockedByPITR, err := pbm.IsPITRRunning(ctx, r.clientcmd, pod)
		if err != nil {
			return status, errors.Wrap(err, "check if PITR is running")
		}

		if isBlockedByPITR {
			log.Info("Waiting for PITR to be disabled.")
			status.State = psmdbv1.RestoreStateWaiting
			return status, nil
		}

		status.PBMName, err = runRestore(ctx, r.clientcmd, r.client, pod, backupName, cr.Spec.PITR)
		status.State = psmdbv1.RestoreStateRequested

		log.Info("Restore is requested", "backup", backupName, "restore", status.PBMName, "pitr", cr.Spec.PITR != nil)

		return status, err
	}

	var restore pbm.DescribeRestoreResponse
	err = retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
		restore, err = pbm.DescribeRestore(ctx, r.clientcmd, pod, pbm.DescribeRestoreOptions{Name: cr.Status.PBMName})
		return err
	})
	if err != nil {
		return status, errors.Wrap(err, "describe restore")
	}

	log.Info("Restore status", "status", restore.Status, "error", restore.Error)

	switch restore.Status {
	case defs.StatusError:
		status.State = psmdbv1.RestoreStateError
		status.Error = restore.Error
		// if err = reEnablePITR(pbmc, cluster.Spec.Backup); err != nil {
		// 	return status, err
		// }
	case defs.StatusDone:
		status.State = psmdbv1.RestoreStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(restore.LastTransitionTS, 0),
		}
		// if err = reEnablePITR(pbmc, cluster.Spec.Backup); err != nil {
		// 	return status, err
		// }
	case defs.StatusStarting, defs.StatusRunning:
		status.State = psmdbv1.RestoreStateRunning
	}

	return status, nil
}

func runRestore(ctx context.Context, cli *clientcmd.Client, k8sclient client.Client, pod *corev1.Pod, backup string, pitr *psmdbv1.PITRestoreSpec) (string, error) {
	opts := pbm.RestoreOptions{
		BackupName: backup,
	}

	// TODO: Implement PITR

	restore, err := pbm.RunRestore(ctx, cli, pod, opts)
	if err != nil {
		return "", errors.Wrap(err, "run restore")
	}

	return restore.Name, nil
}
