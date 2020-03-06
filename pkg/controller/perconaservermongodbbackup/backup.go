package perconaservermongodbbackup

import (
	"fmt"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

type Backup struct {
	pbm *backup.PBM
}

func (r *ReconcilePerconaServerMongoDBBackup) newBackup(cr *api.PerconaServerMongoDBBackup) (*Backup, error) {
	cn, err := backup.NewPBM(r.client, cr.Spec.PSMDBCluster, cr.Spec.Replset, cr.Namespace)
	if err != nil {
		return nil, errors.Wrap(err, "create pbm object")
	}

	return &Backup{pbm: cn}, nil
}

func (b *Backup) Start(cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	err := b.pbm.SetConfig(cr)
	if err != nil {
		return api.PerconaServerMongoDBBackupStatus{}, fmt.Errorf("set backup config: %v", err)
	}

	backupStatus := api.PerconaServerMongoDBBackupStatus{
		StorageName: cr.Spec.StorageName,
		PBMname:     time.Now().UTC().Format(time.RFC3339),
		LastTransition: &metav1.Time{
			Time: time.Unix(time.Now().Unix(), 0),
		},
	}

	err = b.pbm.C.SendCmd(pbm.Cmd{
		Cmd: pbm.CmdBackup,
		Backup: pbm.BackupCmd{
			Name:        backupStatus.PBMname,
			Compression: cr.Spec.Comperssion,
		},
	})
	if err != nil {
		return backupStatus, err
	}

	backupStatus.State = api.BackupStateRequested
	return backupStatus, nil
}

func (b *Backup) Status(cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	status := cr.Status

	meta, err := b.pbm.C.GetBackupMeta(cr.Status.PBMname)
	if err != nil {
		return status, errors.Wrap(err, "get pbm backup meta")
	}
	if meta == nil || meta.Name == "" {
		log.Info("No backup found", "PBM name", cr.Status.PBMname, "backup", cr.Name)
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

	switch meta.Store.Type {
	case pbm.StorageS3:
		status.Destination = "s3://"
		if meta.Store.S3.EndpointURL != "" {
			status.Destination += meta.Store.S3.EndpointURL + "/"
		}
		status.Destination += meta.Store.S3.Bucket
		if meta.Store.S3.Prefix != "" {
			status.Destination += "/" + meta.Store.S3.Prefix
		}
	case pbm.StorageFilesystem:
		status.Destination = meta.Store.Filesystem.Path
	}

	return status, nil
}

func (b *Backup) Close() error {
	return b.pbm.Close()
}
