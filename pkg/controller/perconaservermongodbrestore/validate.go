package perconaservermongodbrestore

import (
	"context"

	"github.com/pkg/errors"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

var (
	errWaitingPBM = errors.New("waiting for pbm-agent")
)

func (r *ReconcilePerconaServerMongoDBRestore) validate(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore, cluster *psmdbv1.PerconaServerMongoDB) error {
	if cluster.Spec.Unmanaged {
		return errors.New("cluster is unmanaged")
	}

	bcp, err := r.getBackup(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get backup")
	}

	if bcp.Status.Type != defs.LogicalBackup && cr.Spec.Selective != nil {
		return errors.New("`.spec.selective` field is supported only for logical backups")
	}

	storage, err := r.getStorage(cr, cluster, bcp.Spec.StorageName)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	pbmc, err := r.newPBMFunc(ctx, r.client, cluster)
	if err != nil {
		return errWaitingPBM
	}
	defer pbmc.Close(ctx)

	cfg, err := backup.GetPBMConfig(ctx, r.client, cluster, storage)
	if err != nil {
		return errors.Wrap(err, "get pbm config")
	}

	if err := pbmc.ValidateBackup(ctx, &cfg, bcp); err != nil {
		return errors.Wrap(err, "failed to validate backup")
	}

	pitr := cr.Spec.PITR
	if pitr == nil {
		return nil
	}

	switch {
	case pitr.Type == psmdbv1.PITRestoreTypeDate && pitr.Date != nil:
		if bcp.Status.LastWriteAt != nil {
			if pitr.Date.Equal(bcp.Status.LastWriteAt) {
				return errors.New("backup's last write is equal to target time")
			}
			if pitr.Date.Before(bcp.Status.LastWriteAt) {
				return errors.New("backup's last write is later than target time")
			}
		}

		ts := pitr.Date.Unix()
		if _, err := pbmc.GetPITRChunkContains(ctx, ts); err != nil {
			return err
		}
	case pitr.Type == psmdbv1.PITRestoreTypeLatest:
		_, err := pbmc.GetLatestTimelinePITR(ctx)
		if err != nil {
			return err
		}
	}

	return nil
}
