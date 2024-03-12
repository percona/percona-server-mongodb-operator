package perconaservermongodbrestore

import (
	"context"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
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

	pbmClient, err := pbm.New(ctx, r.clientcmd, r.client, cluster)
	if err != nil {
		return status, errors.Wrap(err, "create pbm client")
	}

	running, err := pbmClient.GetRunningOperation(ctx)
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

	backupName := bcp.Status.PBMName

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
		err = pbmClient.DisablePITR(ctx)
		if err != nil {
			return status, errors.Wrap(err, "set pbm config")
		}

		isBlockedByPITR, err := pbmClient.IsPITRRunning(ctx)
		if err != nil {
			return status, errors.Wrap(err, "check if PITR is running")
		}

		if isBlockedByPITR {
			log.Info("Waiting for PITR to be disabled.")
			status.State = psmdbv1.RestoreStateWaiting
			return status, nil
		}

		status.PBMName, err = runRestore(ctx, pbmClient, backupName, cr.Spec.PITR)
		status.State = psmdbv1.RestoreStateRequested

		log.Info("Restore is requested", "backup", backupName, "restore", status.PBMName, "pitr", cr.Spec.PITR != nil)

		return status, err
	}

	var restore pbm.DescribeRestoreResponse
	err = retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
		restore, err = pbmClient.DescribeRestore(ctx, pbm.DescribeRestoreOptions{Name: cr.Status.PBMName})
		return err
	})
	if err != nil {
		return status, errors.Wrap(err, "describe restore")
	}

	log.V(1).Info("Restore status", "status", restore)

	switch restore.Status {
	case defs.StatusError:
		status.State = psmdbv1.RestoreStateError
		status.Error = restore.Error
		if cluster.Spec.Backup.PITR.Enabled {
			log.Info("Enabling PITR after restore finished with error")
			if err := pbmClient.EnablePITR(ctx); err != nil {
				return status, errors.Wrap(err, "enable PITR")
			}
		}
	case defs.StatusDone:
		status.State = psmdbv1.RestoreStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(restore.LastTransitionTS, 0),
		}
		if cluster.Spec.Backup.PITR.Enabled {
			log.Info("Enabling PITR after restore finished with success")
			if err := pbmClient.EnablePITR(ctx); err != nil {
				return status, errors.Wrap(err, "enable PITR")
			}
		}
	case defs.StatusStarting, defs.StatusRunning:
		status.State = psmdbv1.RestoreStateRunning
	}

	return status, nil
}

func runRestore(ctx context.Context, pbmClient *pbm.PBM, backup string, pitr *psmdbv1.PITRestoreSpec) (string, error) {
	opts := pbm.RestoreOptions{
		BackupName: backup,
	}

	if pitr != nil {
		switch pitr.Type {
		case psmdbv1.PITRestoreTypeDate:
			opts = pbm.RestoreOptions{
				Time: pitr.Date.String(),
			}
		case psmdbv1.PITRestoreTypeLatest:
			latest, err := pbmClient.LatestPITRChunk(ctx)
			if err != nil {
				return "", errors.Wrap(err, "get latest PITR chunk")
			}
			opts = pbm.RestoreOptions{
				Time: latest,
			}
		}
	}

	restore, err := pbmClient.RunRestore(ctx, opts)
	if err != nil {
		return "", errors.Wrap(err, "run restore")
	}

	return restore.Name, nil
}
