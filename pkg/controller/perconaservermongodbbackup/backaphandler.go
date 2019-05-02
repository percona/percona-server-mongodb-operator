package perconaservermongodbbackup

import (
	"context"
	"errors"
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

	backup, err := b.getMetaData(cr.Name)
	if err != nil {
		return backupStatus, err
	}
	if len(backup.Metadata.Description) == 0 {
		return backupStatus, nil
	}
	backupStatus = b.getNewStatus(backup)

	return backupStatus, nil
}

func (b *BackupHandler) getMetaData(name string) (*pbapi.MetadataFile, error) {
	backup := &pbapi.MetadataFile{}
	stream, err := b.Client.BackupsMetadata(context.TODO(), &pbapi.BackupsMetadataParams{})
	if err != nil {
		return backup, err
	}
	defer stream.CloseSend()

	for {
		backup, err = stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return backup, err
		}
		if backup.Metadata.Description == name {
			return backup, nil
		}
	}
	return backup, nil
}

func (b *BackupHandler) getNewStatus(backup *pbapi.MetadataFile) psmdbv1alpha1.PerconaServerMongoDBBackupStatus {
	newStatus := psmdbv1alpha1.PerconaServerMongoDBBackupStatus{}

	newStatus.StartAt = &metav1.Time{
		Time: time.Unix(backup.Metadata.StartTs, 0),
	}
	if backup.Metadata.StartTs > 0 {
		newStatus.State = psmdbv1alpha1.StateRequested
	}
	if backup.Metadata.EndTs > 0 {
		newStatus.CompletedAt = &metav1.Time{
			Time: time.Unix(backup.Metadata.EndTs, 0),
		}
		newStatus.State = psmdbv1alpha1.StateReady
	}
	if len(backup.Metadata.StorageName) > 0 {
		newStatus.StorageName = backup.Metadata.StorageName
	}
	for k, v := range backup.Metadata.Replicasets {
		if len(v.DbBackupName) > 0 {
			jsonName := strings.Split(v.DbBackupName, "_"+k)
			newStatus.Destination = jsonName[0] + ".json"
			break
		}
	}

	return newStatus
}

// StartBackup is for starting new backup
func (b *BackupHandler) StartBackup(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) (psmdbv1alpha1.PerconaServerMongoDBBackupStatus, error) {
	backupStatus := psmdbv1alpha1.PerconaServerMongoDBBackupStatus{
		StorageName: cr.Spec.StorageName,
	}

	exists, err := b.isStorageExists(cr.Spec.StorageName)
	if err != nil {
		return backupStatus, fmt.Errorf("check storage: %v", err)
	}
	if !exists {
		return backupStatus, errors.New("storage is not availeble")
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

func (b *BackupHandler) isStorageExists(storageName string) (bool, error) {
	stream, err := b.Client.ListStorages(context.Background(), &pbapi.ListStoragesParams{})
	if err != nil {
		return false, fmt.Errorf("list storages: %v", err)
	}
	defer stream.CloseSend()

	for storage, err := stream.Recv(); err != io.EOF; {
		if err != nil {
			return false, fmt.Errorf("stream error: %v", err)
		}

		if storage.Name == storageName {
			return true, nil
		}
	}

	return false, nil
}
