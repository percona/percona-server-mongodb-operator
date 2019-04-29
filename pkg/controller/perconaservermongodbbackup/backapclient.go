package perconaservermongodbbackup

import (
	"context"
	"io"

	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"google.golang.org/grpc"
)

// newBackup creates new Backup
func newBackupHandler(crName, backupName, storageName string) (BackupHandler, error) {
	b := BackupHandler{}

	grpcOps := grpc.WithInsecure()
	conn, err := grpc.Dial(crName+backup.GetCoordinatorSuffix()+":10001", grpcOps)
	if err != nil {
		return b, err
	}
	client := pbapi.NewApiClient(conn)
	bData := BackupData{
		StorageName: storageName,
		Name:        backupName,
		Start:       0,
		End:         0,
		Type:        "",
		Status:      "",
	}
	b = BackupHandler{
		Client:     client,
		BackupData: bData,
	}
	return b, nil
}

// BackupHandler implements BC
type BackupHandler struct {
	Client     pbapi.ApiClient
	BackupData BackupData
}

// Back
type BackupData struct {
	Status      string
	Start       int64
	End         int64
	Type        string
	Name        string
	StorageName string
}

// BackupExist is check if backup exist and update it status if true
func (b *BackupHandler) BackupExist() (bool, BackupData, error) {
	stream, err := b.Client.BackupsMetadata(context.TODO(), &pbapi.BackupsMetadataParams{})
	if err != nil {
		return false, BackupData{}, err
	}

	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return false, BackupData{}, err
		}
		if msg.Metadata.Description == b.BackupData.Name {
			b.BackupData.Start = msg.Metadata.StartTs
			if msg.Metadata.EndTs > 0 {
				b.BackupData.Status = "ready"
			} else {
				b.BackupData.Status = "running"
			}
			return true, b.BackupData, nil

		}
	}
	return false, BackupData{}, nil
}

// StartBackup is for starting new backup
func (b *BackupHandler) StartBackup() error {
	msg := &pbapi.RunBackupParams{
		CompressionType: pbapi.CompressionType_COMPRESSION_TYPE_NO_COMPRESSION,
		Cypher:          pbapi.Cypher_CYPHER_NO_CYPHER,
		Description:     b.BackupData.Name,
		StorageName:     b.BackupData.StorageName,
	}
	_, err := b.Client.RunBackup(context.Background(), msg)
	if err != nil {
		return err
	}

	b.BackupData.Status = "running"
	b.BackupData.End = 0

	return nil
}
