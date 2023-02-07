package perconaservermongodbrestore

import (
	"context"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"
	appsv1 "k8s.io/api/apps/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
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

	cjobs, err := backup.HasActiveJobs(ctx, r.client, cluster, backup.NewRestoreJob(cr), backup.NotPITRLock)
	if err != nil {
		return status, errors.Wrap(err, "check for concurrent jobs")
	}
	if cjobs {
		if cr.Status.State != psmdbv1.RestoreStateWaiting {
			log.Info("waiting to finish another backup/restore.")
		}
		status.State = psmdbv1.RestoreStateWaiting
		return status, nil
	}

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

		isBlockedByPITR, err := pbmc.HasLocks(backup.IsPITRLock)
		if err != nil {
			return status, errors.Wrap(err, "checking pbm pitr locks")
		}

		if isBlockedByPITR {
			log.Info("Waiting for PITR to be disabled.")
			status.State = psmdbv1.RestoreStateWaiting
			return status, nil
		}

		log.Info("Starting restore")
		status.PBMname, err = runRestore(ctx, backupName, pbmc, cr.Spec.PITR)
		status.State = psmdbv1.RestoreStateRequested
		return status, err
	}

	meta, err := pbmc.C.GetRestoreMeta(cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbm.ErrNotFound) {
		return status, errors.Wrap(err, "get pbm metadata")
	}

	if meta == nil || meta.Name == "" {
		log.Info("Waiting for restore metadata", "PBM name", cr.Status.PBMname, "restore", cr.Name, "backup", cr.Spec.BackupName)
		return status, nil
	}

	switch meta.Status {
	case pbm.StatusError:
		status.State = psmdbv1.RestoreStateError
		status.Error = meta.Error
		if err = reEnablePITR(pbmc, cluster.Spec.Backup); err != nil {
			return status, err
		}
	case pbm.StatusDone:
		status.State = psmdbv1.RestoreStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(meta.LastTransitionTS, 0),
		}
		if err = reEnablePITR(pbmc, cluster.Spec.Backup); err != nil {
			return status, err
		}
	case pbm.StatusStarting, pbm.StatusRunning:
		status.State = psmdbv1.RestoreStateRunning
	}

	return status, nil
}

func reEnablePITR(pbm *backup.PBM, backup psmdbv1.BackupSpec) (err error) {
	if !backup.IsEnabledPITR() {
		return
	}

	err = pbm.C.SetConfigVar("pitr.enabled", "true")
	if err != nil {
		return
	}

	return
}

func runRestore(ctx context.Context, backup string, pbmc *backup.PBM, pitr *psmdbv1.PITRestoreSpec) (string, error) {
	e := pbmc.C.Logger().NewEvent(string(pbm.CmdResync), "", "", primitive.Timestamp{})
	err := pbmc.C.ResyncStorage(e)
	if err != nil {
		return "", errors.Wrap(err, "set resync backup list from the store")
	}

	var (
		cmd   pbm.Cmd
		rName = time.Now().UTC().Format(time.RFC3339Nano)
	)

	switch {
	case pitr == nil:
		cmd = pbm.Cmd{
			Cmd: pbm.CmdRestore,
			Restore: &pbm.RestoreCmd{
				Name:       rName,
				BackupName: backup,
			},
		}
	case pitr.Type == psmdbv1.PITRestoreTypeDate:
		ts := pitr.Date.Unix()

		if _, err := pbmc.GetPITRChunkContains(ctx, ts); err != nil {
			return "", err
		}

		cmd = pbm.Cmd{
			Cmd: pbm.CmdPITRestore,
			PITRestore: &pbm.PITRestoreCmd{
				Name: rName,
				TS:   ts,
			},
		}
	case pitr.Type == psmdbv1.PITRestoreTypeLatest:
		tl, err := pbmc.GetLatestTimelinePITR()
		if err != nil {
			return "", err
		}

		cmd = pbm.Cmd{
			Cmd: pbm.CmdPITRestore,
			PITRestore: &pbm.PITRestoreCmd{
				Name: rName,
				TS:   int64(tl.End),
			},
		}
	}

	if err = pbmc.C.SendCmd(cmd); err != nil {
		return "", errors.Wrap(err, "send restore cmd")
	}

	return rName, nil
}
