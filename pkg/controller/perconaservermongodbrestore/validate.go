package perconaservermongodbrestore

import (
	"context"

	"github.com/pkg/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

var (
	errWaitingPBM     = errors.New("waiting for pbm-agent")
	errWaitingRestore = errors.New("waiting for restore to finish")
)

func (r *ReconcilePerconaServerMongoDBRestore) validate(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore, cluster *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)
	if cluster.Spec.Unmanaged {
		return errors.New("cluster is unmanaged")
	}

	bcp, err := r.getBackup(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get backup")
	}

	// TODO: remove this if statement after https://perconadev.atlassian.net/browse/PBM-1360 is fixed
	if bcp.Status.Type != defs.PhysicalBackup {
		cjobs, err := backup.HasActiveJobs(ctx, r.newPBMFunc, r.client, cluster, backup.NewRestoreJob(cr), backup.NotPITRLock)
		if err != nil {
			return errors.Wrap(err, "check for concurrent jobs")
		}
		if cjobs {
			if cr.Status.State != psmdbv1.RestoreStateWaiting {
				log.Info("waiting to finish another backup/restore.")
			}
			return errWaitingRestore
		}
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
		log.Info("Waiting for pbm-agent.")
		return errWaitingPBM
	}
	defer pbmc.Close(ctx)

	cfg, err := backup.GetPBMConfig(ctx, r.client, cluster, storage)
	if err != nil {
		return errors.Wrap(err, "get pbm config")
	}

	if err := pbmc.ValidateBackup(ctx, bcp, cfg); err != nil {
		return errors.Wrap(err, "failed to validate backup")
	}
	return nil
}
