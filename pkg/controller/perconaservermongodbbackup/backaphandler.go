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

const (
	statusRequested = "requested"
	statusRejected  = "regected"
	statusReady     = "ready"
)

// newBackup creates new Backup
func newBackupHandler(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) (BackupHandler, error) {
	if len(cr.Status.Name) == 0 {
		cr.Status = psmdbv1alpha1.PerconaServerMongoDBBackupStatus{
			Name:        cr.Name,
			StorageName: cr.Spec.StorageName,
			StartAt:     &metav1.Time{},
			CompletedAt: &metav1.Time{},
		}
	}
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

// CheckAndUpdateBackup is for check if backup exist and update CR status if true.
func (b *BackupHandler) CheckAndUpdateBackup(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) (bool, error) {
	stream, err := b.Client.BackupsMetadata(context.TODO(), &pbapi.BackupsMetadataParams{})
	if err != nil {
		return false, err
	}
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return false, err
		}
		if msg.Metadata.Description == cr.Name {
			cr.Status.StartAt = &metav1.Time{
				Time: time.Unix(msg.Metadata.StartTs, 0),
			}
			if msg.Metadata.StartTs > 0 {
				cr.Status.State = statusRequested
			}
			if msg.Metadata.EndTs > 0 {
				cr.Status.CompletedAt = &metav1.Time{
					Time: time.Unix(msg.Metadata.EndTs, 0),
				}
				cr.Status.State = statusReady
			}
			for k, v := range msg.Metadata.Replicasets {
				if len(v.DbBackupName) > 0 {
					jsonName := strings.Split(v.DbBackupName, "_"+k)
					cr.Status.Destination = jsonName[0] + ".json"
					break
				}
			}
			return true, nil
		}
	}

	return false, nil
}

// StartBackup is for starting new backup
func (b *BackupHandler) StartBackup(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) error {

	stream, err := b.Client.ListStorages(context.Background(), &pbapi.ListStoragesParams{})
	if err != nil {
		return fmt.Errorf("cannot list storages")
	}

	storages := []pbapi.StorageInfo{}
	for {
		msg, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
		storages = append(storages, *msg)
	}

	// Checking if the coordinator has availeble storage
	in := false
	for _, s := range storages {
		if s.Name == cr.Spec.StorageName {
			in = true
			break
		}
	}
	if !in {
		return fmt.Errorf("storage is not availeble")
	}

	msg := &pbapi.RunBackupParams{
		BackupType:      pbapi.BackupType_BACKUP_TYPE_LOGICAL,
		CompressionType: pbapi.CompressionType_COMPRESSION_TYPE_GZIP,
		Cypher:          pbapi.Cypher_CYPHER_NO_CYPHER,
		Description:     cr.Name,
		StorageName:     cr.Spec.StorageName,
	}
	resp, err := b.Client.RunBackup(context.Background(), msg)
	if err != nil {
		cr.Status.State = statusRejected
		return err
	}
	if resp != nil {
		if resp.Code > 0 {
			log.Info("Backup resp code:", resp.Code)
		}
		if len(resp.Message) > 0 {
			log.Info("Backup msg", resp.Message)
		}
	}
	cr.Status.State = statusRequested
	return nil
}
