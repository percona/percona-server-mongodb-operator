package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	psmdbv1alpha1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"google.golang.org/grpc"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newBackup creates new Backup
func newBackupHandler(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) (BackupHandler, error) {
	b := BackupHandler{}

	grpcOps := grpc.WithInsecure()
	conn, err := grpc.Dial(cr.Spec.PSMDBCluster+backup.GetCoordinatorSuffix()+":10001", grpcOps)
	if err != nil {
		return b, err
	}

	client := pbapi.NewApiClient(conn)

	b = BackupHandler{
		Client: client,
	}

	return b, nil
}

// BackupHandler is for working with backup coordinator
type BackupHandler struct {
	Client pbapi.ApiClient
}

// CheckBackup is for check if backup exist
func (b *BackupHandler) CheckBackup(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) (psmdbv1alpha1.PerconaServerMongoDBBackupStatus, error) {
	backupStatus := psmdbv1alpha1.PerconaServerMongoDBBackupStatus{}

	stream, err := b.Client.BackupsMetadata(context.TODO(), &pbapi.BackupsMetadataParams{})
	if err != nil {
		return backupStatus, err
	}
	defer stream.CloseSend()

	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return backupStatus, err
		}
		if msg.Metadata.Description == cr.Name {
			backupStatus.StartAt = &metav1.Time{
				Time: time.Unix(msg.Metadata.StartTs, 0),
			}
			if msg.Metadata.StartTs > 0 {
				backupStatus.State = psmdbv1alpha1.StateRequested
			}
			if msg.Metadata.EndTs > 0 {
				backupStatus.CompletedAt = &metav1.Time{
					Time: time.Unix(msg.Metadata.EndTs, 0),
				}
				backupStatus.State = psmdbv1alpha1.StateReady
			}
			if len(msg.Metadata.StorageName) > 0 {
				backupStatus.StorageName = msg.Metadata.StorageName
			}
			for k, v := range msg.Metadata.Replicasets {
				if len(v.DbBackupName) > 0 {
					jsonName := strings.Split(v.DbBackupName, "_"+k)
					backupStatus.Destination = jsonName[0] + ".json"
					break
				}
			}
			return backupStatus, nil
		}
	}

	return backupStatus, nil
}

// StartBackup is for starting new backup
func (b *BackupHandler) StartBackup(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) (psmdbv1alpha1.PerconaServerMongoDBBackupStatus, error) {
	backupStatus := psmdbv1alpha1.PerconaServerMongoDBBackupStatus{
		StorageName: cr.Spec.StorageName,
	}
	stream, err := b.Client.ListStorages(context.Background(), &pbapi.ListStoragesParams{})
	if err != nil {
		return backupStatus, fmt.Errorf("cannot list storages")
	}

	storages := []pbapi.StorageInfo{}
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return backupStatus, err
		}
		storages = append(storages, *msg)
	}
	err = stream.CloseSend()
	if err != nil {
		return backupStatus, fmt.Errorf("cannot close stream: %v", err)
	}

	// Checking if the storage exists
	in := false
	for _, s := range storages {
		if s.Name == cr.Spec.StorageName {
			in = true
			break
		}
	}
	if !in {
		return backupStatus, fmt.Errorf("storage is not availeble")
	}

	msg := &pbapi.RunBackupParams{
		BackupType:      pbapi.BackupType_BACKUP_TYPE_LOGICAL,
		CompressionType: pbapi.CompressionType_COMPRESSION_TYPE_GZIP,
		Cypher:          pbapi.Cypher_CYPHER_NO_CYPHER,
		Description:     cr.Name,
		StorageName:     cr.Spec.StorageName,
	}
	_, err = b.Client.RunBackup(context.Background(), msg)
	if err != nil {
		backupStatus.State = psmdbv1alpha1.StateRejected
		return backupStatus, err
	}

	backupStatus.State = psmdbv1alpha1.StateRequested
	return backupStatus, nil
}
