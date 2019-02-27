package server

import (
	"context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/percona/percona-backup-mongodb/internal/notify"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	EVENT_BACKUP_FINISH = iota
	EVENT_OPLOG_FINISH
	EVENT_RESTORE_FINISH
	logBufferSize = 500
)

type MessagesServer struct {
	stopChan chan struct{}
	lock     *sync.Mutex
	clients  map[string]*Client
	// Current backup status
	clientsRefreshInterval time.Duration
	replicasRunningBackup  map[string]bool // Key is ReplicasetUUID
	lastOplogTs            int64           // Timestamp in Unix format
	backupRunning          bool
	oplogBackupRunning     bool
	restoreRunning         bool
	err                    error
	//
	workDir               string
	clientLoggingEnabled  bool
	lastBackupMetadata    *BackupMetadata
	lastBackupErrors      []error
	clientDisconnetedChan chan string
	dbBackupFinishChan    chan interface{}
	oplogBackupFinishChan chan interface{}
	restoreFinishChan     chan interface{}
	clientsLogChan        chan *pb.LogEntry
	logger                *logrus.Logger
}

type StorageEntry struct {
	MatchClients  []string
	DifferClients []string
	StorageInfo   *pb.StorageInfo
}

type restoreSource struct {
	Client *Client
	Host   string
	Port   string
}

func NewMessagesServer(workDir string, clientsRefreshSecs int, logger *logrus.Logger) *MessagesServer {
	messagesServer := newMessagesServer(workDir, clientsRefreshSecs, logger)
	return messagesServer
}

func NewMessagesServerWithClientLogging(workDir string, clientsRefreshSecs int, logger *logrus.Logger) *MessagesServer {
	messagesServer := newMessagesServer(workDir, clientsRefreshSecs, logger)
	messagesServer.clientLoggingEnabled = true
	return messagesServer
}

func newMessagesServer(workDir string, clientsRefreshSecs int, logger *logrus.Logger) *MessagesServer {
	if logger == nil {
		logger = logrus.New()
		logger.SetLevel(logrus.StandardLogger().Level)
		logger.Out = logrus.StandardLogger().Out
	}

	bfc := notify.Start(EVENT_BACKUP_FINISH)
	ofc := notify.Start(EVENT_OPLOG_FINISH)
	rbf := notify.Start(EVENT_RESTORE_FINISH)
	if workDir == "" {
		workDir = "."
	}

	messagesServer := &MessagesServer{
		lock:                   &sync.Mutex{},
		clients:                make(map[string]*Client),
		clientDisconnetedChan:  make(chan string),
		clientsRefreshInterval: time.Duration(clientsRefreshSecs) * time.Second,
		stopChan:               make(chan struct{}),
		clientsLogChan:         make(chan *pb.LogEntry, logBufferSize),
		dbBackupFinishChan:     bfc,
		oplogBackupFinishChan:  ofc,
		restoreFinishChan:      rbf,
		replicasRunningBackup:  make(map[string]bool),
		workDir:                workDir,
		logger:                 logger,
	}
	//if clientsRefreshSecs > 0 {
	//	go messagesServer.refreshClientsScheduler()
	//}
	return messagesServer
}

func (s *MessagesServer) refreshClientsScheduler() {
	s.logger.Debugf("Starting clients background refresher with interval: %s", s.clientsRefreshInterval)
	ticker := time.NewTicker(s.clientsRefreshInterval)
	for {
		select {
		case <-s.stopChan:
			s.logger.Debug("Stopping clients background refresher")
			ticker.Stop()
			return
		case <-ticker.C:
			err := s.RefreshClients()
			if err != nil {
				s.logger.Errorf(err.Error())
			}
		}
	}
}

func (s *MessagesServer) BackupSourceNameByReplicaset() (map[string]string, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	sources := make(map[string]string)
	for _, client := range s.clients {
		if _, ok := sources[client.ReplicasetName]; !ok {
			if client.NodeType == pb.NodeType_NODE_TYPE_MONGOS {
				continue
			}
			backupSource, err := client.GetBackupSource()
			if err != nil {
				s.logger.Errorf("Cannot get backup source for client %s: %s", client.NodeName, err)
			}
			sources[client.ReplicasetName] = backupSource
		}
	}
	return sources, nil
}

func (s *MessagesServer) RestoreSourcesByReplicaset(bm *pb.BackupMetadata, storageName string) (map[string]restoreSource, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	sources := make(map[string]restoreSource)
	for replicasetName, replicasetMetaData := range bm.Replicasets {
		for _, client := range s.clients {
			if client.NodeType == pb.NodeType_NODE_TYPE_MONGOS {
				continue
			}
			if client.ReplicasetName != replicasetName {
				continue
			}
			resp, err := client.CanRestoreBackup(bm.BackupType, replicasetMetaData.DbBackupName, storageName)
			if err != nil {
				continue
			}
			_, ok := sources[resp.Replicaset]
			if !ok {
				sources[resp.Replicaset] = restoreSource{
					Client: s.getClientByID(resp.ClientId),
				}
			}
			if resp.IsPrimary {
				s, _ := sources[resp.Replicaset]
				s.Host = resp.Host
				s.Port = resp.Port
				sources[resp.Replicaset] = s
			}
		}
	}
	return sources, nil
}

func (s *MessagesServer) BackupSourceByReplicaset() (map[string]*Client, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	sources := make(map[string]*Client)
	for _, client := range s.clients {
		if _, ok := sources[client.ReplicasetName]; !ok {
			if client.NodeType == pb.NodeType_NODE_TYPE_MONGOS {
				continue
			}
			backupSource, err := client.GetBackupSource()
			if err != nil {
				s.logger.Errorf("Cannot get backup source for client %s: %s", client.NodeName, err)
			}
			if err != nil {
				return nil, fmt.Errorf("Cannot get best client for replicaset %q: %s", client.ReplicasetName, err)
			}
			bestClient := s.getClientByNodeName(backupSource)
			if bestClient == nil {
				return nil, fmt.Errorf("Cannot get the client connected to MongoDB %s", backupSource)
			}
			sources[client.ReplicasetName] = bestClient
		}
	}

	return sources, nil
}

func (s *MessagesServer) Clients() map[string]Client {
	s.lock.Lock()
	defer s.lock.Unlock()

	c := make(map[string]Client)
	for id, client := range s.clients {
		c[id] = *client
	}
	return c
}

func (s *MessagesServer) ClientsByReplicaset() map[string][]Client {
	replicas := make(map[string][]Client)
	for _, client := range s.clients {
		if _, ok := replicas[client.ReplicasetName]; !ok {
			replicas[client.ReplicasetName] = make([]Client, 0)
		}
		replicas[client.ReplicasetName] = append(replicas[client.ReplicasetName], *client)

	}
	return replicas
}

func (s *MessagesServer) LastOplogTs() int64 {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.lastOplogTs
}

func (s *MessagesServer) ListStorages() (map[string]StorageEntry, error) {
	stgs := make(map[string][]*pb.StorageInfo)
	s.lock.Lock()
	defer s.lock.Unlock()

	// Get all storages from all clients.
	// At the end of this loop, stgs is a map where the key is the client id and the value is an
	// array of all storages that client has defined.
	for id, c := range s.clients {
		ssInfo, err := c.GetStoragesInfo()
		if err != nil {
			return nil, errors.Wrapf(err, "Cannot get Storages Info on client %s", id)
		}
		stgs[id] = ssInfo
	}

	// Group storages by name and build two lists:
	// 1. Clients where the storages definitions matches
	// 2. Clients where the storages definitions are different
	// The lists are sorted only to make it easier to test
	ss := make(map[string]StorageEntry)
	for clientID, storages := range stgs {
		for _, info := range storages {
			stg, ok := ss[info.Name]
			if !ok {
				ss[info.Name] = StorageEntry{
					MatchClients:  []string{clientID},
					DifferClients: []string{},
					StorageInfo:   info,
				}
				continue
			}
			if reflect.DeepEqual(info, stg.StorageInfo) {
				stg.MatchClients = append(stg.MatchClients, clientID)
				sort.Strings(stg.MatchClients)
			} else {
				stg.DifferClients = append(stg.DifferClients, clientID)
				sort.Strings(stg.DifferClients)
			}
			ss[info.Name] = stg
		}
	}

	return ss, nil
}

func (s *MessagesServer) RefreshClients() error {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, client := range s.clients {
		if err := client.ping(); err != nil {
			return errors.Wrapf(err, "cannot refresh clients list while pinging client %s (%s)", client.ID, client.NodeName)
		}
	}
	return nil
}

// IsShardedSystem returns if a system is sharded.
// It check if the Node Type is:
// - Mongos
// - Config Server
// - Shard Server
// or if the ClusterID is not empty because in a sharded system, the cluster id
// is never empty.
func (s *MessagesServer) IsShardedSystem() bool {
	for _, client := range s.clients {
		if client.NodeType == pb.NodeType_NODE_TYPE_MONGOS ||
			client.NodeType == pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR ||
			client.NodeType == pb.NodeType_NODE_TYPE_MONGOD_SHARDSVR ||
			client.ClusterID != "" {
			return true
		}
	}
	return false
}

func (s *MessagesServer) LastBackupErrors() []error {
	return s.lastBackupErrors
}

func (s *MessagesServer) LastBackupMetadata() *BackupMetadata {
	return s.lastBackupMetadata
}

func (s *MessagesServer) ListBackups() (map[string]pb.BackupMetadata, error) {
	files, err := ioutil.ReadDir(s.workDir)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot list workdir %q backup filenames", s.workDir)
	}

	backups := make(map[string]pb.BackupMetadata)
	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") {
			continue
		}
		filename := filepath.Join(s.workDir, file.Name())
		bm, err := LoadMetadataFromFile(filename)
		if err != nil {
			return nil, fmt.Errorf("Invalid backup metadata file %s: %s", filename, err)
		}
		backups[file.Name()] = *bm.Metadata()
	}

	return backups, nil
}

func (s *MessagesServer) ReplicasetsRunningDBBackup() map[string]*Client {
	replicasets := make(map[string]*Client)

	for _, client := range s.clients {
		if client.isDBBackupRunning() {
			replicasets[client.ReplicasetName] = client
		}
	}
	return replicasets
}

func (s *MessagesServer) ReplicasetsRunningOplogBackup() map[string]*Client {
	replicasets := make(map[string]*Client)

	// use sync.Map?
	for _, client := range s.clients {
		if client.isOplogTailerRunning() {
			replicasets[client.ReplicasetName] = client
		}
	}

	return replicasets
}

func (s *MessagesServer) ReplicasetsRunningRestore() map[string]*Client {
	s.lock.Lock()
	defer s.lock.Unlock()

	replicasets := make(map[string]*Client)

	// use sync.Map?
	for _, client := range s.clients {
		if client.isRestoreRunning() {
			replicasets[client.ReplicasetName] = client
		}
	}

	return replicasets
}

// RestoreBackupFromMetadataFile is just a wrappwe around RestoreBackUp that receives a metadata filename
// loads and parse it and then call RestoreBackUp
func (s *MessagesServer) RestoreBackupFromMetadataFile(filename, storageName string, skipUsersAndRoles bool) error {
	filename = filepath.Join(s.workDir, filename)
	bm, err := LoadMetadataFromFile(filename)
	if err != nil {
		return fmt.Errorf("Invalid backup metadata file %s: %s", filename, err)
	}

	return s.RestoreBackUp(bm.Metadata(), storageName, skipUsersAndRoles)
}

// RestoreBackUp will run a restore on each client, using the provided backup metadata to choose the source for each
// replicaset.
func (s *MessagesServer) RestoreBackUp(bm *pb.BackupMetadata, storageName string, skipUsersAndRoles bool) error {
	clients, err := s.RestoreSourcesByReplicaset(bm, storageName)
	if err != nil {
		return errors.Wrapf(err, "Cannot start backup restore. Cannot find backup source for replicas")
	}

	if s.isBackupRunning() {
		return fmt.Errorf("Cannot start a restore while a backup is running")
	}
	if s.isRestoreRunning() {
		return fmt.Errorf("Cannot start a restore while another restore is still running")
	}

	s.reset()
	s.setRestoreRunning(true)

	// Ping will also update the status and if it is primary or secondary
	for _, source := range clients {
		source.Client.ping()
	}

	for replName, source := range clients {
		s.logger.Infof("Starting restore for replicaset %q on client %s %s %s", replName, source.Client.ID, source.Client.NodeName, source.Client.NodeType)
		s.replicasRunningBackup[replName] = true
		for bmReplName, metadata := range bm.Replicasets {
			if bmReplName == replName {
				msg := &pb.RestoreBackup{
					BackupType:        bm.BackupType,
					DbSourceName:      metadata.DbBackupName,
					OplogSourceName:   metadata.OplogBackupName,
					CompressionType:   bm.CompressionType,
					Cypher:            bm.Cypher,
					SkipUsersAndRoles: skipUsersAndRoles,
					Host:              source.Host,
					Port:              source.Port,
					StorageName:       storageName,
				}
				source.Client.restoreBackup(msg)
			}
		}
	}

	return nil
}

// TODO Create an API StartBackup message instead of using pb.StartBackup
// For example, we don't need DBBackupName & OplogBackupName and having them,
// here if we use pb.StartBackup message, leads to confusions
func (s *MessagesServer) StartBackup(opts *pb.StartBackup) error {
	if s.isBackupRunning() {
		return fmt.Errorf("Cannot start a backup while another backup is still running")
	}
	if s.isRestoreRunning() {
		return fmt.Errorf("Cannot start a backup while a restore is still running")
	}

	if err := s.ValidateReplicasetAgents(); err != nil {
		return errors.Wrap(err, "cannot start a backup while not all MongoDB instances have a backup agent")
	}

	ext := getFileExtension(pb.CompressionType(opts.CompressionType), pb.Cypher(opts.Cypher))

	s.lastBackupMetadata = NewBackupMetadata(opts)

	if err := s.RefreshClients(); err != nil {
		return errors.Wrapf(err, "cannot refresh clients state for backup")
	}

	clients, err := s.BackupSourceByReplicaset()
	if err != nil {
		return errors.Wrapf(err, "Cannot start backup. Cannot find backup source for replicas")
	}

	s.reset()
	s.setBackupRunning(true)
	s.setOplogBackupRunning(true)

	for replName, client := range clients {
		s.logger.Infof("Starting backup for replicaset %q on client %s %s %s", replName, client.ID, client.NodeName, client.NodeType)
		s.replicasRunningBackup[replName] = true

		dbBackupName := fmt.Sprintf("%s_%s.dump%s", opts.NamePrefix, client.ReplicasetName, ext)
		oplogBackupName := fmt.Sprintf("%s_%s.oplog%s", opts.NamePrefix, client.ReplicasetName, ext)

		s.lastBackupMetadata.AddReplicaset(client.ClusterID, client.ReplicasetName, client.ReplicasetUUID, dbBackupName, oplogBackupName)

		msg := &pb.StartBackup{
			BackupType:      opts.GetBackupType(),
			DbBackupName:    dbBackupName,
			OplogBackupName: oplogBackupName,
			CompressionType: opts.GetCompressionType(),
			Cypher:          opts.GetCypher(),
			OplogStartTime:  opts.GetOplogStartTime(),
			Description:     opts.Description,
			StorageName:     opts.GetStorageName(),
		}
		client.startBackup(msg)
	}

	return nil
}

// StartBalancer restarts the balancer if this is a sharded system
func (s *MessagesServer) StartBalancer() error {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, client := range s.clients {
		if client.NodeType == pb.NodeType_NODE_TYPE_MONGOS {
			return client.startBalancer()
		}
	}
	// This is not a sharded system. There is nothing to do.
	return nil
}

func (s *MessagesServer) Stop() {
	close(s.stopChan)
}

// StopBalancer stops the balancer if this is a sharded system
func (s *MessagesServer) StopBalancer() error {
	s.lock.Lock()
	defer s.lock.Unlock()
	for _, client := range s.clients {
		if client.NodeType == pb.NodeType_NODE_TYPE_MONGOS {
			return client.stopBalancer()
		}
	}
	// This is not a sharded system. There is nothing to do.
	return nil
}

// StopOplogTail calls every agent StopOplogTail(ts) method using the last oplog timestamp reported by the clients
// when they call DBBackupFinished after mongodump finish on each client. That way s.lastOplogTs has the last
// timestamp of the slowest backup
func (s *MessagesServer) StopOplogTail() error {
	if !s.isOplogBackupRunning() {
		return fmt.Errorf("Backup is not running")
	}
	// This should never happen. We get the last oplog timestamp when agents call DBBackupFinished
	if s.lastOplogTs == 0 {
		s.lastOplogTs = time.Now().Unix()
		s.logger.Errorf("Trying to stop the oplog tailer but last oplog timestamp is 0. Using current timestamp")
	}
	s.logger.Infof("StopOplogTs: %d (%v)", s.lastOplogTs, time.Unix(s.lastOplogTs, 0).Format(time.RFC3339))

	var gErr error
	for _, client := range s.clients {
		s.logger.Debugf("Checking if client %s is running the oplog backup: %v", client.NodeName, client.isOplogTailerRunning())
		if client.isOplogTailerRunning() {
			s.logger.Debugf("Stopping oplog tail in client %s at %s", client.NodeName, time.Unix(s.lastOplogTs, 0).Format(time.RFC3339))
			err := client.stopOplogTail(s.lastOplogTs)
			if err != nil {
				gErr = errors.Wrapf(gErr, "client: %s, error: %s", client.NodeName, err)
			}
		}
	}
	s.setBackupRunning(false)

	if gErr != nil {
		return errors.Wrap(gErr, "cannot stop oplog tailer")
	}
	return nil
}

func (s *MessagesServer) WaitBackupFinish() {
	replicasets := s.ReplicasetsRunningDBBackup()
	if len(replicasets) == 0 {
		return
	}
	<-s.dbBackupFinishChan
}

func (s *MessagesServer) WaitOplogBackupFinish() {
	replicasets := s.ReplicasetsRunningOplogBackup()
	if len(replicasets) == 0 {
		return
	}
	<-s.oplogBackupFinishChan
}

func (s *MessagesServer) WaitRestoreFinish() {
	replicasets := s.ReplicasetsRunningRestore()
	if len(replicasets) == 0 {
		return
	}
	<-s.restoreFinishChan
}

func (s *MessagesServer) WriteBackupMetadata(filename string) error {
	return s.lastBackupMetadata.WriteMetadataToFile(filepath.Join(s.workDir, filename))
}

// WorkDir returns the server working directory.
func (s *MessagesServer) WorkDir() string {
	return s.workDir
}

// ---------------------------------------------------------------------------------------------------------------------
//                                                              gRPC methods
// ---------------------------------------------------------------------------------------------------------------------

// DBBackupFinished process backup finished message from clients.
// After the mongodump call finishes, clients should call this method to inform the event to the server
func (s *MessagesServer) DBBackupFinished(ctx context.Context, msg *pb.DBBackupFinishStatus) (*pb.DBBackupFinishedAck, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	client := s.getClientByID(msg.GetClientId())
	if client == nil {
		return nil, fmt.Errorf("Unknown client ID: %s", msg.GetClientId())
	}
	client.setDBBackupRunning(false)

	if !msg.GetOk() {
		s.lastBackupErrors = append(s.lastBackupErrors, errors.New(msg.GetError()))
	}

	replicasets := s.ReplicasetsRunningDBBackup()

	if len(replicasets) == 0 {
		notify.Post(EVENT_BACKUP_FINISH, time.Now())
	}

	// Most probably, we are running the backup from a secondary, but we need the last oplog timestamp
	// from the primary in order to have a consistent backup.
	primaryClient, err := s.getPrimaryClient(client.ReplicasetName)
	if err != nil {
		return &pb.DBBackupFinishedAck{}, errors.Wrap(err, "DBBackupFinished")
	}
	lastOplogTs, err := primaryClient.getPrimaryLastOplogTs()
	if err != nil {
		return &pb.DBBackupFinishedAck{}, errors.Wrap(err, "cannot get primary's last oplog timestamp")
	}
	// Keep the last (bigger) oplog timestamp from all clients running the backup.
	// When all clients finish the backup, we will call CloseAt(s.lastOplogTs) on all clients to have a consistent
	// stop time for all oplogs.
	if lastOplogTs > s.lastOplogTs {
		s.lastOplogTs = lastOplogTs
	}
	return &pb.DBBackupFinishedAck{}, nil
}

func (s *MessagesServer) Logging(stream pb.Messages_LoggingServer) error {
	for {
		msg, err := stream.Recv()
		if err != nil {
			return err
		}

		level := logrus.Level(msg.GetLevel())
		msgText := strings.TrimSpace(msg.GetMessage())

		logLine := fmt.Sprintf("-> Client: %s, %s", msg.GetClientId(), msgText)
		switch level {
		case logrus.PanicLevel:
			s.logger.Panic(logLine)
		case logrus.FatalLevel:
			s.logger.Fatal(logLine)
		case logrus.ErrorLevel:
			s.logger.Error(logLine)
		case logrus.WarnLevel:
			s.logger.Warn(logLine)
		case logrus.InfoLevel:
			s.logger.Info(logLine)
		case logrus.DebugLevel:
			s.logger.Debug(logLine)
		}
	}
}

// MessagesChat is the method exposed by gRPC to stream messages between the server and agents
func (s *MessagesServer) MessagesChat(stream pb.Messages_MessagesChatServer) error {
	// This first message should be a RegisterMsg
	msg, err := stream.Recv()
	if err != nil {
		return err
	}

	clientID := msg.GetClientId()
	s.logger.Debugf("Registering new client: %s", clientID)
	if err := s.registerClient(stream, msg); err != nil {
		s.logger.Errorf("Cannot register client: %s", err)
		r := &pb.ServerMessage{
			Payload: &pb.ServerMessage_ErrorMsg{
				ErrorMsg: &pb.Error{
					Code:    pb.ErrorType_ERROR_TYPE_CLIENT_ALREADY_REGISTERED,
					Message: "",
				},
			},
		}
		err = stream.Send(r)
		if err != nil {
			s.logger.Errorf("Cannot send to client stream %s: %v", clientID, err)
		}

		return ClientAlreadyExistsError
	}

	r := &pb.ServerMessage{
		Payload: &pb.ServerMessage_AckMsg{AckMsg: &pb.Ack{}},
	}

	if err := stream.Send(r); err != nil {
		return err
	}

	// Keep the stream open
	<-stream.Context().Done()
	if err := s.unregisterClient(clientID); err != nil {
		s.logger.Errorf("Client %s stream was closed but cannot unregister client: %s", clientID, err)
	}

	return nil
}

// OplogBackupFinished process oplog tailer finished message from clients.
// After the the oplog tailer has been closed on clients, clients should call this method to inform the event to the server
func (s *MessagesServer) OplogBackupFinished(ctx context.Context, msg *pb.OplogBackupFinishStatus) (*pb.OplogBackupFinishedAck, error) {
	s.lock.Lock()
	defer s.lock.Unlock()
	client := s.getClientByID(msg.GetClientId())
	if client == nil {
		return nil, fmt.Errorf("Unknown client ID: %s", msg.GetClientId())
	}
	client.setOplogTailerRunning(false)

	replicasets := s.ReplicasetsRunningOplogBackup()
	if len(replicasets) == 0 {
		notify.Post(EVENT_OPLOG_FINISH, time.Now())
	}
	return &pb.OplogBackupFinishedAck{}, nil
}

// RestoreCompleted handles a replicaset restore completed messages from clients.
// After restore is completed or upon errors, each client running the restore will cann this gRPC method
// to inform the server about the restore status.
func (s *MessagesServer) RestoreCompleted(ctx context.Context, msg *pb.RestoreComplete) (*pb.RestoreCompletedAck, error) {
	//s.lock.Lock()
	//defer s.lock.Unlock()
	client := s.getClientByID(msg.GetClientId())
	if client == nil {
		return nil, fmt.Errorf("Unknown client ID: %s", msg.GetClientId())
	}
	s.logger.Debugf("Received RestoreCompleted from client %v", msg.GetClientId())
	client.setRestoreRunning(false)

	replicasets := s.ReplicasetsRunningRestore()
	if len(replicasets) == 0 {
		s.setRestoreRunning(false)
		notify.Post(EVENT_RESTORE_FINISH, time.Now())
	}
	return &pb.RestoreCompletedAck{}, nil
}

// ---------------------------------------------------------------------------------------------------------------------
//                                                        Internal helpers
// ---------------------------------------------------------------------------------------------------------------------

func (s *MessagesServer) cancelBackup() error {
	if !s.isOplogBackupRunning() {
		return fmt.Errorf("Backup is not running")
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	var gerr error
	for _, client := range s.clients {
		if err := client.stopBackup(); err != nil {
			gerr = errors.Wrapf(err, "cannot stop backup on %s", client.ID)
		}
	}
	return gerr
}

func (s *MessagesServer) getClientByID(id string) *Client {
	for _, client := range s.clients {
		if client.ID == id {
			return client
		}
	}
	return nil
}

func (s *MessagesServer) getPrimaryClient(replName string) (*Client, error) {
	for _, client := range s.clients {
		if client.ReplicasetName == replName && client.isPrimary {
			return client, nil
		}
	}
	return nil, fmt.Errorf("cannot find a primary in the clients list for replicaset %s", replName)
}

func (s *MessagesServer) getClientByNodeName(name string) *Client {
	for _, client := range s.clients {
		if client.NodeName == name {
			return client
		}
	}
	return nil
}

// validateReplicasetAgents will run getShardMap and parse the results on a config server.
// With that list, we can validate if we have at least one agent connected to each replicaset.
// Currently it is mandatory to have one agent ON EACH cluster member.
// Maybe in the future we can relax the requirements because having one agent per replicaset might
// be enough but it needs more testing.
func (s *MessagesServer) ValidateReplicasetAgents() error {
	var err error
	var repls []string

	s.lock.Lock()
	defer s.lock.Unlock()

	for _, client := range s.clients {
		if client.NodeType == pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR {
			repls, err = client.listReplicasets()
			if err != nil {
				return errors.Wrap(err, "cannot get repliscasets list using getShardMap")
			}
			break
		}
	}

	for _, repl := range repls {
		haveRepl := false
		for _, client := range s.clients {
			if client.ReplicasetName == repl {
				haveRepl = true
				break
			}
		}
		if !haveRepl {
			return fmt.Errorf("There are not agents for replicaset %q", repl)
		}
	}

	return nil
}

func getFileExtension(compressionType pb.CompressionType, cypher pb.Cypher) string {
	ext := ""

	switch cypher {
	case pb.Cypher_CYPHER_NO_CYPHER:
	}

	switch compressionType {
	case pb.CompressionType_COMPRESSION_TYPE_GZIP:
		ext = ext + ".gz"
	case pb.CompressionType_COMPRESSION_TYPE_LZ4:
		ext = ext + ".lz4"
	case pb.CompressionType_COMPRESSION_TYPE_SNAPPY:
		ext = ext + ".snappy"
	}

	return ext
}

func (s *MessagesServer) isBackupRunning() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.backupRunning
}

func (s *MessagesServer) isOplogBackupRunning() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.oplogBackupRunning
}

func (s *MessagesServer) isRestoreRunning() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	return s.restoreRunning
}

func (s *MessagesServer) registerClient(stream pb.Messages_MessagesChatServer, msg *pb.ClientMessage) error {
	s.lock.Lock()
	defer s.lock.Unlock()
	if msg.ClientId == "" {
		return fmt.Errorf("Invalid client ID (empty)")
	}

	if client, exists := s.clients[msg.ClientId]; exists {
		if err := client.ping(); err != nil {
			delete(s.clients, msg.ClientId)
		} else {
			return ClientAlreadyExistsError
		}
	}

	regMsg := msg.GetRegisterMsg()
	if regMsg == nil || regMsg.NodeType == pb.NodeType_NODE_TYPE_INVALID {
		return fmt.Errorf("Node type in register payload cannot be empty")
	}
	s.logger.Debugf("Register msg: %+v", regMsg)
	client := newClient(msg.ClientId, regMsg, stream, s.logger)
	s.clients[msg.ClientId] = client

	return nil
}

func (s *MessagesServer) reset() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.lastOplogTs = 0
	s.backupRunning = false
	s.oplogBackupRunning = false
	s.restoreRunning = false
	s.err = nil
	s.lastBackupErrors = []error{}
}

func (s *MessagesServer) setBackupRunning(status bool) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.backupRunning = status
}

func (s *MessagesServer) setOplogBackupRunning(status bool) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.oplogBackupRunning = status
}

func (s *MessagesServer) setRestoreRunning(status bool) {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.restoreRunning = status
}

func (s *MessagesServer) unregisterClient(id string) error {
	s.logger.Infof("Unregistering client %s", id)
	s.lock.Lock()
	defer s.lock.Unlock()

	if _, exists := s.clients[id]; !exists {
		return UnknownClientID
	}

	delete(s.clients, id)
	return nil
}
