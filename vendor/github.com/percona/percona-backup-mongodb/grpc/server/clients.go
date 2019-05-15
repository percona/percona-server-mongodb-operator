package server

import (
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

var (
	ClientAlreadyExistsError = fmt.Errorf("Client ID already registered")
	UnknownClientID          = fmt.Errorf("Unknown client ID")
	// Timeuot is exported because in tests we might want to change it
	Timeout = 30 * time.Second
)

type Client struct {
	ID        string      `json:"id"`
	NodeType  pb.NodeType `json:"node_type"`
	NodeName  string      `json:"node_name"`
	ClusterID string      `json:"cluster_id"`

	ReplicasetName      string `json:"replicaset_name"`
	ReplicasetUUID      string `json:"replicaset_uuid"`
	ReplicasetVersion   int32  `json:"replicaset_version"`
	isPrimary           bool
	isSecondary         bool
	isTailing           bool
	lastTailedTimestamp int64

	LastCommandSent string    `json:"last_command_sent"`
	LastSeen        time.Time `json:"last_seen"`
	logger          *logrus.Logger

	streamRecvChan chan *pb.ClientMessage

	streamLock *sync.Mutex
	stream     pb.Messages_MessagesChatServer
	statusLock *sync.Mutex
	status     pb.Status
}

func newClient(id string, registerMsg *pb.Register, stream pb.Messages_MessagesChatServer,
	logger *logrus.Logger) *Client {
	if logger == nil {
		logger = logrus.New()
		logger.SetLevel(logrus.StandardLogger().Level)
		logger.Out = logrus.StandardLogger().Out
	}

	client := &Client{
		ID:             id,
		ClusterID:      registerMsg.ClusterId,
		ReplicasetUUID: registerMsg.ReplicasetId,
		ReplicasetName: registerMsg.ReplicasetName,
		NodeType:       registerMsg.NodeType,
		NodeName:       registerMsg.NodeName,
		isPrimary:      registerMsg.IsPrimary,
		isSecondary:    registerMsg.IsSecondary,
		stream:         stream,
		LastSeen:       time.Now(),
		status: pb.Status{
			RunningDbBackup:    false,
			RunningOplogBackup: false,
			RestoreStatus:      pb.RestoreStatus_RESTORE_STATUS_NOT_RUNNING,
		},
		streamLock:     &sync.Mutex{},
		statusLock:     &sync.Mutex{},
		logger:         logger,
		streamRecvChan: make(chan *pb.ClientMessage),
	}

	go client.handleStreamRecv()

	return client
}

func (c *Client) CanRestoreBackup(backupType pb.BackupType, name, storageName string) (
	pb.CanRestoreBackupResponse, error) {
	if err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_CanRestoreBackupMsg{
			CanRestoreBackupMsg: &pb.CanRestoreBackup{
				BackupType:  backupType,
				BackupName:  name,
				StorageName: storageName,
			},
		},
	}); err != nil {
		return pb.CanRestoreBackupResponse{}, err
	}
	msg, err := c.streamRecv()
	if err != nil {
		return pb.CanRestoreBackupResponse{}, err
	}
	if canRestoreBackupMsg := msg.GetCanRestoreBackupMsg(); canRestoreBackupMsg != nil {
		return *canRestoreBackupMsg, nil
	}

	return pb.CanRestoreBackupResponse{}, fmt.Errorf("cannot get CanRestoreBackup Response (response is nil)")
}

func (c *Client) GetCmdLineOpts() (*pb.CmdLineOpts, error) {
	msg := &pb.ServerMessage{
		Payload: &pb.ServerMessage_GetCmdLineOpts{
			GetCmdLineOpts: &pb.GetCmdLineOpts{},
		},
	}
	if err := c.stream.SendMsg(msg); err != nil {
		return nil, errors.Wrap(err, "cannot get cmd line options")
	}

	response, err := c.streamRecv()
	if err != nil {
		return nil, err
	}

	switch response.Payload.(type) {
	case *pb.ClientMessage_ErrorMsg:
		return nil, fmt.Errorf("cannot list shards on client %s: %s", c.NodeName, response.GetErrorMsg())
	case *pb.ClientMessage_CmdLineOpts:
		return response.GetCmdLineOpts(), nil
	}

	return nil, fmt.Errorf("invalid response type for GetCmdLineOpts (%T)", response.Payload)
}

func (c *Client) GetBackupSource() (string, error) {
	if err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_BackupSourceMsg{BackupSourceMsg: &pb.GetBackupSource{}},
	}); err != nil {
		return "", err
	}
	msg, err := c.streamRecv()
	if err != nil {
		return "", err
	}
	if errMsg := msg.GetErrorMsg(); errMsg != nil {
		return "", fmt.Errorf("cannot get backup source for client %s: %s", c.NodeName, errMsg)
	}
	return msg.GetBackupSourceMsg().GetSourceClient(), nil
}

func (c *Client) GetMongoDBVersion() (string, error) {
	if err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_GetMongodbVersion{GetMongodbVersion: &pb.GetMongoDBVersion{}},
	}); err != nil {
		return "", err
	}
	msg, err := c.streamRecv()
	if err != nil {
		return "", err
	}
	if errMsg := msg.GetErrorMsg(); errMsg != nil {
		return "", fmt.Errorf("cannot get MongoDB version from client %s: %s", c.NodeName, errMsg)
	}
	return msg.GetMongodbVersion().GetVersion(), nil
}

func (c *Client) GetStoragesInfo() ([]*pb.StorageInfo, error) {
	if err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_ListStoragesMsg{ListStoragesMsg: &pb.ListStorages{}},
	}); err != nil {
		return nil, err
	}
	msg, err := c.streamRecv()
	if err != nil {
		return nil, err
	}
	if errMsg := msg.GetErrorMsg(); errMsg != nil {
		return nil, fmt.Errorf("cannot get storages info from client %s: %s", c.NodeName, errMsg)
	}
	return msg.GetStoragesInfo().GetStoragesInfo(), nil
}

func (c *Client) Status() pb.Status {
	c.statusLock.Lock()
	defer c.statusLock.Unlock()
	return c.status
}

func (c *Client) GetStatus() (pb.Status, error) {
	if err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_GetStatusMsg{GetStatusMsg: &pb.GetStatus{}},
	}); err != nil {
		return pb.Status{}, err
	}
	msg, err := c.streamRecv()
	if err != nil {
		return pb.Status{}, err
	}
	c.logger.Debugf("grpc/server/clients.go GetStatus recv message: %v\n", msg)
	statusMsg := msg.GetStatusMsg()
	c.logger.Debugf("grpc/server/clients.go GetStatus recv message (decoded): %v\n", statusMsg)
	if statusMsg == nil {
		return pb.Status{}, fmt.Errorf("received nil GetStatus message from client: %s", c.NodeName)
	}

	c.statusLock.Lock()
	c.status = *statusMsg
	c.statusLock.Unlock()
	return *statusMsg, nil
}

func (c *Client) handleStreamRecv() {
	for {
		msg, err := c.stream.Recv()
		if err != nil {
			return
		}
		c.streamRecvChan <- msg
	}
}

func (c *Client) isDBBackupRunning() bool {
	c.statusLock.Lock()
	defer c.statusLock.Unlock()
	return c.status.RunningDbBackup
}

func (c *Client) isOplogTailerRunning() (status bool) {
	c.statusLock.Lock()
	defer c.statusLock.Unlock()

	return c.status.RunningOplogBackup
}

func (c *Client) isRestoreRunning() (status bool) {
	c.statusLock.Lock()
	defer c.statusLock.Unlock()

	return c.status.RestoreStatus > pb.RestoreStatus_RESTORE_STATUS_NOT_RUNNING
}

/*
  This function should be called only on primaries after receiving the backup completed message.
  We need to know the last oplog timestamp from the primary in order to tell the oplog tailer when
  to stop to have a consistent backup across all the shards.
*/
func (c *Client) getPrimaryLastOplogTs() (int64, error) {
	err := c.streamSend(&pb.ServerMessage{Payload: &pb.ServerMessage_LastOplogTs{LastOplogTs: &pb.GetLastOplogTs{}}})
	if err != nil {
		return 0, errors.Wrapf(err, "cannot send LastOplogTs request to the primary node %s", c.NodeName)
	}
	response, err := c.streamRecv()
	if err != nil {
		return 0, errors.Wrapf(err, "cannot get LastOplogTs from the primary node %s", c.NodeName)
	}

	switch response.Payload.(type) {
	case *pb.ClientMessage_ErrorMsg:
		return 0, fmt.Errorf("cannot list shards on client %s: %s", c.NodeName, response.GetErrorMsg())
	case *pb.ClientMessage_LastOplogTs:
		return response.GetLastOplogTs().LastOplogTs, nil
	}
	return 0, fmt.Errorf("unknown response type for list Shards message: %T, %+v", response.Payload, response.Payload)
}

// listReplicasets will trigger processGetReplicasets on clients connected to a MongoDB instance.
// It makes sense to call it only on config servers since it runs "getShardMap" on MongoDB and that
// only return valid values on config servers.
// By parsing the results of getShardMap, we can know which replicasets are running in the cluster
// and we will use that list to validate we have agents connected to all replicasets, otherwise,
// a backup would be incomplete.
func (c *Client) listReplicasets() ([]string, error) {
	err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_ListReplicasets{
			ListReplicasets: &pb.ListReplicasets{},
		},
	})
	if err != nil {
		return nil, err
	}

	response, err := c.streamRecv()
	if err != nil {
		return nil, err
	}

	switch response.Payload.(type) {
	case *pb.ClientMessage_ErrorMsg:
		return nil, fmt.Errorf("cannot list shards on client %s: %s", c.NodeName, response.GetErrorMsg())
	case *pb.ClientMessage_ReplicasetsMsg:
		shards := response.GetReplicasetsMsg()
		return shards.Replicasets, nil
	}
	return nil, fmt.Errorf("unknown response type for list Shards message: %T, %+v", response.Payload, response.Payload)
}

func (c *Client) ping() error {
	c.logger.Debug("sending ping")
	err := c.streamSend(&pb.ServerMessage{Payload: &pb.ServerMessage_PingMsg{PingMsg: &pb.Ping{}}})
	if err != nil {
		return errors.Wrap(err, "clients.go -> ping()")
	}

	msg, err := c.streamRecv()
	if err != nil {
		return errors.Wrapf(err, "ping client %s (%s)", c.ID, c.NodeName)
	}
	pongMsg := msg.GetPongMsg()
	c.statusLock.Lock()
	c.LastSeen = time.Now()
	c.NodeType = pongMsg.GetNodeType()
	c.ReplicasetUUID = pongMsg.GetReplicaSetUuid()
	c.ReplicasetVersion = pongMsg.GetReplicaSetVersion()
	c.isTailing = pongMsg.GetIsTailing()
	c.isPrimary = pongMsg.GetIsPrimary()
	c.isSecondary = pongMsg.GetIsSecondary()
	c.lastTailedTimestamp = pongMsg.GetLastTailedTimestamp()
	c.statusLock.Unlock()

	return nil
}

func (c *Client) restoreBackup(msg *pb.RestoreBackup) error {
	outMsg := &pb.ServerMessage{
		Payload: &pb.ServerMessage_RestoreBackupMsg{
			RestoreBackupMsg: &pb.RestoreBackup{
				BackupType:        msg.BackupType,
				SourceBucket:      msg.SourceBucket,
				DbSourceName:      msg.DbSourceName,
				OplogSourceName:   msg.OplogSourceName,
				CompressionType:   msg.CompressionType,
				Cypher:            msg.Cypher,
				OplogStartTime:    msg.OplogStartTime,
				SkipUsersAndRoles: msg.SkipUsersAndRoles,
				Host:              msg.Host,
				Port:              msg.Port,
				StorageName:       msg.GetStorageName(),
				MongodbVersion:    msg.MongodbVersion,
			},
		},
	}
	if err := c.streamSend(outMsg); err != nil {
		return err
	}

	response, err := c.streamRecv()
	if err != nil {
		return err
	}
	switch response.Payload.(type) {
	case *pb.ClientMessage_ErrorMsg:
		return fmt.Errorf("cannot start restore on client %s: %s", c.NodeName, response.GetErrorMsg())
	case *pb.ClientMessage_AckMsg:
		c.setRestoreRunning(true)
		return nil
	}
	return fmt.Errorf("unknown response type for Restore message: %T, %+v", response.Payload, response.Payload)
}

func (c *Client) setDBBackupRunning(status bool) {
	c.statusLock.Lock()
	c.status.RunningDbBackup = status
	c.statusLock.Unlock()
}

func (c *Client) setOplogTailerRunning(status bool) {
	c.statusLock.Lock()
	c.status.RunningOplogBackup = status
	c.statusLock.Unlock()
}

func (c *Client) setRestoreRunning(status bool) {
	c.statusLock.Lock()
	defer c.statusLock.Unlock()
	if status {
		c.status.RestoreStatus = pb.RestoreStatus_RESTORE_STATUS_RESTORINGDB
		return
	}
	c.status.RestoreStatus = pb.RestoreStatus_RESTORE_STATUS_NOT_RUNNING
}

func (c *Client) startBackup(opts *pb.StartBackup) error {
	msg := &pb.ServerMessage{
		Payload: &pb.ServerMessage_StartBackupMsg{
			StartBackupMsg: &pb.StartBackup{
				BackupType:      opts.BackupType,
				DbBackupName:    opts.DbBackupName,
				OplogBackupName: opts.OplogBackupName,
				CompressionType: opts.CompressionType,
				Cypher:          opts.Cypher,
				OplogStartTime:  opts.OplogStartTime,
				StorageName:     opts.GetStorageName(),
			},
		},
	}
	if err := c.streamSend(msg); err != nil {
		return err
	}
	response, err := c.streamRecv()
	if err != nil {
		return err
	}
	if _, ok := response.Payload.(*pb.ClientMessage_AckMsg); ok {
		c.setDBBackupRunning(true)
		c.setOplogTailerRunning(true)
		return nil
	}
	return fmt.Errorf("invalid client response to start backup message. Want 'ack', got %T", response.Payload)
}

func (c *Client) startBalancer() error {
	err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_StartBalancerMsg{StartBalancerMsg: &pb.StartBalancer{}},
	})
	if err != nil {
		return err
	}
	msg, err := c.streamRecv()
	if err != nil {
		return err
	}

	switch msg.Payload.(type) {
	case *pb.ClientMessage_ErrorMsg:
		return fmt.Errorf("%s", msg.GetErrorMsg().Message)
	case *pb.ClientMessage_AckMsg:
		return nil
	}
	return fmt.Errorf("start balancer unknown respose type. Want ACK, got %T", msg)
}

func (c *Client) stopBackup() error {
	msg := &pb.ServerMessage{
		Payload: &pb.ServerMessage_CancelBackupMsg{},
	}
	if err := c.streamSend(msg); err != nil {
		return err
	}
	if msg, err := c.streamRecv(); err != nil {
		return err
	} else if ack := msg.GetAckMsg(); ack == nil {
		return fmt.Errorf("invalid client response to stop backup message. Want 'ack', got %T", msg)
	}
	return nil
}

func (c *Client) stopBalancer() error {
	err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_StopBalancerMsg{StopBalancerMsg: &pb.StopBalancer{}},
	})
	if err != nil {
		return err
	}
	msg, err := c.streamRecv()
	if err != nil {
		return err
	}

	switch msg.Payload.(type) {
	case *pb.ClientMessage_AckMsg:
		return nil
	case *pb.ClientMessage_ErrorMsg:
		errMsg := msg.GetErrorMsg()
		return fmt.Errorf("%s", errMsg.Message)
	}
	return fmt.Errorf("stop balancer unknown respose type. Want ACK, got %T", msg)
}

func (c *Client) stopOplogTail(ts int64) error {
	c.logger.Debugf("Stopping oplog tail for client: %s, at %d", c.NodeName, ts)
	err := c.streamSend(&pb.ServerMessage{
		Payload: &pb.ServerMessage_StopOplogTailMsg{StopOplogTailMsg: &pb.StopOplogTail{Ts: ts}},
	})
	if err != nil {
		c.logger.Errorf("Error in client.StopOplogTail stream.Send(...): %s", err)
	}

	if msg, err := c.streamRecv(); err != nil {
		return err
	} else if ack := msg.GetAckMsg(); ack == nil {
		return fmt.Errorf("invalid client response to start backup message. Want 'ack', got %T", msg)
	}
	return nil
}

func (c *Client) writeBackupMetadata(fileName, storageName string, data []byte) error {
	msg := &pb.ServerMessage{
		Payload: &pb.ServerMessage_WriteFile{
			WriteFile: &pb.WriteFile{
				StorageName: storageName,
				FileName:    fileName,
				Data:        data,
			},
		},
	}
	if err := c.streamSend(msg); err != nil {
		return err
	}
	response, err := c.streamRecv()
	if err != nil {
		return err
	}
	if errMsg := response.GetErrorMsg(); errMsg != nil {
		return fmt.Errorf(errMsg.GetMessage())
	}

	resp, ok := response.Payload.(*pb.ClientMessage_WriteStatus)
	if !ok {
		return fmt.Errorf("invalid client response to writeBackupMetadata. Want 'WriteStatus', got %T", response.Payload)
	}
	if resp.WriteStatus.GetError() != "" {
		return fmt.Errorf(resp.WriteStatus.GetError())
	}
	return nil
}

func (c *Client) streamRecv() (*pb.ClientMessage, error) {
	select {
	case msg := <-c.streamRecvChan:
		return msg, nil
	case <-time.After(Timeout):
		return nil, fmt.Errorf("Timeout reading from the stream: \n" + string(debug.Stack()))
	}
}

func (c *Client) streamSend(msg *pb.ServerMessage) error {
	c.streamLock.Lock()
	defer c.streamLock.Unlock()
	return c.stream.Send(msg)
}
