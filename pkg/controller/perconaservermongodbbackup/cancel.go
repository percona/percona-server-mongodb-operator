package perconaservermongodbbackup

import (
	"context"
	"time"

	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/pkg/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func cancellationRequested(cr *api.PerconaServerMongoDBBackup) bool {
	return cr.DeletionTimestamp != nil || cr.Annotations[naming.AnnotationCancelBackup] == "true" || cr.Status.State == api.BackupStateCanceling
}

func (r *ReconcilePerconaServerMongoDBBackup) reconcileCancellation(ctx context.Context, cr *api.PerconaServerMongoDBBackup) (reconcile.Result, error) {
	result := reconcile.Result{RequeueAfter: r.reconcileIn}
	if cr.Status.State != api.BackupStateCanceling {
		cr.Status.State = api.BackupStateCanceling
		cr.Status.LastTransition = &metav1.Time{Time: time.Now().UTC()}
		if err := r.updateStatus(ctx, cr); err != nil {
			return reconcile.Result{}, err
		}
	}

	cr.Status.Error = ""
	err := r.cancelBackup(ctx, cr)
	if err != nil {
		cr.Status.State = api.BackupStateCanceling
		cr.Status.Error = err.Error()
	}
	if updateErr := r.updateStatus(ctx, cr); updateErr != nil {
		return reconcile.Result{}, updateErr
	}
	if !backupStateTerminal(cr.Status.State) {
		return result, err
	}
	if err := r.runReleaseLockFinalizer(ctx, cr); err != nil {
		return reconcile.Result{}, err
	}
	if cr.DeletionTimestamp != nil {
		return result, nil
	}
	return reconcile.Result{}, nil
}

func (r *ReconcilePerconaServerMongoDBBackup) cancelBackup(ctx context.Context, cr *api.PerconaServerMongoDBBackup) error {
	if cr.Status.PBMname == "" {
		cr.Status.State = api.BackupStateCanceled
		cr.Status.LastTransition = &metav1.Time{Time: time.Now().UTC()}
		return nil
	}

	cluster := new(api.PerconaServerMongoDB)
	if err := r.apiReader.Get(ctx, client.ObjectKey{Namespace: cr.Namespace, Name: cr.Spec.GetClusterName()}, cluster); err != nil {
		if k8serrors.IsNotFound(err) {
			if cr.Spec.Type == defs.ExternalBackup {
				done, err := cleanupCanceledSnapshots(ctx, r.client, cr)
				if err != nil || !done {
					return err
				}
				cr.Status.Snapshots = nil
			}
			cr.Status.State = api.BackupStateError
			cr.Status.Error = "cluster not found during backup cancellation"
			cr.Status.LastTransition = &metav1.Time{Time: time.Now().UTC()}
			return nil
		}
		return errors.Wrap(err, "get cluster for backup cancellation")
	}
	if len(cluster.Spec.Replsets) == 0 {
		return errors.New("cluster has no replica sets")
	}
	svr, err := version.Server(ctx, r.clientcmd)
	if err != nil {
		return errors.Wrap(err, "fetch server version for backup cancellation")
	}
	if err := cluster.CheckNSetDefaults(ctx, svr.Platform); err != nil {
		return errors.Wrap(err, "check cluster defaults for backup cancellation")
	}

	pbm, err := r.newPBMFunc(ctx, r.client, cluster)
	if err != nil {
		return errors.Wrap(err, "connect to PBM for backup cancellation")
	}
	defer func() {
		if err := pbm.Close(ctx); err != nil {
			logf.FromContext(ctx).Error(err, "close PBM after backup cancellation")
		}
	}()

	meta, err := pbm.GetBackupMeta(ctx, cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbmErrors.ErrNotFound) {
		return errors.Wrap(err, "get pbm backup meta")
	}
	if errors.Is(err, pbmErrors.ErrNotFound) || meta == nil || meta.Name == "" {
		meta = nil
	}

	var status api.PerconaServerMongoDBBackupStatus
	if cr.Spec.Type == defs.ExternalBackup {
		status, err = (&snapshotBackups{pbm: pbm}).statusFromMeta(ctx, r.client, cluster, cr, meta)
	} else {
		status, err = (&managedBackups{pbm: pbm}).statusFromMeta(ctx, cluster, cr, meta)
	}
	if err != nil {
		return err
	}

	if status.State == api.BackupStateCanceling && (meta == nil || (meta.Status != defs.StatusStarting && meta.Status != defs.StatusCancelled)) {
		if err := checkCancellationTimeout(ctx, r.client, cluster, cr, pbm, &status, meta); err != nil {
			return err
		}
	}
	if cr.Spec.Type == defs.ExternalBackup && status.State == api.BackupStateError {
		done, err := cleanupCanceledSnapshots(ctx, r.client, cr)
		if err != nil || !done {
			return err
		}
		status.Snapshots = nil
	}
	cr.Status = status
	if backupStateTerminal(status.State) {
		return nil
	}
	cr.Status.State = api.BackupStateCanceling

	return pbm.CancelBackup(ctx, cr.Status.PBMname)
}

func checkCancellationTimeout(ctx context.Context, cl client.Client, cluster *api.PerconaServerMongoDB, cr *api.PerconaServerMongoDBBackup, pbm backup.PBM, status *api.PerconaServerMongoDBBackupStatus, meta *backup.BackupMeta) error {
	if status.State != api.BackupStateCanceling {
		return nil
	}
	if status.LastTransition == nil {
		status.LastTransition = &metav1.Time{Time: time.Now().UTC()}
		return nil
	}
	deadline := defaultPBMStartingDeadline
	if s := cluster.Spec.Backup.StartingDeadlineSeconds; s != nil && *s > 0 {
		deadline = *s
	}
	lastProgress := status.LastTransition.Time
	opID := ""
	if meta != nil {
		opID = meta.OPID
		for _, ts := range []int64{meta.LastTransitionTS, int64(meta.Hb.T)} {
			if progress := time.Unix(ts, 0); progress.After(lastProgress) {
				lastProgress = progress
			}
		}
	}
	if time.Since(lastProgress) < time.Duration(deadline)*time.Second {
		return nil
	}
	hasLock, err := pbm.HasBackupLock(ctx, opID)
	if err != nil {
		return errors.Wrap(err, "check backup locks during cancellation recovery")
	}
	if hasLock {
		return nil
	}
	if cr.Spec.Type == defs.ExternalBackup {
		done, err := cleanupCanceledSnapshots(ctx, cl, cr)
		if err != nil || !done {
			return err
		}
		status.Snapshots = nil
	}
	status.State = api.BackupStateError
	status.Error = "PBM backup metadata not found during cancellation within the allowed timeout"
	if meta != nil {
		status.Error = "PBM backup agent stopped responding before cancellation could finish"
	}
	status.LastTransition = &metav1.Time{Time: time.Now().UTC()}
	return nil
}

// cleanupCanceledSnapshots deletes a canceled backup's VolumeSnapshots and waits until they are gone
func cleanupCanceledSnapshots(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDBBackup) (bool, error) {
	names := make(map[string]struct{})
	for _, snapshot := range cr.Status.Snapshots {
		names[snapshot.SnapshotName] = struct{}{}
	}
	for _, rs := range cr.Status.ReplsetNames {
		names[naming.VolumeSnapshotName(cr, rs)] = struct{}{}
	}
	done := true
	for name := range names {
		snapshot := &volumesnapshotv1.VolumeSnapshot{}
		if err := cl.Get(ctx, client.ObjectKey{Namespace: cr.Namespace, Name: name}, snapshot); err != nil {
			if client.IgnoreNotFound(err) != nil {
				return false, errors.Wrap(err, "get canceled backup snapshot")
			}
			continue
		}
		done = false
		if snapshot.DeletionTimestamp == nil {
			if err := cl.Delete(ctx, snapshot); client.IgnoreNotFound(err) != nil {
				return false, errors.Wrap(err, "delete canceled backup snapshot")
			}
		}
	}
	return done, nil
}
