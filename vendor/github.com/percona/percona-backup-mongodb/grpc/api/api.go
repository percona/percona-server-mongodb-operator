package api

import (
	"context"
	"os"
	"time"

	"github.com/percona/percona-backup-mongodb/grpc/server"
	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

type ApiServer struct {
	messagesServer *server.MessagesServer
	workDir        string
}

func NewApiServer(server *server.MessagesServer) *ApiServer {
	return &ApiServer{
		messagesServer: server,
	}
}

var (
	logger = logrus.New()
)

func init() {
	if os.Getenv("DEBUG") == "1" {
		logger.SetLevel(logrus.DebugLevel)
	}
}

func (a *ApiServer) GetClients(m *pbapi.Empty, stream pbapi.Api_GetClientsServer) error {
	for _, clientsByReplicasets := range a.messagesServer.ClientsByReplicaset() {
		for _, client := range clientsByReplicasets {
			status := client.Status()

			c := &pbapi.Client{
				Id:              client.ID,
				NodeType:        client.NodeType.String(),
				NodeName:        client.NodeName,
				ClusterId:       client.ClusterID,
				ReplicasetName:  client.ReplicasetName,
				ReplicasetId:    client.ReplicasetUUID,
				LastCommandSent: client.LastCommandSent,
				LastSeen:        client.LastSeen.Unix(),
				Status: &pbapi.ClientStatus{
					ReplicasetUuid:    client.ReplicasetUUID,
					ReplicasetName:    client.ReplicasetName,
					ReplicasetVersion: status.ReplicasetVersion,
					RunningDbBackup:   status.RunningDbBackup,
					Compression:       status.CompressionType.String(),
					Encrypted:         status.Cypher.String(),
					Filename:          status.DestinationName,
					BackupType:        status.BackupType.String(),
					StartOplogTs:      status.StartOplogTs,
					LastOplogTs:       status.LastOplogTs,
					LastError:         status.LastError,
					Finished:          status.BackupCompleted,
				},
			}
			stream.Send(c)
		}
	}
	return nil
}

func (a *ApiServer) BackupsMetadata(m *pbapi.BackupsMetadataParams, stream pbapi.Api_BackupsMetadataServer) error {
	bmd, err := a.messagesServer.ListBackups()
	if err != nil {
		return errors.Wrap(err, "cannot get backups metadata listing")
	}

	for name, md := range bmd {
		msg := &pbapi.MetadataFile{
			Filename: name,
			Metadata: &md,
		}
		stream.Send(msg)
	}

	return nil
}

// LastBackupMetadata returns the last backup metadata so it can be stored in the local filesystem as JSON
func (a *ApiServer) LastBackupMetadata(ctx context.Context, e *pbapi.LastBackupMetadataParams) (*pb.BackupMetadata, error) {
	return a.messagesServer.LastBackupMetadata().Metadata(), nil
}

// StartBackup starts a backup by calling server's StartBackup gRPC method
// This call waits until the backup finish
func (a *ApiServer) RunBackup(ctx context.Context, opts *pbapi.RunBackupParams) (*pbapi.Error, error) {
	msg := &pb.StartBackup{
		OplogStartTime:  time.Now().Unix(),
		BackupType:      pb.BackupType(opts.BackupType),
		CompressionType: pb.CompressionType(opts.CompressionType),
		Cypher:          pb.Cypher(opts.Cypher),
		NamePrefix:      time.Now().UTC().Format(time.RFC3339),
		Description:     opts.Description,
		StorageName:     opts.GetStorageName(),
		// DBBackupName & OplogBackupName are going to be set in server.go
		// We cannot set them here because the backup name will include the replicaset name so, it will
		// be different for each client/MongoDB instance
		// Here we are just using the same pb.StartBackup message to avoid declaring a new structure.
	}

	logger.Debug("Stopping the balancer")
	if err := a.messagesServer.StopBalancer(); err != nil {
		return &pbapi.Error{Message: err.Error()}, err
	}
	logger.Debug("Balancer stopped")

	logger.Debug("Starting the backup")
	if err := a.messagesServer.StartBackup(msg); err != nil {
		return &pbapi.Error{Message: err.Error()}, err
	}
	logger.Debug("Backup started")
	logger.Debug("Waiting for backup to finish")

	a.messagesServer.WaitBackupFinish()
	logger.Debug("Stopping oplog")
	err := a.messagesServer.StopOplogTail()
	if err != nil {
		logger.Fatalf("Cannot stop oplog tailer %s", err)
		return &pbapi.Error{Message: err.Error()}, err
	}
	logger.Debug("Waiting oplog to finish")
	a.messagesServer.WaitOplogBackupFinish()
	logger.Debug("Oplog finished")

	mdFilename := msg.NamePrefix + ".json"

	logger.Debugf("Writing metadata to %s", mdFilename)
	a.messagesServer.WriteBackupMetadata(mdFilename)

	logger.Debug("Starting the balancer")
	if err := a.messagesServer.StartBalancer(); err != nil {
		return &pbapi.Error{Message: err.Error()}, err
	}
	logger.Debug("Balancer started")
	return &pbapi.Error{}, nil
}

func (a *ApiServer) RunRestore(ctx context.Context, opts *pbapi.RunRestoreParams) (*pbapi.RunRestoreResponse, error) {
	err := a.messagesServer.RestoreBackupFromMetadataFile(opts.MetadataFile, opts.GetStorageName(), opts.SkipUsersAndRoles)
	if err != nil {
		return &pbapi.RunRestoreResponse{Error: err.Error()}, err
	}

	return &pbapi.RunRestoreResponse{}, nil
}

func (a *ApiServer) ListStorages(opts *pbapi.ListStoragesParams, stream pbapi.Api_ListStoragesServer) error {
	storages, err := a.messagesServer.ListStorages()
	for name, stg := range storages {
		msg := &pbapi.StorageInfo{
			Name:          name,
			MatchClients:  stg.MatchClients,
			DifferClients: stg.DifferClients,
			Info: &pb.StorageInfo{
				Valid:    stg.StorageInfo.Valid,
				CanRead:  stg.StorageInfo.CanRead,
				CanWrite: stg.StorageInfo.CanWrite,
				Name:     stg.StorageInfo.Name,
				Type:     stg.StorageInfo.Type,
				S3: &pb.S3{
					Region:      stg.StorageInfo.S3.Region,
					EndpointUrl: stg.StorageInfo.S3.EndpointUrl,
					Bucket:      stg.StorageInfo.S3.Bucket,
				},
				Filesystem: &pb.Filesystem{
					Path: stg.StorageInfo.Filesystem.Path,
				},
			},
		}
		stream.Send(msg)
	}
	return err
}
