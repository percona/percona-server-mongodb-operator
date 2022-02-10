package perconaservermongodbbackup

import (
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// pbmStartingDeadline is timeout after which continuous starting state is considered as error
	pbmStartingDeadline       = time.Duration(40)
	pbmStartingDeadlineErrMsg = "starting deadline exceeded"
)

type Backup struct {
	pbm  *backup.PBM
	spec api.BackupSpec
}

func (r *ReconcilePerconaServerMongoDBBackup) newBackup(
	cluster *api.PerconaServerMongoDB,
	cr *api.PerconaServerMongoDBBackup,
) (*Backup, error) {
	cn, err := backup.NewPBM(r.client, cluster)
	if err != nil {
		return nil, errors.Wrap(err, "create pbm object")
	}

	return &Backup{pbm: cn, spec: cluster.Spec.Backup}, nil
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

	var compLevel *int
	if cr.Spec.CompressionLevel != nil {
		l := int(*cr.Spec.CompressionLevel)
		compLevel = &l
	}

	err = b.pbm.C.SendCmd(pbm.Cmd{
		Cmd: pbm.CmdBackup,
		Backup: pbm.BackupCmd{
			Name:             name,
			Compression:      cr.Spec.Compression,
			CompressionLevel: compLevel,
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
		Azure: &stg.Azure,
		State: api.BackupStateRequested,
	}

	if stg.S3.Prefix != "" {
		status.Destination = stg.S3.Prefix + "/"
	}

	if stg.Azure.Prefix != "" {
		status.Destination = stg.Azure.Prefix + "/"
	}
	status.Destination += status.PBMname

	return status, nil
}

// Status return backup status
func (b *Backup) Status(cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	status := cr.Status

	meta, err := b.pbm.C.GetBackupMeta(cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbm.ErrNotFound) {
		return status, errors.Wrap(err, "get pbm backup meta")
	}

	if meta == nil || meta.Name == "" || errors.Is(err, pbm.ErrNotFound) {
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
	case pbm.StatusStarting:
		passed := time.Now().UTC().Sub(time.Unix(meta.StartTS, 0))
		if passed >= pbmStartingDeadline {
			status.State = api.BackupStateError
			status.Error = pbmStartingDeadlineErrMsg
			break
		}

		status.State = api.BackupStateRequested
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
