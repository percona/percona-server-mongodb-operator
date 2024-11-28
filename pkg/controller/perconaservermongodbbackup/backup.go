package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	pbmBackup "github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

const (
	// pbmStartingDeadline is timeout after which continuous starting state is considered as error
	pbmStartingDeadline       = time.Duration(120) * time.Second
	pbmStartingDeadlineErrMsg = "starting deadline exceeded"
)

type Backup struct {
	pbm  backup.PBM
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
	log := logf.FromContext(ctx)
	log.Info("Starting backup", "backup", cr.Name, "storage", cr.Spec.StorageName)

	var status api.PerconaServerMongoDBBackupStatus

	stg, ok := b.spec.Storages[cr.Spec.StorageName]
	if !ok {
		return status, errors.Errorf("unable to get storage '%s'", cr.Spec.StorageName)
	}

	err := b.pbm.GetNSetConfig(ctx, k8sclient, cluster, stg)
	if err != nil {
		return api.PerconaServerMongoDBBackupStatus{}, errors.Wrapf(err, "set backup config with storage %s", cr.Spec.StorageName)
	}

	name := time.Now().UTC().Format(time.RFC3339)

	var compLevel *int
	if cr.Spec.CompressionLevel != nil {
		l := int(*cr.Spec.CompressionLevel)
		compLevel = &l
	}

	cmd := ctrl.Cmd{
		Cmd: ctrl.CmdBackup,
		Backup: &ctrl.BackupCmd{
			Name:             name,
			Type:             cr.Spec.Type,
			Compression:      cr.Spec.Compression,
			CompressionLevel: compLevel,
		},
	}
	log.Info("Sending backup command", "backupCmd", cmd)
	err = b.pbm.SendCmd(ctx, cmd)
	if err != nil {
		return status, err
	}
	status.State = api.BackupStateRequested

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

		status.Destination = stg.S3.Bucket

		if stg.S3.Prefix != "" {
			status.Destination = stg.S3.Bucket + "/" + stg.S3.Prefix
		}
		if !strings.HasPrefix(stg.S3.Bucket, "s3://") {
			status.Destination = "s3://" + status.Destination
		}
	case api.BackupStorageAzure:
		status.Azure = &stg.Azure

		status.Destination = stg.Azure.Container

		if stg.Azure.Prefix != "" {
			status.Destination = stg.Azure.Container + "/" + stg.Azure.Prefix
		}
		if !strings.HasPrefix(stg.Azure.Container, "azure://") {
			if stg.Azure.EndpointURL != "" {
				status.Destination = stg.Azure.EndpointURL + "/" + status.Destination
			} else {
				status.Destination = "azure://" + status.Destination
			}
		}
	case api.BackupStorageFilesystem:
		status.Filesystem = &stg.Filesystem
		status.Destination = strings.TrimSuffix(stg.Filesystem.Path, "/")
	}
	status.Destination += "/" + status.PBMname

	return status, nil
}

// Status return backup status
func (b *Backup) Status(ctx context.Context, cr *api.PerconaServerMongoDBBackup) (api.PerconaServerMongoDBBackupStatus, error) {
	status := cr.Status

	meta, err := b.pbm.GetBackupMeta(ctx, cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbmErrors.ErrNotFound) {
		return status, errors.Wrap(err, "get pbm backup meta")
	}

	if meta == nil || meta.Name == "" || errors.Is(err, pbmErrors.ErrNotFound) {
		logf.FromContext(ctx).Info("Waiting for backup metadata", "pbmName", cr.Status.PBMname, "backup", cr.Name)
		return status, nil
	}

	if meta.StartTS > 0 {
		status.StartAt = &metav1.Time{
			Time: time.Unix(meta.StartTS, 0),
		}
	}

	switch meta.Status {
	case defs.StatusError:
		status.State = api.BackupStateError
		status.Error = fmt.Sprintf("%v", meta.Error())
	case defs.StatusDone:
		status.State = api.BackupStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(meta.LastTransitionTS, 0),
		}
	case defs.StatusStarting:
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

	node, err := b.pbm.Node(ctx)
	if err != nil {
		return status, nil
	}
	status.PBMPod = node

	meta, err = b.pbm.GetBackupMeta(ctx, cr.Status.PBMname)
	if err != nil || meta == nil || meta.Replsets == nil {
		return status, nil
	}

	status.PBMPods = backupPods(meta.Replsets)

	return status, nil
}

func backupPods(replsets []pbmBackup.BackupReplset) map[string]string {
	pods := make(map[string]string)
	for _, rs := range replsets {
		pods[rs.Name] = rs.Node
	}
	return pods
}

// Close closes the PBM connection
func (b *Backup) Close(ctx context.Context) error {
	if b.pbm == nil {
		return nil
	}
	return b.pbm.Close(ctx)
}
