package perconaservermongodbbackup

import (
	"context"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type Backup struct {
	pbm  *backup.PBM
	spec api.BackupSpec
}

func (r *ReconcilePerconaServerMongoDBBackup) newBackup(cr *api.PerconaServerMongoDBBackup) (*Backup, error) {
	cluster := &api.PerconaServerMongoDB{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Spec.PSMDBCluster, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return nil, errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.PSMDBCluster)
	}

	cn, err := backup.NewPBM(r.client, cluster)
	if err != nil {
		return nil, errors.Wrap(err, "create pbm object")
	}

	return &Backup{
		pbm:  cn,
		spec: cluster.Spec.Backup,
	}, nil
}

// Start requests backup on PBM
func (b *Backup) Start(cr *api.PerconaServerMongoDBBackup, priority map[string]float64) (api.PerconaServerMongoDBBackupStatus, error) {
	var status api.PerconaServerMongoDBBackupStatus

	stg, ok := b.spec.Storages[cr.Spec.StorageName]
	if !ok {
		return status, errors.Errorf("unable to get storage '%s'", cr.Spec.StorageName)
	}

	err := b.pbm.SetConfig(stg, b.spec.PITR, priority)
	if err != nil {
		return api.PerconaServerMongoDBBackupStatus{}, errors.Wrapf(err, "set backup config with storage %s", cr.Spec.StorageName)
	}

	name := time.Now().UTC().Format(time.RFC3339)

	err = b.pbm.C.SendCmd(pbm.Cmd{
		Cmd: pbm.CmdBackup,
		Backup: pbm.BackupCmd{
			Name:        name,
			Compression: cr.Spec.Comperssion,
		},
	})
	if err != nil {
		return status, err
	}

	status = api.PerconaServerMongoDBBackupStatus{
		StorageName: cr.Spec.StorageName,
		PBMname:     name,
		LastTransition: &metav1.Time{
			Time: time.Unix(time.Now().Unix(), 0),
		},
		S3:    &stg.S3,
		State: api.BackupStateRequested,
	}

	if stg.S3.Prefix != "" {
		status.Destination = stg.S3.Prefix + "/"
	}
	status.Destination += status.PBMname

	return status, nil
}

// Status return backup status
func (b *Backup) Status(cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	status := cr.Status

	var (
		meta    *pbm.BackupMeta
		retries uint64
	)
	const maxRetries = 60
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// PBM 1.5.0 needs some sync up before we can read the backup meta
	for range ticker.C {
		retries++

		var err error
		meta, err = b.pbm.C.GetBackupMeta(cr.Status.PBMname)
		if err == nil {
			break
		}

		if errors.Is(err, pbm.ErrNotFound) {
			log.Info("Waiting for backup metadata", "PBM name", cr.Status.PBMname, "backup", cr.Name)
		} else {
			return status, errors.Wrap(err, "get pbm backup meta")
		}

		if retries >= maxRetries {
			break
		}
	}

	if meta == nil || meta.Name == "" {
		log.Info("Waiting for backup metadata", "PBM name", cr.Status.PBMname, "backup", cr.Name)
		return status, nil
	}

	if meta.StartTS > 0 {
		status.StartAt = &metav1.Time{
			Time: time.Unix(meta.StartTS, 0),
		}
	}

	switch meta.Status {
	case pbm.StatusError:
		status.State = api.BackupStateError
		status.Error = meta.Error
	case pbm.StatusDone:
		status.State = api.BackupStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(meta.LastTransitionTS, 0),
		}
	default:
		status.State = api.BackupStateRunning
	}

	status.LastTransition = &metav1.Time{
		Time: time.Unix(meta.LastTransitionTS, 0),
	}

	return status, nil
}

// Close closes the PBM connection
func (b *Backup) Close() error {
	return b.pbm.Close()
}
