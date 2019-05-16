package api

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/percona/percona-backup-mongodb/grpc/client"
	"github.com/percona/percona-backup-mongodb/grpc/server"
	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

// Apiserver has unexported fields and all the methods for API calls
type Server struct {
	messagesServer *server.MessagesServer
	logger         *logrus.Logger
}

// NewServer returns a new API Server
func NewServer(server *server.MessagesServer) *Server {
	s := &Server{
		messagesServer: server,
		logger:         logrus.New(),
	}
	if os.Getenv("DEBUG") == "1" {
		s.logger.SetLevel(logrus.DebugLevel)
	}

	return s
}

// GetClients streams back the list of connected clients
func (a *Server) GetClients(m *pbapi.Empty, stream pbapi.Api_GetClientsServer) error {
	// If there are a backup or restore running we should not send messages over the stream
	if !a.isBackupOrRestoreRunning() {
		if err := a.messagesServer.RefreshClients(); err != nil {
			return errors.Wrap(err, "cannot refresh clients list")
		}
	}

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
			if err := stream.Send(c); err != nil {
				return errors.Wrap(err, "cannot stream GetClients results")
			}
		}
	}
	return nil
}

// BackupMetadata streams back the last backup metadata
func (a *Server) BackupsMetadata(m *pbapi.BackupsMetadataParams, stream pbapi.Api_BackupsMetadataServer) error {
	bmd, err := a.messagesServer.ListBackups()
	if err != nil {
		return errors.Wrap(err, "cannot get backups metadata listing")
	}

	for name := range bmd {
		md := bmd[name]
		msg := &pbapi.MetadataFile{
			Filename: name,
			Metadata: &md,
		}
		if err := stream.Send(msg); err != nil {
			return errors.Wrap(err, "cannot send MetadataFile through the stream")
		}
	}

	return nil
}

// LastBackupMetadata returns the last backup metadata so it can be stored in the local filesystem as JSON
func (a *Server) LastBackupMetadata(ctx context.Context, e *pbapi.LastBackupMetadataParams) (
	*pb.BackupMetadata, error) {
	return a.messagesServer.LastBackupMetadata().Metadata(), nil
}

// StartBackup starts a backup by calling server's StartBackup gRPC method
// This call waits until the backup finish
func (a *Server) RunBackup(ctx context.Context, opts *pbapi.RunBackupParams) (*pbapi.RunBackupResponse, error) {
	var gerr error
	// response is an empty message because gRPC doesn't allow methods without a response message but we are
	// only interested in the error
	response := &pbapi.RunBackupResponse{}
	if a.isBackupOrRestoreRunning() {
		return response, fmt.Errorf("cannot start a new process while a backup or restore are running")
	}

	namePrefix := time.Now().UTC().Format(time.RFC3339)
	if opts.GetFilename() != "" {
		namePrefix = opts.GetFilename()
	}

	msg := &pb.StartBackup{
		OplogStartTime:  time.Now().Unix(),
		BackupType:      pb.BackupType(opts.BackupType),
		CompressionType: pb.CompressionType(opts.CompressionType),
		Cypher:          pb.Cypher(opts.Cypher),
		NamePrefix:      namePrefix,
		Description:     opts.Description,
		StorageName:     opts.GetStorageName(),
		// DBBackupName & OplogBackupName are going to be set in server.go
		// We cannot set them here because the backup name will include the replicaset name so, it will
		// be different for each client/MongoDB instance
		// Here we are just using the same pb.StartBackup message to avoid declaring a new structure.
	}

	a.logger.Info("Stopping the balancer")
	if err := a.messagesServer.StopBalancer(); err != nil {
		if !client.IsError(errors.Cause(err), client.NoMongosError) {
			return response, err
		}
	}

	defer func() {
		a.logger.Info("Starting the balancer")
		if err := a.messagesServer.StartBalancer(); err != nil {
			if !client.IsError(errors.Cause(err), client.NoMongosError) {
				gerr = multierror.Append(gerr, err)
			}
		}
	}()

	a.logger.Debug("Starting the backup")
	if err := a.messagesServer.StartBackup(msg); err != nil {
		return response, err
	}
	a.logger.Debug("Backup started")
	a.logger.Debug("Waiting for backup to finish")

	if err := a.messagesServer.WaitBackupFinish(); err != nil {
		gerr = multierror.Append(gerr, err)
	}
	a.logger.Info("Database dump completed")

	a.logger.Debug("Stopping oplog")
	if err := a.messagesServer.StopOplogTail(); err != nil {
		gerr = multierror.Append(gerr, fmt.Errorf("cannot stop oplog tailer %s", err))
		return response, gerr
	}

	a.logger.Debug("Waiting oplog to finish")
	if err := a.messagesServer.WaitOplogBackupFinish(); err != nil {
		gerr = multierror.Append(gerr, err)
	}
	a.logger.Info("Oplog finished")

	mdFilename := msg.NamePrefix + ".json"

	// This writes the backup metadata along with the backup files
	if err := a.messagesServer.WriteBackupMetadata(); err != nil {
		gerr = multierror.Append(gerr, fmt.Errorf("cannot write backup metadata: %s", err))
	}

	// Writes a copy of the backup metadata into the coordinator's working directory
	a.logger.Debugf("Writing metadata to %s", mdFilename)
	if err := a.messagesServer.WriteServerBackupMetadata(mdFilename); err != nil {
		gerr = multierror.Append(gerr, fmt.Errorf("cannot write backup metadata: %s", err))
	}

	return response, gerr
}

func (a *Server) RunRestore(ctx context.Context, opts *pbapi.RunRestoreParams) (*pbapi.RunRestoreResponse, error) {
	// response is an empty message because gRPC doesn't allow methods without a response message but we are
	// only interested in the error
	response := &pbapi.RunRestoreResponse{}
	if a.isBackupOrRestoreRunning() {
		return response, fmt.Errorf("cannot start a new process while a backup or restore are running")
	}

	err := a.messagesServer.RestoreBackupFromMetadataFile(opts.MetadataFile, opts.GetStorageName(), opts.SkipUsersAndRoles)
	if err != nil {
		return response, err
	}

	if err := a.messagesServer.WaitRestoreFinish(); err != nil {
		return response, err
	}

	return response, nil
}

func (a *Server) ListStorages(opts *pbapi.ListStoragesParams, stream pbapi.Api_ListStoragesServer) error {
	if a.isBackupOrRestoreRunning() {
		return fmt.Errorf("cannot start a new process while a backup or restore are running")
	}

	storages, err := a.messagesServer.ListStorages()
	if err != nil {
		return errors.Wrap(err, "cannot get storages from the server")
	}
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
		if err := stream.Send(msg); err != nil {
			return errors.Wrap(err, "cannot stream storage info msg for ListStorages")
		}
	}
	return nil
}

func (a *Server) isBackupOrRestoreRunning() bool {
	rsrb := a.messagesServer.ReplicasetsRunningDBBackup()
	if len(rsrb) > 0 {
		return true
	}

	rsrr := a.messagesServer.ReplicasetsRunningRestore()
	return len(rsrr) > 0
}
