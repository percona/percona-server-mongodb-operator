package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

const (
	// pbmStartingDeadline is timeout after which continuous starting state is considered as error
	pbmStartingDeadline       = time.Duration(120) * time.Second
	pbmStartingDeadlineErrMsg = "starting deadline exceeded"
)

type Backup struct {
	pbm  *backup.PBM
	spec api.BackupSpec
}

func (r *ReconcilePerconaServerMongoDBBackup) newBackup(ctx context.Context, cluster *api.PerconaServerMongoDB) (*Backup, error) {
	if cluster == nil {
		return new(Backup), nil
	}
	cn, err := backup.NewPBM(ctx, r.client, cluster)
	if err != nil {
		return nil, errors.Wrap(err, "create pbm object")
	}

	return &Backup{pbm: cn, spec: cluster.Spec.Backup}, nil
}

// Start requests backup on PBM
func (b *Backup) Start(ctx context.Context, k8sclient client.Client, cluster *api.PerconaServerMongoDB, cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	var status api.PerconaServerMongoDBBackupStatus

	stg, ok := b.spec.Storages[cr.Spec.StorageName]
	if !ok {
		return status, errors.Errorf("unable to get storage '%s'", cr.Spec.StorageName)
	}

	err := b.pbm.SetConfig(ctx, k8sclient, cluster, stg)
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
		Backup: &pbm.BackupCmd{
			Name:             name,
			Type:             cr.Spec.Type,
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
		State: api.BackupStateRequested,
	}
	if cluster.Spec.Sharding.Enabled && cluster.Spec.Sharding.ConfigsvrReplSet != nil {
		status.ReplsetNames = append(status.ReplsetNames, cluster.Spec.Sharding.ConfigsvrReplSet.Name)
	}
	for _, rs := range cluster.Spec.Replsets {
		status.ReplsetNames = append(status.ReplsetNames, rs.Name)
	}

	switch stg.Type {
	case api.BackupStorageS3:
		status.S3 = &stg.S3
		if stg.S3.Prefix != "" {
			status.Destination = stg.S3.Prefix + "/"
		}
	case api.BackupStorageAzure:
		status.Azure = &stg.Azure
		if stg.Azure.Prefix != "" {
			status.Destination = stg.Azure.Prefix + "/"
		}
	}
	status.Destination += status.PBMname

	return status, nil
}

// Status return backup status
func (b *Backup) Status(ctx context.Context, cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	status := cr.Status

	meta, err := b.pbm.C.GetBackupMeta(cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbm.ErrNotFound) {
		return status, errors.Wrap(err, "get pbm backup meta")
	}

	if meta == nil || meta.Name == "" || errors.Is(err, pbm.ErrNotFound) {
		logf.FromContext(ctx).Info("Waiting for backup metadata", "PBM name", cr.Status.PBMname, "backup", cr.Name)
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
		status.Error = fmt.Sprintf("%v", meta.Error())
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
	status.Type = cr.Spec.Type

	return status, nil
}

// Close closes the PBM connection
func (b *Backup) Close(ctx context.Context) error {
	if b.pbm == nil {
		return nil
	}
	return b.pbm.Close(ctx)
}
