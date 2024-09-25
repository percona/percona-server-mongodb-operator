package perconaservermongodbrestore

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"
	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

func (r *ReconcilePerconaServerMongoDBRestore) reconcileLogicalRestore(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore, bcp *psmdbv1.PerconaServerMongoDBBackup, cluster *psmdbv1.PerconaServerMongoDB) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	log := logf.FromContext(ctx)
	var err error

	status := cr.Status

	var (
		backupName  = bcp.Status.PBMname
		storageName = bcp.Spec.StorageName
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

	pbmc, err := backup.NewPBM(ctx, r.client, cluster)
	if err != nil {
		log.Info("Waiting for pbm-agent.")
		status.State = psmdbv1.RestoreStateWaiting
		return status, nil
	}
	defer pbmc.Close(ctx)

	if status.State == psmdbv1.RestoreStateNew || status.State == psmdbv1.RestoreStateWaiting {
		storage, err := r.getStorage(cr, cluster, storageName)
		if err != nil {
			return status, errors.Wrap(err, "get storage")
		}

		// Disable PITR before restore
		cluster.Spec.Backup.PITR.Enabled = false
		err = pbmc.SetConfig(ctx, r.client, cluster, storage)
		if err != nil {
			return status, errors.Wrap(err, "set pbm config")
		}

		isBlockedByPITR, err := pbmc.HasLocks(ctx, backup.IsPITRLock)
		if err != nil {
			return status, errors.Wrap(err, "checking pbm pitr locks")
		}

		if isBlockedByPITR {
			log.Info("Waiting for PITR to be disabled.")
			status.State = psmdbv1.RestoreStateWaiting
			return status, nil
		}

		log.Info("Starting restore", "backup", backupName)
		status.PBMname, err = runRestore(ctx, backupName, pbmc, cr.Spec.PITR, cr.Spec.Selective)
		status.State = psmdbv1.RestoreStateRequested
		return status, err
	}

	meta, err := pbmc.GetRestoreMeta(ctx, cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbmErrors.ErrNotFound) {
		return status, errors.Wrap(err, "get pbm metadata")
	}

	if meta == nil || meta.Name == "" {
		log.Info("Waiting for restore metadata", "pbmName", cr.Status.PBMname, "restore", cr.Name, "backup", cr.Spec.BackupName)
		return status, nil
	}

	switch meta.Status {
	case defs.StatusError:
		status.State = psmdbv1.RestoreStateError
		status.Error = meta.Error
		if err = reEnablePITR(ctx, pbmc, cluster.Spec.Backup); err != nil {
			return status, err
		}
	case defs.StatusDone:
		status.State = psmdbv1.RestoreStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(meta.LastTransitionTS, 0),
		}
		if err = reEnablePITR(ctx, pbmc, cluster.Spec.Backup); err != nil {
			return status, err
		}
	case defs.StatusStarting, defs.StatusRunning:
		status.State = psmdbv1.RestoreStateRunning
	}

	return status, nil
}

func reEnablePITR(ctx context.Context, pbm backup.PBM, backup psmdbv1.BackupSpec) (err error) {
	if !backup.IsEnabledPITR() {
		return
	}

	err = pbm.SetConfigVar(ctx, "pitr.enabled", "true")
	if err != nil {
		return
	}

	return
}

func runRestore(ctx context.Context, backup string, pbmc backup.PBM, pitr *psmdbv1.PITRestoreSpec, selective *psmdbv1.SelectiveRestoreOpts) (string, error) {
	log := logf.FromContext(ctx)
	log.Info("Starting logical restore", "backup", backup)

	e := pbmc.Logger().NewEvent(string(ctrl.CmdResync), "", "", primitive.Timestamp{})
	err := pbmc.ResyncStorage(ctx, e)
	if err != nil {
		return "", errors.Wrap(err, "set resync backup list from the store")
	}

	var (
		cmd   ctrl.Cmd
		rName = time.Now().UTC().Format(time.RFC3339Nano)
	)

	switch {
	case pitr == nil:
		cmd = ctrl.Cmd{
			Cmd: ctrl.CmdRestore,
			Restore: &ctrl.RestoreCmd{
				Name:          rName,
				BackupName:    backup,
				Namespaces:    selective.GetNamespaces(),
				UsersAndRoles: selective.GetWithUsersAndRoles(),
			},
		}
	case pitr.Type == psmdbv1.PITRestoreTypeDate:
		ts := pitr.Date.Unix()

		if _, err := pbmc.GetPITRChunkContains(ctx, ts); err != nil {
			return "", err
		}

		cmd = ctrl.Cmd{
			Cmd: ctrl.CmdRestore,
			Restore: &ctrl.RestoreCmd{
				Name:          rName,
				BackupName:    backup,
				OplogTS:       primitive.Timestamp{T: uint32(ts)},
				Namespaces:    selective.GetNamespaces(),
				UsersAndRoles: selective.GetWithUsersAndRoles(),
			},
		}
	case pitr.Type == psmdbv1.PITRestoreTypeLatest:
		tl, err := pbmc.GetLatestTimelinePITR(ctx)
		if err != nil {
			return "", err
		}

		cmd = ctrl.Cmd{
			Cmd: ctrl.CmdRestore,
			Restore: &ctrl.RestoreCmd{
				Name:          rName,
				BackupName:    backup,
				OplogTS:       primitive.Timestamp{T: tl.End},
				Namespaces:    selective.GetNamespaces(),
				UsersAndRoles: selective.GetWithUsersAndRoles(),
			},
		}
	}

	log.Info("Sending restore command", "restoreCmd", cmd.Restore)
	if err = pbmc.SendCmd(ctx, cmd); err != nil {
		return "", errors.Wrap(err, "send restore cmd")
	}

	return rName, nil
}
