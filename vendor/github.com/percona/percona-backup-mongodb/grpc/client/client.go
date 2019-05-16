package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/hashicorp/go-multierror"
	"github.com/hashicorp/go-version"
	"github.com/percona/percona-backup-mongodb/bsonfile"
	"github.com/percona/percona-backup-mongodb/internal/awsutils"
	"github.com/percona/percona-backup-mongodb/internal/backup/dumper"
	"github.com/percona/percona-backup-mongodb/internal/cluster"
	"github.com/percona/percona-backup-mongodb/internal/oplog"
	"github.com/percona/percona-backup-mongodb/internal/reader"
	"github.com/percona/percona-backup-mongodb/internal/restore"
	"github.com/percona/percona-backup-mongodb/internal/utils"
	"github.com/percona/percona-backup-mongodb/internal/writer"
	"github.com/percona/percona-backup-mongodb/mdbstructs"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/percona/percona-backup-mongodb/storage"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

const (
	NoMongosError = iota
)

type Client struct {
	id         string
	ctx        context.Context
	cancelFunc context.CancelFunc

	replicasetName string
	replicasetID   string
	nodeName       string
	clusterID      string
	backupDir      string
	nodeType       pb.NodeType
	running        bool

	mdbSession  *mgo.Session
	mgoDI       *mgo.DialInfo
	connOpts    ConnectionOptions
	sslOpts     SSLOptions
	isMasterDoc *mdbstructs.IsMaster

	storages        *storage.Storages
	mongoDumper     *dumper.Mongodump
	oplogTailer     *oplog.OplogTail
	logger          *logrus.Logger
	grpcClientConn  *grpc.ClientConn
	grpcClient      pb.MessagesClient
	dbReconnectChan chan struct{}
	//
	status pb.Status
	lock   *sync.Mutex

	streamLock *sync.Mutex
	stream     pb.Messages_MessagesChatClient
}

type ConnectionOptions struct {
	Host                string `yaml:"host,omitempty" kingpin:"mongodb-host"`
	Port                string `yaml:"port,omitempty" kingpin:"mongodb-port"`
	User                string `yaml:"user,omitempty" kingpin:"mongodb-user"`
	Password            string `yaml:"password,omitempty" kingpin:"mongodb-password"`
	AuthDB              string `yaml:"authdb,omitempty" kingpin:"mongodb-authdb"`
	ReplicasetName      string `yaml:"replicaset_name,omitempty" kingpin:"mongodb-replicaset"`
	Timeout             int    `yaml:"timeout,omitempty"`
	TCPKeepAliveSeconds int    `yaml:"tcp_keep_alive_seconds,omitempty"`
	ReconnectDelay      int    `yaml:"reconnect_delay,omitempty" kingpin:"mongodb-reconnect-delay"`
	ReconnectCount      int    `yaml:"reconnect_count,omitempty" kingpin:"mongodb-reconnect-count"` // 0: forever
}

// Struct holding ssl-related options
type SSLOptions struct {
	SSLCAFile           string `yaml:"sslca_file,omitempty"`
	SSLPEMKeyFile       string `yaml:"sslpem_key_file,omitempty"`
	SSLPEMKeyPassword   string `yaml:"sslpem_key_password,omitempty"`
	SSLCRLFile          string `yaml:"sslcrl_file,omitempty"`
	UseSSL              bool   `yaml:"use_ssl,omitempty"`
	SSLAllowInvalidCert bool   `yaml:"ssl_allow_invalid_cert,omitempty"`
	SSLAllowInvalidHost bool   `yaml:"ssl_allow_invalid_host,omitempty"`
	SSLFipsMode         bool   `yaml:"ssl_fips_mode,omitempty"`
}

type InputOptions struct {
	BackupDir     string
	DbConnOptions ConnectionOptions
	DbSSLOptions  SSLOptions
	GrpcConn      *grpc.ClientConn
	Logger        *logrus.Logger
	Storages      *storage.Storages
}

type shardsMap struct {
	Map map[string]string `bson:"map"`
	OK  int               `bson:"ok"`
}

type Error struct {
	code int
	err  error
}

func (ce *Error) Error() string {
	return ce.err.Error()
}

func (ce *Error) Cause() error {
	return ce.err
}

func Errorf(code int, args ...string) *Error {
	ce := &Error{
		code: code,
	}

	// if args are not provided this will panic. it is intentional
	if len(args) > 1 {
		ce.err = fmt.Errorf(args[0], args[1:])
	} else {
		ce.err = fmt.Errorf(args[0])
	}

	return ce
}

func IsError(err error, code int) bool {
	ce, ok := err.(*Error)
	if !ok {
		return false
	}
	return ce.code == code
}

const (
	balancerStopRetries = 3
	balancerStopTimeout = 30 * time.Second
	dbReconnectInterval = 30 * time.Second
	dbPingInterval      = 60 * time.Second
	isdbgrid            = "isdbgrid"
	typeFilesystem      = "filesystem"
	typeS3              = "s3"
)

func NewClient(inctx context.Context, in InputOptions) (*Client, error) {
	// If the provided logger is nil, create a new logger and copy Logrus standard logger level and writer
	if in.Logger == nil {
		in.Logger = logrus.New()
		in.Logger.SetLevel(logrus.StandardLogger().Level)
		in.Logger.Out = logrus.StandardLogger().Out
	}

	ctx, cancel := context.WithCancel(inctx)

	di := &mgo.DialInfo{
		Addrs:          []string{in.DbConnOptions.Host + ":" + in.DbConnOptions.Port},
		Username:       in.DbConnOptions.User,
		Password:       in.DbConnOptions.Password,
		Source:         in.DbConnOptions.AuthDB,
		ReplicaSetName: in.DbConnOptions.ReplicasetName,
		AppName:        "percona-backup-mongodb",
		FailFast:       true,
		Direct:         true,
	}

	c := &Client{
		ctx:            ctx,
		cancelFunc:     cancel,
		backupDir:      in.BackupDir,
		grpcClientConn: in.GrpcConn,
		status: pb.Status{
			BackupType: pb.BackupType_BACKUP_TYPE_LOGICAL,
		},
		connOpts:        in.DbConnOptions,
		sslOpts:         in.DbSSLOptions,
		logger:          in.Logger,
		lock:            &sync.Mutex{},
		running:         true,
		mgoDI:           di,
		dbReconnectChan: make(chan struct{}),
		// This lock is used to sync the access to the stream Send() method.
		// For example, if the backup is running, we can receive a Ping request from
		// the server but while we are sending the Ping response, the backup can finish
		// or fail and in that case it will try to send a message to the server to inform
		// the event at the same moment we are sending the Ping response.
		// Since the access to the stream is not thread safe, we need to synchronize the
		// access to it with a mutex
		streamLock: &sync.Mutex{},
		storages:   in.Storages,
	}

	return c, nil
}

func (c *Client) Start() error {
	var err error

	c.grpcClient = pb.NewMessagesClient(c.grpcClientConn)
	c.stream, err = c.grpcClient.MessagesChat(c.ctx)
	if err != nil {
		return errors.Wrap(err, "cannot connect to the gRPC server")
	}

	if err := c.dbConnect(); err != nil {
		return errors.Wrap(err, "cannot connect to the database")
	}

	if err := c.updateClientInfo(); err != nil {
		return errors.Wrap(err, "cannot get MongoDB status information")
	}

	if err := c.register(); err != nil {
		return err
	}

	// start listening server messages
	go c.processIncommingServerMessages()
	go c.dbWatchdog()

	return nil
}

func (c *Client) dbConnect() (err error) {
	c.mdbSession, err = mgo.DialWithInfo(c.mgoDI)
	if err != nil {
		return err
	}
	c.mdbSession.SetMode(mgo.Eventual, true)

	bi, err := c.mdbSession.BuildInfo()
	if err != nil {
		return errors.Wrapf(err, "Cannot get build info")
	}
	if !bi.VersionAtLeast(3, 4) {
		return fmt.Errorf("you need at least MongoDB version 3.4 to run this tool")
	}

	return nil
}

func (c *Client) updateClientInfo() (err error) {
	c.lock.Lock()
	defer c.lock.Unlock()

	isMaster, err := cluster.NewIsMaster(c.mdbSession)
	if err != nil {
		return errors.Wrap(err, "cannot update client info")
	}

	c.isMasterDoc = isMaster.IsMasterDoc()
	c.nodeType = getNodeType(isMaster)

	if isMaster.IsMasterDoc().Me != "" {
		c.nodeName = isMaster.IsMasterDoc().Me
	} else {
		status := struct {
			Host string `bson:"host"`
		}{}
		err = c.mdbSession.Run(bson.D{{Name: "serverStatus", Value: 1}}, &status)
		if err != nil {
			return fmt.Errorf("cannot get an agent's ID from serverStatus: %s", err)
		}
		c.nodeName = status.Host
	}

	c.id = c.nodeName
	if c.nodeType != pb.NodeType_NODE_TYPE_MONGOS {
		replset, err := cluster.NewReplset(c.mdbSession)
		if err != nil {
			return fmt.Errorf("cannot create a new replicaset instance: %s", err)
		}
		c.replicasetName = replset.Name()
		c.replicasetID = replset.ID().Hex()
	} else {
		c.nodeName = c.mgoDI.Addrs[0]
	}

	if clusterID, _ := cluster.GetClusterID(c.mdbSession); clusterID != nil {
		c.clusterID = clusterID.Hex()
	}

	return nil
}

func (c *Client) ID() string {
	return c.id
}

func (c *Client) NodeName() string {
	return c.nodeName
}

func (c *Client) ReplicasetName() string {
	return c.replicasetName
}

func (c *Client) isRunning() bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.running
}

func (c *Client) connect() {
	if !c.isRunning() {
		return
	}

	for {
		c.logger.Infof("Reconnecting with the gRPC server")
		stream, err := c.grpcClient.MessagesChat(c.ctx)
		if err != nil {
			time.Sleep(3 * time.Second)
			continue
		}
		c.stream = stream
		c.lock.Lock()
		c.running = true
		c.lock.Unlock()

		return // remember we are in a reconnect for loop and we need to exit it
	}
}

func (c *Client) register() error {
	if !c.isRunning() {
		return fmt.Errorf("gRPC stream is closed. Cannot register the client (%s)", c.id)
	}

	isMaster, err := cluster.NewIsMaster(c.mdbSession)
	if err != nil {
		return errors.Wrap(err, "cannot get IsMasterDoc for register method")
	}

	m := &pb.ClientMessage{
		ClientId: c.id,
		Payload: &pb.ClientMessage_RegisterMsg{
			RegisterMsg: &pb.Register{
				NodeType:       c.nodeType,
				NodeName:       c.nodeName,
				ClusterId:      c.clusterID,
				ReplicasetName: c.replicasetName,
				ReplicasetId:   c.replicasetID,
				IsPrimary: isMaster.IsMasterDoc().IsMaster &&
					isMaster.IsMasterDoc().SetName != "" &&
					c.isMasterDoc.Msg != isdbgrid,
				IsSecondary: isMaster.IsMasterDoc().Secondary,
			},
		},
	}
	c.logger.Infof("Registering node ...")
	if err := c.stream.Send(m); err != nil {
		return errors.Wrapf(err, "cannot register node %s", c.nodeName)
	}

	response, err := c.stream.Recv()
	if err != nil {
		return errors.Wrap(err, "error detected while receiving the RegisterMsg ACK")
	}

	switch response.Payload.(type) {
	case *pb.ServerMessage_AckMsg:
		c.logger.Info("Node registered")
		return nil
	case *pb.ServerMessage_ErrorMsg:
		return fmt.Errorf(response.GetErrorMsg().GetMessage())
	}

	return fmt.Errorf("unknow response type %T", response.Payload)
}

func (c *Client) Stop() error {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.running = false

	c.cancelFunc()
	return nil
	//return c.stream.CloseSend()
}

func (c *Client) IsDBBackupRunning() bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.status.RunningDbBackup
}

func (c *Client) IsOplogBackupRunning() bool {
	c.lock.Lock()
	defer c.lock.Unlock()
	return c.status.RunningOplogBackup
}

func (c *Client) processIncommingServerMessages() {
	for {
		msg, err := c.stream.Recv()
		if err != nil { // Stream has been closed
			if !c.isRunning() {
				return
			}
			// Try to reconnect
			c.connect()
			if err := c.register(); err != nil {
				c.logger.Errorf("Cannot register client %s: %s", c.id, err)
				time.Sleep(5 * time.Second)
			}
			continue
		}

		var omsg *pb.ClientMessage

		switch msg.Payload.(type) {
		case *pb.ServerMessage_GetStatusMsg:
			omsg, err = c.processStatus()
		case *pb.ServerMessage_BackupSourceMsg:
			omsg, err = c.processGetBackupSource()
		case *pb.ServerMessage_ListReplicasets:
			omsg, err = c.processListReplicasets()
		case *pb.ServerMessage_PingMsg:
			omsg = c.processPing()
		case *pb.ServerMessage_CanRestoreBackupMsg:
			omsg, err = c.processCanRestoreBackup(msg.GetCanRestoreBackupMsg())
		case *pb.ServerMessage_GetCmdLineOpts:
			omsg, err = c.processGetCmdLineOpts()
		case *pb.ServerMessage_StartBackupMsg:
			// this runs in a go routine so there is no ClientMessage nor error to send
			c.processStartBackup(msg.GetStartBackupMsg())
			continue
		case *pb.ServerMessage_StopOplogTailMsg:
			// this will send an ACK and upon finishing, it will call grpcClient.OplogBackupFinished
			// instead of sending a regular message
			c.processStopOplogTail(msg.GetStopOplogTailMsg())
			continue
		case *pb.ServerMessage_CancelBackupMsg:
			err = c.processCancelBackup()
		case *pb.ServerMessage_RestoreBackupMsg:
			if err := c.processRestore(msg.GetRestoreBackupMsg()); err != nil {
				c.logger.Errorf("[client %s] cannot process restore: %s", c.id, err)
			}
			continue
		case *pb.ServerMessage_GetMongodbVersion:
			omsg, err = c.processGetMongoDBVersion()
		case *pb.ServerMessage_StopBalancerMsg:
			omsg, err = c.processStopBalancer()
		case *pb.ServerMessage_StartBalancerMsg:
			omsg, err = c.processStartBalancer()
		case *pb.ServerMessage_LastOplogTs:
			omsg, err = c.processLastOplogTs()
		case *pb.ServerMessage_GetStorageInfoMsg:
			omsg, err = c.processGetStorageInfo(msg.GetGetStorageInfoMsg())
		case *pb.ServerMessage_ListStoragesMsg:
			omsg, err = c.processListStorages()
		case *pb.ServerMessage_WriteFile:
			omsg, err = c.processWriteFile(msg.GetWriteFile())
		default:
			err = fmt.Errorf("client: %s, don't know how to handle gRPC message type %v", c.NodeName(), msg.Payload)
		}

		if err != nil {
			errMsg := &pb.ClientMessage{
				ClientId: c.id,
				Payload:  &pb.ClientMessage_ErrorMsg{ErrorMsg: &pb.Error{Message: err.Error()}},
			}
			if err = c.streamSend(errMsg); err != nil {
				c.logger.Errorf("Cannot send error response (%+v) for message type %T to the RPC server: %s", msg, msg.Payload, err)
			}
			continue
		}

		if omsg != nil {
			if err := c.streamSend(omsg); err != nil {
				c.logger.Errorf("cannot send processStopBalancer response to the server: %s", err)
			}
		}
	}
}

func (c *Client) dbWatchdog() {
	for {
		select {
		case <-time.After(dbPingInterval):
			if err := c.mdbSession.Ping(); err != nil {
				c.dbReconnect()
			}
		case <-c.dbReconnectChan:
			c.dbReconnect()
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) dbReconnect() {
	for {
		if err := c.dbConnect(); err == nil {
			return
		}
		select {
		case <-time.After(dbReconnectInterval):
		case <-c.ctx.Done():
			return
		}
	}
}

func (c *Client) processCancelBackup() error {
	err := c.mongoDumper.Stop()
	c.oplogTailer.Cancel()
	return err
}

func (c *Client) processCanRestoreBackup(msg *pb.CanRestoreBackup) (*pb.ClientMessage, error) {
	var err error
	if err := c.updateClientInfo(); err != nil {
		return nil, err
	}
	resp := &pb.CanRestoreBackupResponse{
		ClientId:   c.id,
		IsPrimary:  c.isMasterDoc.IsMaster && c.isMasterDoc.SetName != "" && c.isMasterDoc.Msg != isdbgrid,
		Replicaset: c.ReplicasetName(),
		Host:       c.connOpts.Host,
		Port:       c.connOpts.Port,
	}

	stg, err := c.storages.Get(msg.GetStorageName())
	if err != nil {
		c.logger.Errorf("cannot get storage name %s: %s", msg.GetStorageName(), err)
		return nil, err
	}

	switch stg.Type {
	case typeFilesystem:
		resp.CanRestore, err = c.checkCanRestoreLocal(msg)
	case typeS3:
		resp.CanRestore, err = c.checkCanRestoreS3(msg)
	}
	if err != nil {
		c.logger.Errorf("can restore error: %s", err)
		return nil, err
	}

	outMsg := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_CanRestoreBackupMsg{CanRestoreBackupMsg: resp},
	}

	return outMsg, nil
}

func (c *Client) checkCanRestoreLocal(msg *pb.CanRestoreBackup) (bool, error) {
	stg, _ := c.storages.Get(msg.GetStorageName())
	path := filepath.Join(stg.Filesystem.Path, msg.GetBackupName())
	fi, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	if fi.IsDir() || fi.Size() == 0 {
		return false, fmt.Errorf("%v is a directory or the file size is 0", path)
	}
	return true, nil
}

func (c *Client) checkCanRestoreS3(msg *pb.CanRestoreBackup) (bool, error) {
	stg, _ := c.storages.Get(msg.GetStorageName())
	awsSession, err := awsutils.GetAWSSessionFromStorage(stg.S3)
	if err != nil {
		return false, fmt.Errorf("checkCanRestoreS3: cannot get AWS session: %s", err)
	}
	svc := s3.New(awsSession)
	c.logger.Debugf("Checking if client %s can restore %s from s3 %s bucket %s", c.id,
		msg.GetBackupName(),
		stg.S3.Region,
		stg.S3.Bucket,
	)
	fi, err := awsutils.S3Stat(svc, stg.S3.Bucket, msg.GetBackupName())
	if err != nil {
		return false, fmt.Errorf("cannot check if backup exists in S3: %s", err)
	}
	if fi != nil {
		return true, nil
	}
	return false, nil
}

func (c *Client) processGetBackupSource() (*pb.ClientMessage, error) {
	c.logger.Debug("Received GetBackupSource command")
	r, err := cluster.NewReplset(c.mdbSession)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get cluster's replicasets for processGetBackupSource")
	}

	winner, err := r.BackupSource(nil)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get a backup source winner for processGetBackupSource")
	}
	if winner == "" {
		winner = c.nodeName
	}

	msg := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_BackupSourceMsg{BackupSourceMsg: &pb.BackupSource{SourceClient: winner}},
	}
	return msg, nil
}

func (c *Client) processGetCmdLineOpts() (*pb.ClientMessage, error) {
	cmdLineOpts := bson.M{}
	err := c.mdbSession.Run(bson.D{{Name: "getCmdLineOpts", Value: 1}}, &cmdLineOpts)
	if err != nil {
		return nil, fmt.Errorf("cannot get cmdLineOpts: %s", err)
	}
	buf, err := json.Marshal(cmdLineOpts)
	if err != nil {
		return nil, errors.Wrap(err, "cannot encode cmdLineOpts as JSON")
	}

	omsg := &pb.ClientMessage{
		ClientId: c.id,
		Payload: &pb.ClientMessage_CmdLineOpts{
			CmdLineOpts: &pb.CmdLineOpts{
				Options: buf,
			},
		},
	}

	return omsg, nil
}

func (c *Client) processGetStorageInfo(msg *pb.GetStorageInfo) (*pb.ClientMessage, error) {
	stg, err := c.storages.Get(msg.GetStorageName())
	if err != nil {
		return nil, errors.Wrap(err, "cannot process GetStorageInfo")
	}

	var valid, canRead, canWrite bool

	switch strings.ToLower(stg.Type) {
	case typeS3:
		valid, canRead, canWrite, err = c.checkS3Access(stg.S3)
	case typeFilesystem:
		valid, canRead, canWrite, err = c.checkFilesystemAccess(stg.Filesystem.Path)
	}
	if err != nil {
		return nil, errors.Wrapf(err, "cannot check is storage %q is valid", msg.GetStorageName())
	}

	omsg := &pb.ClientMessage{
		ClientId: c.id,
		Payload: &pb.ClientMessage_StorageInfo{
			StorageInfo: &pb.StorageInfo{
				Name:     msg.GetStorageName(),
				Type:     stg.Type,
				Valid:    valid,
				CanRead:  canRead,
				CanWrite: canWrite,
				S3: &pb.S3{
					Region:      stg.S3.Region,
					EndpointUrl: stg.S3.EndpointURL,
					Bucket:      stg.S3.Bucket,
				},
				Filesystem: &pb.Filesystem{
					Path: stg.Filesystem.Path,
				},
			},
		},
	}

	return omsg, nil
}

// processGetMongoDBVersion returns the MongoDB version from the BuildInfo mgo's method.
// We need to know it because starting with MongoDB 4.0, collections have an UUID and mongo restore
// has a parameter named preserveUUID but it should be set to false for Mongo 3.6 bacause if it is
// set to true and the underlying Mongo version is not 4.0+, mongo restore fails
func (c *Client) processGetMongoDBVersion() (*pb.ClientMessage, error) {
	bi, err := c.mdbSession.BuildInfo()
	if err != nil {
		return nil, errors.Wrap(err, "cannot read BuildInfo to get MongoDB version")
	}
	omsg := &pb.ClientMessage{
		ClientId: c.id,
		Payload: &pb.ClientMessage_MongodbVersion{
			MongodbVersion: &pb.MongoDBVersion{
				Version: bi.Version,
			},
		},
	}
	return omsg, nil
}

func (c *Client) processListStorages() (*pb.ClientMessage, error) {
	ssi := []*pb.StorageInfo{}

	for name, stg := range c.storages.Storages {
		var valid, canRead, canWrite bool
		var err error

		switch strings.ToLower(stg.Type) {
		case typeS3:
			valid, canRead, canWrite, err = c.checkS3Access(stg.S3)
		case typeFilesystem:
			valid, canRead, canWrite, err = c.checkFilesystemAccess(stg.Filesystem.Path)
		}
		if err != nil {
			return nil, errors.Wrapf(err, "processListStorages: storage %q is invalid", name)
		}
		si := &pb.StorageInfo{
			Name:     name,
			Type:     stg.Type,
			Valid:    valid,
			CanRead:  canRead,
			CanWrite: canWrite,
			S3: &pb.S3{
				Region:      stg.S3.Region,
				EndpointUrl: stg.S3.EndpointURL,
				Bucket:      stg.S3.Bucket,
			},
			Filesystem: &pb.Filesystem{
				Path: stg.Filesystem.Path,
			},
		}
		ssi = append(ssi, si)
	}

	omsg := &pb.ClientMessage{
		ClientId: c.id,
		Payload: &pb.ClientMessage_StoragesInfo{
			StoragesInfo: &pb.StoragesInfo{StoragesInfo: ssi},
		},
	}

	return omsg, nil
}

func (c *Client) processLastOplogTs() (*pb.ClientMessage, error) {
	isMaster, err := cluster.NewIsMaster(c.mdbSession)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get masterDoc for processLastOplogTs")
	}

	msg := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_LastOplogTs{LastOplogTs: &pb.LastOplogTs{LastOplogTs: int64(isMaster.LastWrite())}},
	}
	return msg, nil
}

func (c *Client) processListReplicasets() (*pb.ClientMessage, error) {
	var sm shardsMap
	err := c.mdbSession.Run("getShardMap", &sm)
	if err != nil {
		return nil, errors.Wrap(err, "cannot getShardMap for processListReplicasets")
	}

	replicasets := []string{}
	/* Example
	mongos> db.getSiblingDB('admin').runCommand('getShardMap')
	{
	  "map" : {
	    "config" : "localhost:19001,localhost:19002,localhost:19003",
	    "localhost:17001" : "r1/localhost:17001,localhost:17002,localhost:17003",
	    "r1" : "r1/localhost:17001,localhost:17002,localhost:17003",
	    "r1/localhost:17001,localhost:17002,localhost:17003" : "r1/localhost:17001,localhost:17002,localhost:17003",
	  },
	  "ok" : 1
	}
	*/
	for key := range sm.Map {
		m := strings.Split(key, "/")
		if len(m) < 2 {
			continue
		}
		replicasets = append(replicasets, m[0])
	}

	msg := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_ReplicasetsMsg{ReplicasetsMsg: &pb.Replicasets{Replicasets: replicasets}},
	}
	return msg, nil
}

func (c *Client) processPing() *pb.ClientMessage {
	c.logger.Debug("Received Ping command")
	if err := c.updateClientInfo(); err != nil {
		c.logger.Errorf("Error while processing the ping message: %s", err)
	}

	pongMsg := &pb.Pong{
		Timestamp:           time.Now().Unix(),
		NodeType:            c.nodeType,
		ReplicaSetUuid:      c.replicasetID,
		ReplicaSetVersion:   0,
		IsPrimary:           c.isMasterDoc.IsMaster && c.isMasterDoc.SetName != "" && c.isMasterDoc.Msg != "isdbgrid",
		IsSecondary:         !c.isMasterDoc.IsMaster,
		IsTailing:           c.IsOplogBackupRunning(),
		LastTailedTimestamp: c.oplogTailer.LastOplogTimestamp().Time().Unix(),
	}

	msg := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_PongMsg{PongMsg: pongMsg},
	}
	return msg
}

func (c *Client) processRestore(msg *pb.RestoreBackup) error {
	c.logger.Debug("Starting restore")
	c.logger.Debug("Status mutex Lock")
	c.lock.Lock()
	c.status.RestoreStatus = pb.RestoreStatus_RESTORE_STATUS_RESTORINGDB
	c.lock.Unlock()
	c.logger.Debug("Status mutex Unlock")

	defer func() {
		c.lock.Lock()
		c.status.RestoreStatus = pb.RestoreStatus_RESTORE_STATUS_NOT_RUNNING
		c.lock.Unlock()
	}()

	c.logger.Debug("Sending ACK to the gRPC server")
	c.sendACK()

	c.logger.Debug("Starting DB dump restore process")
	c.logger.Debugf("Incomig restore msg: %+v", msg)
	if err := c.restoreDBDump(msg); err != nil {
		err := errors.Wrapf(err, "cannot restore DB backup file %s", msg.GetDbSourceName())
		if sendErr := c.sendRestoreComplete(err); sendErr != nil {
			err = multierror.Append(err, sendErr)
		}
		c.logger.Debug(err)
		return err
	}

	if c.nodeType != pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR {
		c.logger.Debug("Starting oplog restore process")
		c.logger.Debug("Config Server mutex lock")
		c.lock.Lock()
		c.status.RestoreStatus = pb.RestoreStatus_RESTORE_STATUS_RESTORINGOPLOG
		c.lock.Unlock()
		c.logger.Debug("Config Server mutex unlocked")

		c.mdbSession.ResetIndexCache()
		c.mdbSession.Refresh()
		if err := c.restoreOplog(msg); err != nil {
			err := errors.Wrapf(err, "[%s] cannot restore Oplog backup file %s", c.id, msg.GetOplogSourceName())
			if err1 := c.sendRestoreComplete(err); err1 != nil {
				err = errors.Wrapf(err, "cannot send backup complete message: %s", err1)
			}
			c.logger.Debug(err)
			return err
		}
	}

	c.logger.Debug("Sending restore completed message to the gRPC server")
	if err := c.sendRestoreComplete(nil); err != nil {
		err = errors.Wrap(err, "cannot send backup completed message")
		c.logger.Error(err)
		return err
	}

	c.logger.Debug("Restore completed")
	return nil
}

func (c *Client) sendRestoreComplete(err error) error {
	msg := &pb.RestoreComplete{
		ClientId: c.id,
		Err:      &pb.Error{},
	}
	if err != nil {
		msg.Err = &pb.Error{
			Message: err.Error(),
		}
	}

	_, err = c.grpcClient.RestoreCompleted(context.Background(), msg)
	if err != nil {
		return err
	}

	return nil
}

func (c *Client) processStartBackup(msg *pb.StartBackup) {
	c.logger.Info("Received StartBackup command")
	// Send the ACK message and work on the background. When the process finishes, it will send the
	// gRPC messages to signal that the backup has been completed
	c.sendACK()

	c.lock.Lock()
	defer c.lock.Unlock()

	if c.status.RunningDbBackup {
		c.sendDBBackupFinishError(fmt.Errorf("backup already running"))
		return
	}

	// Validate backup type by asking MongoDB capabilities?
	if msg.BackupType == pb.BackupType_BACKUP_TYPE_INVALID {
		c.sendDBBackupFinishError(fmt.Errorf("backup type should be hot or logical"))
		return
	}
	if msg.BackupType != pb.BackupType_BACKUP_TYPE_LOGICAL {
		c.sendDBBackupFinishError(fmt.Errorf("hot Backup is not implemented yet"))
		return
	}

	var err error

	stg, err := c.storages.Get(msg.GetStorageName())
	if err != nil {
		c.sendDBBackupFinishError(errors.Wrapf(err, "Invalid storage name %s", msg.GetStorageName()))
		return
	}

	switch stg.Type {
	case typeFilesystem:
		fi, err := os.Stat(stg.Filesystem.Path)
		if err != nil {
			c.sendDBBackupFinishError(errors.Wrapf(err, "Error while checking destination directory: %s", stg.Filesystem.Path))
			return
		}
		if !fi.IsDir() {
			c.sendDBBackupFinishError(fmt.Errorf("%s is not a directory", stg.Filesystem.Path))
			return
		}
	case typeS3:
	}

	// There is a delay when starting a new go-routine. We need to instantiate c.oplogTailer here otherwise
	// if we run go c.runOplogBackup(msg) and then WaitUntilFirstDoc(), the oplogTailer can be nill because
	// of the delay
	c.oplogTailer, err = oplog.Open(c.mdbSession)
	if err != nil {
		c.logger.Errorf("Cannot open the oplog tailer: %s", err)
		finishMsg := &pb.OplogBackupFinishStatus{
			ClientId: c.id,
			Ok:       false,
			Ts:       time.Now().Unix(),
			Error:    fmt.Sprintf("Cannot open the oplog tailer: %s", err),
		}
		c.logger.Debugf("Sending OplogFinishStatus with cannot open the tailer error to the gRPC server: %+v", *finishMsg)
		if _, err := c.grpcClient.OplogBackupFinished(context.Background(), finishMsg); err != nil {
			c.logger.Errorf("cannot signal OplogBackupFinished: %s", err)
		}
		return
	}

	c.logger.Debug("Starting oplog backup")
	go c.runOplogBackup(msg, c.oplogTailer)
	// Wait until we have at least one document from the tailer to start the backup only after we have
	// documents in the oplog tailer.
	c.logger.Debug("Waiting oplog first doc")
	if err := c.oplogTailer.WaitUntilFirstDoc(); err != nil {
		err := errors.Wrapf(err, "Cannot read from the oplog tailer")
		c.oplogTailer.Cancel()
		c.logger.Error(err)
		finishMsg := &pb.OplogBackupFinishStatus{
			ClientId: c.id,
			Ok:       false,
			Ts:       time.Now().Unix(),
			Error:    err.Error(),
		}
		if _, err := c.grpcClient.OplogBackupFinished(context.Background(), finishMsg); err != nil {
			c.logger.Errorf("cannot signal OplogBackupFinished: %s", err)
		}
		return
	}
	c.logger.Debugf("Starting DB backup")
	go func() {
		err := c.runDBBackup(msg)
		if err != nil {
			c.sendDBBackupFinishError(fmt.Errorf("cannot check if S3 bucket %q exists: %s", c.backupDir, err))
			return
		}
		c.sendBackupFinishOK()
	}()
}

func (c *Client) processStartBalancer() (*pb.ClientMessage, error) {
	if c.nodeType != pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR {
		return nil, fmt.Errorf("Start balancer only works on config servers")
	}

	mongosSession, err := c.getMongosSession()
	if err != nil {
		return nil, errors.Wrap(err, "cannot process Stop Balancer")
	}
	balancer, err := cluster.NewBalancer(mongosSession)
	if err != nil {
		return nil, errors.Wrap(err, "processStartBalancer -> cannot create a balancer instance")
	}
	if err := balancer.Start(); err != nil {
		return nil, err
	}
	c.logger.Debugf("Balancer has been started by %s", c.id)

	out := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_AckMsg{AckMsg: &pb.Ack{}},
	}

	return out, nil
}

func (c *Client) processStatus() (*pb.ClientMessage, error) {
	c.logger.Debug("Received Status command")
	c.lock.Lock()

	isMaster, err := cluster.NewIsMaster(c.mdbSession)
	if err != nil {
		return nil, errors.Wrap(err, "cannot get IsMaster for processStatus")
	}

	msg := &pb.ClientMessage{
		ClientId: c.id,
		Payload: &pb.ClientMessage_StatusMsg{
			StatusMsg: &pb.Status{
				RunningDbBackup:    c.status.RunningDbBackup,
				RunningOplogBackup: c.status.RunningOplogBackup,
				BackupType:         c.status.BackupType,
				BytesSent:          c.status.BytesSent,
				LastOplogTs:        c.status.LastOplogTs,
				BackupCompleted:    c.status.BackupCompleted,
				LastError:          c.status.LastError,
				ReplicasetVersion:  c.status.ReplicasetVersion,
				DestinationName:    c.status.DestinationName,
				CompressionType:    c.status.CompressionType,
				Cypher:             c.status.Cypher,
				StartOplogTs:       c.status.StartOplogTs,
				IsPrimary: isMaster.IsMasterDoc().IsMaster &&
					isMaster.IsMasterDoc().SetName != "" &&
					c.isMasterDoc.Msg != isdbgrid,
			},
		},
	}
	c.lock.Unlock()
	return msg, nil
}

func (c *Client) getMongosSession() (*mgo.Session, error) {
	routers, err := cluster.GetMongosRouters(c.mdbSession)
	if err != nil {
		return nil, errors.Wrap(err, "cannot process Stop Balancer")
	}
	if len(routers) < 1 {
		return nil, Errorf(NoMongosError, "there are no mongodb routers")
	}

	parts := strings.Split(routers[0].Id, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("cannot get host and port from %s", routers[0].Id)
	}

	f, err := net.LookupHost(parts[0])
	if err != nil {
		return nil, errors.Wrapf(err, "cannot get host for %s", routers[0].Id)
	}

	// Due to a debian patch as a workaround for some issues.
	// MongoDB ussualy binds to 127.0.0.1 so, we have to manually fix it in order to be able to connect.
	// See https://www.debian.org/doc/manuals/debian-reference/ch05.en.html#_the_hostname_resolution
	if f[0] == "127.0.1.1" {
		f[0] = "127.0.0.1"
	}

	configHost := net.JoinHostPort(f[0], parts[1])

	di := &mgo.DialInfo{
		Addrs:    []string{configHost},
		Username: c.mgoDI.Username,
		Password: c.mgoDI.Password,
		Source:   c.mgoDI.Source,
		AppName:  "percona-backup-mongodb",
		FailFast: true,
		Direct:   true,
	}

	session, err := mgo.DialWithInfo(di)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot connect to the mongos instance on %s", configHost)
	}

	return session, nil
}

func (c *Client) processStopBalancer() (*pb.ClientMessage, error) {
	if c.nodeType != pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR {
		return nil, fmt.Errorf("Stop balancer only works on config servers. This is a [%d]%s type",
			c.nodeType, pb.NodeType_name[int32(c.nodeType)],
		)
	}

	mongosSession, err := c.getMongosSession()
	if err != nil {
		return nil, errors.Wrap(err, "cannot process Stop Balancer")
	}

	balancer, err := cluster.NewBalancer(mongosSession)
	if err != nil {
		return nil, errors.Wrap(err, "processStopBalancer -> cannot create a balancer instance")
	}
	if err := balancer.StopAndWait(balancerStopRetries, balancerStopTimeout); err != nil {
		return nil, err
	}

	c.logger.Debugf("Balancer has been stopped by %s", c.nodeName)
	out := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_AckMsg{AckMsg: &pb.Ack{}},
	}
	return out, nil
}

func (c *Client) processStopOplogTail(msg *pb.StopOplogTail) {
	c.logger.Debugf("Received StopOplogTail command for client: %s", c.id)
	c.sendACK()

	c.setOplogBackupRunning(false)

	if err := c.oplogTailer.CloseAt(bson.MongoTimestamp(msg.GetTs())); err != nil {
		c.logger.Errorf("Cannot stop the oplog tailer: %s", err)
		finishMsg := &pb.OplogBackupFinishStatus{
			ClientId: c.id,
			Ok:       false,
			Ts:       time.Now().Unix(),
			Error:    fmt.Sprintf("Cannot close the oplog tailer: %s", err),
		}
		c.logger.Debugf("Sending OplogFinishStatus with error to the gRPC server: %+v", *finishMsg)
		if ack, err := c.grpcClient.OplogBackupFinished(context.Background(), finishMsg); err != nil {
			c.logger.Errorf("Cannot call OplogBackupFinished RPC method: %s", err)
		} else {
			c.logger.Debugf("Received ACK from OplogBackupFinished RPC method: %+v", *ack)
		}
		return
	}

	finishMsg := &pb.OplogBackupFinishStatus{
		ClientId: c.id,
		Ok:       true,
		Ts:       time.Now().Unix(),
		Error:    "",
	}
	c.logger.Debugf("Sending OplogFinishStatus OK to the gRPC server: %+v", *finishMsg)
	if ack, err := c.grpcClient.OplogBackupFinished(context.Background(), finishMsg); err != nil {
		c.logger.Errorf("Cannot call OplogBackupFinished RPC method: %s", err)
	} else {
		c.logger.Debugf("Received ACK from OplogBackupFinished RPC method: %+v", *ack)
	}
}

func (c *Client) processWriteFile(msg *pb.WriteFile) (*pb.ClientMessage, error) {
	stg, err := c.storages.Get(msg.GetStorageName())
	if err != nil {
		return nil, errors.Wrap(err, "cannot process Write File")
	}

	bw, err := writer.NewBackupWriter(
		stg, msg.GetFileName(),
		pb.CompressionType_COMPRESSION_TYPE_NO_COMPRESSION,
		pb.Cypher_CYPHER_NO_CYPHER,
	)
	if err != nil {
		return nil, errors.Wrapf(err, "cannot create a writer for file %s using storage %s",
			msg.GetFileName(),
			msg.GetStorageName(),
		)
	}
	defer func() {
		if err := bw.Close(); err != nil {
			c.logger.Errorf("cannot close file writer for processWriteFile. Storage: %s, file: %s, Error: %s",
				msg.GetStorageName(),
				msg.GetFileName(),
				err,
			)

		}
	}()
	_, err = io.Copy(bw, bytes.NewBuffer(msg.GetData()))
	if err != nil {
		return nil, errors.Wrapf(err, "cannot write file %s to storage %s: %s",
			msg.GetFileName(),
			msg.GetStorageName(),
			err,
		)
	}
	omsg := &pb.ClientMessage{
		ClientId: c.id,
		Payload: &pb.ClientMessage_WriteStatus{
			WriteStatus: &pb.WriteStatus{
				Error: "",
			},
		},
	}
	return omsg, nil
}

func (c *Client) runDBBackup(msg *pb.StartBackup) error {
	var err error
	c.logger.Info("Starting DB backup")
	stg, _ := c.storages.Get(msg.GetStorageName())

	bw, err := writer.NewBackupWriter(stg, msg.GetDbBackupName(), msg.GetCompressionType(), msg.GetCypher())
	if err != nil {
		return err
	}

	mi := &dumper.MongodumpInput{
		Host:     c.connOpts.Host,
		Port:     c.connOpts.Port,
		Username: c.connOpts.User,
		Password: c.connOpts.Password,
		Gzip:     false,
		Oplog:    false,
		Threads:  1,
		Writer:   bw,
	}

	c.mongoDumper, err = dumper.NewMongodump(mi)
	if err != nil {
		return err
	}

	c.setDBBackupRunning(true)
	defer c.setDBBackupRunning(false)

	if err := c.mongoDumper.Start(); err != nil {
		return errors.Wrap(err, "Cannot start the backup")
	}

	derr := c.mongoDumper.Wait()
	if err = bw.Close(); err != nil {
		return err
	}
	if derr != nil {
		return derr
	}

	c.logger.Info("DB dump completed")
	return nil
}

func (c *Client) runOplogBackup(msg *pb.StartBackup, oplogTailer io.Reader) {
	c.logger.Info("Starting oplog backup")

	c.setOplogBackupRunning(true)
	defer c.setOplogBackupRunning(false)

	stg, _ := c.storages.Get(msg.GetStorageName())

	bw, err := writer.NewBackupWriter(stg, msg.GetOplogBackupName(), msg.GetCompressionType(), msg.GetCypher())
	if err != nil {
		c.setOplogBackupRunning(false)
		c.logger.Errorf("Error while copying data from the oplog tailer: %s", err)
		finishMsg := &pb.OplogBackupFinishStatus{
			ClientId: c.id,
			Ok:       false,
			Ts:       time.Now().Unix(),
			Error:    fmt.Sprintf("Cannot open the oplog tailer: %s", err),
		}
		c.logger.Debugf("Sending OplogFinishStatus with cannot open the tailer error to the gRPC server: %+v", *finishMsg)
		if _, err := c.grpcClient.OplogBackupFinished(context.Background(), finishMsg); err != nil {
			c.logger.Errorf("Cannot signal Oplog Backup Finished with error (%s): %s", finishMsg.Error, err)
		}
		return
	}

	n, err := io.Copy(bw, oplogTailer)
	bw.Close()
	if err != nil {
		c.setOplogBackupRunning(false)
		c.logger.Errorf("Error while copying data from the oplog tailer: %s", err)
		finishMsg := &pb.OplogBackupFinishStatus{
			ClientId: c.id,
			Ok:       false,
			Ts:       time.Now().Unix(),
			Error:    fmt.Sprintf("Cannot open the oplog tailer: %s", err),
		}
		c.logger.Debugf("Sending OplogFinishStatus with cannot open the tailer error to the gRPC server: %+v", *finishMsg)
		if _, err := c.grpcClient.OplogBackupFinished(context.Background(), finishMsg); err != nil {
			c.logger.Errorf("Cannot signal Oplog Backup Finished OK: %s", err)
		}
		return
	}

	c.lock.Lock()
	c.status.BytesSent += uint64(n)
	c.lock.Unlock()

	c.logger.Info("Oplog backup completed")
	finishMsg := &pb.OplogBackupFinishStatus{
		ClientId: c.id,
		Ok:       true,
		Ts:       time.Now().Unix(),
		Error:    "",
	}
	c.logger.Debugf("Sending OplogFinishStatus to the gRPC server: %+v", *finishMsg)
	if ack, err := c.grpcClient.OplogBackupFinished(context.Background(), finishMsg); err != nil {
		c.logger.Errorf("Cannot call OplogFinishStatus RPC method: %s", err)
	} else {
		c.logger.Debugf("Received ACK from OplogFinishStatus RPC method: %+v", *ack)
	}
}

func (c *Client) sendACK() {
	response := &pb.ClientMessage{
		ClientId: c.id,
		Payload:  &pb.ClientMessage_AckMsg{AckMsg: &pb.Ack{}},
	}
	if err := c.streamSend(response); err != nil {
		c.logger.Errorf("sendACK error: cannot stream response to the server: %s. Out message: %+v. In message type: %T",
			err, *response, response.Payload,
		)
	}
}

func (c *Client) sendBackupFinishOK() {
	ismaster, err := cluster.NewIsMaster(c.mdbSession)
	// This should never happen.
	if err != nil {
		c.logger.Errorf("cannot get LastWrite.OpTime.Ts from MongoDB: %s", err)
		finishMsg := &pb.DBBackupFinishStatus{
			ClientId: c.id,
			Ok:       false,
			Ts:       0,
			Error:    err.Error(),
		}
		if _, err := c.grpcClient.DBBackupFinished(context.Background(), finishMsg); err != nil {
			c.logger.Errorf("Cannot signal DB Backup finished with error (%s): %s", finishMsg.Error, err)
		}
		return
	}

	finishMsg := &pb.DBBackupFinishStatus{
		ClientId: c.id,
		Ok:       true,
		Ts:       int64(ismaster.IsMasterDoc().LastWrite.OpTime.Ts),
		Error:    "",
	}
	c.logger.Debugf("Sending DBBackupFinishStatus to the gRPC server: %+v", *finishMsg)
	if ack, err := c.grpcClient.DBBackupFinished(context.Background(), finishMsg); err != nil {
		c.logger.Errorf("Cannot call DBBackupFinished RPC method: %s", err)
	} else {
		c.logger.Debugf("Received ACK from DBBackupFinished RPC method: %+v", *ack)
	}
}

func (c *Client) sendDBBackupFinishError(err error) {
	finishMsg := &pb.DBBackupFinishStatus{
		ClientId: c.id,
		Ok:       false,
		Ts:       0,
		Error:    err.Error(),
	}
	if ack, err := c.grpcClient.DBBackupFinished(context.Background(), finishMsg); err != nil {
		c.logger.Errorf("Cannot call DBBackupFinished with error (%q): \n-\n%s\n-\n", finishMsg.Error, err)
	} else {
		c.logger.Debugf("Received ACK from DBBackupFinished with error RPC method: %+v", *ack)
	}
}

func (c *Client) setDBBackupRunning(status bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.status.RunningDbBackup = status
}

func (c *Client) setOplogBackupRunning(status bool) {
	c.lock.Lock()
	defer c.lock.Unlock()
	c.status.RunningOplogBackup = status
}

func (c *Client) streamSend(msg *pb.ClientMessage) error {
	c.streamLock.Lock()
	defer c.streamLock.Unlock()
	return c.stream.Send(msg)
}

func (c *Client) NodeTye() pb.NodeType {
	return c.nodeType
}

func getNodeType(isMaster *cluster.IsMaster) pb.NodeType {
	if isMaster.IsShardServer() {
		return pb.NodeType_NODE_TYPE_MONGOD_SHARDSVR
	}
	// Don't change the order. A config server can also be a replica set so we need to call this BEFORE
	// calling .IsReplset()
	if isMaster.IsConfigServer() {
		return pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR
	}
	if isMaster.IsReplset() {
		return pb.NodeType_NODE_TYPE_MONGOD_REPLSET
	}
	if isMaster.IsMongos() {
		return pb.NodeType_NODE_TYPE_MONGOS
	}
	return pb.NodeType_NODE_TYPE_MONGOD
}

func (c *Client) ListStorages() ([]*pb.StorageInfo, error) {
	ssi := []*pb.StorageInfo{}

	for name, stg := range c.storages.Storages {
		var valid, canRead, canWrite bool
		var err error

		switch strings.ToLower(stg.Type) {
		case typeS3:
			valid, canRead, canWrite, err = c.checkS3Access(stg.S3)
		case typeFilesystem:
			valid, canRead, canWrite, err = c.checkFilesystemAccess(stg.Filesystem.Path)
		}
		if err != nil {
			return nil, errors.Wrapf(err, "processListStorages: storage %q is invalid", name)
		}
		si := &pb.StorageInfo{
			Name:     name,
			Type:     stg.Type,
			Valid:    valid,
			CanRead:  canRead,
			CanWrite: canWrite,
			S3: &pb.S3{
				Region:      stg.S3.Region,
				EndpointUrl: stg.S3.EndpointURL,
				Bucket:      stg.S3.Bucket,
			},
			Filesystem: &pb.Filesystem{
				Path: stg.Filesystem.Path,
			},
		}
		ssi = append(ssi, si)
	}
	return ssi, nil
}

func (c *Client) restoreDBDump(msg *pb.RestoreBackup) (err error) {
	c.logger.Debugf("Entering restoreDBDump")
	stg, err := c.storages.Get(msg.GetStorageName())
	if err != nil {
		return errors.Wrap(err, "invalid storage name received in restoreDBDump")
	}
	c.logger.Debugf("Making a backup reader for the restore. Name: %s, CompressionType: %v"+
		", cypher: %v, storage: %s", msg.GetDbSourceName(), msg.GetCompressionType(),
		msg.GetCypher(), msg.GetStorageName())

	rdr, err := reader.MakeReader(msg.GetDbSourceName(), stg, msg.GetCompressionType(), msg.GetCypher())
	if err != nil {
		c.logger.Errorf("restoreDBDump: cannot get a backup reader: %s", err)
		return errors.Wrap(err, "restoreDBDump: cannot get a backup reader")
	}

	// The config.version collection cannot be dropped while in --configsvr mode so, we need to clean it
	// up manually
	if c.nodeType == pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR {
		c.logger.Debugf("This is a comfig server. Removing the config.version collection")
		if _, err := c.mdbSession.DB("config").C("version").RemoveAll(nil); err != nil {
			c.logger.Warnf("cannot empty config.version collection: %s", err)
		}
	}

	// We need to set Archive = "-" so MongoRestore can use the provided reader.
	// Why we don't want to use archive? Because if we use Archive, we are limited to files in the local
	// filesystem and those files could be plain dumps or gzipped dump files but we want to be able to:
	// 1. Read from a stream (S3?)
	// 2. Use different compression algorithms.
	// 3. Read from encrypted backups.
	// That's why we are providing our own reader to MongoRestore
	v4, _ := version.NewVersion("4.0")
	bi, err := c.mdbSession.BuildInfo()
	if err != nil {
		return errors.Wrap(err, "cannot read BuildInfo to get MongoDB version")
	}
	v, err := version.NewVersion(bi.Version)
	if err != nil {
		return errors.Wrapf(err, "cannot parse MongoDB version from BuildInfo: %q", bi.Version)
	}

	preserveUUID := true
	dropCollections := true
	ingnoreErrors := false
	if c.nodeType == pb.NodeType_NODE_TYPE_MONGOD_CONFIGSVR {
		// For config servers, if we set dropCollections to true, we are going to receive this error:
		//   cannot drop config.version document while in --configsvr mode
		// We must ignore the errors and we shouldn't drop the collections
		dropCollections = false
		ingnoreErrors = true
		preserveUUID = false // cannot be used with dropCollections=false
	}

	// If the destination server is < v4.0, collections don't have an UUID
	if v.LessThan(v4) {
		preserveUUID = false
	}

	v, err = version.NewVersion(msg.GetMongodbVersion())
	c.logger.Debugf("Incoming restore message: %+v", msg)
	if err != nil {
		c.logger.Debugf("MongoDB version from incommig message: %v\n", msg.GetMongodbVersion())
		return errors.Wrapf(err, "cannot parse backup source MongoDB version from incomming message version (%q)",
			msg.GetMongodbVersion())
	}
	// If the backup source was MongoDB < 4.0, the backed up collection don't have an UUID
	if v.LessThan(v4) {
		preserveUUID = false
	}

	input := &restore.MongoRestoreInput{
		Archive:         "-",
		DryRun:          false,
		Host:            msg.Host,
		Port:            msg.Port,
		Username:        c.connOpts.User,
		Password:        c.connOpts.Password,
		DropCollections: dropCollections,
		IgnoreErrors:    ingnoreErrors,
		Gzip:            false,
		Oplog:           false,
		Threads:         10,
		Reader:          rdr,
		PreserveUUID:    preserveUUID,
		// A real restore would be applied to a just created and empty instance and it should be
		// configured to run without user authentication.
		// For testing purposes, we can skip restoring users and roles.
		SkipUsersAndRoles: msg.SkipUsersAndRoles,
	}

	r, err := restore.NewMongoRestore(input)
	if err != nil {
		c.logger.Errorf("cannot instantiate mongo restore instance: %s", err)
		return errors.Wrap(err, "cannot instantiate mongo restore instance")
	}

	c.logger.Debug("Calling mongorestore.Start")
	if err := r.Start(); err != nil {
		return errors.Wrap(err, "cannot start restore")
	}

	c.logger.Debug("Waiting for mongo restore to finish")
	if err := r.Wait(); err != nil {
		return errors.Wrap(err, "error while trying to restore")
	}
	c.logger.Debug("mongo restore finished")

	// clean up the config.mongos collection to remove posibble invalid mongos servers from
	// the mongos collection. This collection will be updated automatically since all mongos
	// instances will ping the config server and the collection will be updated with all the
	// really alive mongos servers.
	c.logger.Debug("removing config.mongos")
	if _, err := c.mdbSession.DB("config").C("mongos").RemoveAll(nil); err != nil {
		c.logger.Info("cannot empty config.mongos collection")
	}
	return nil
}

func (c *Client) restoreOplog(msg *pb.RestoreBackup) (err error) {
	stg, err := c.storages.Get(msg.GetStorageName())
	if err != nil {
		return errors.Wrap(err, "invalid storage name received in restoreDBDump")
	}
	rdr, err := reader.MakeReader(msg.GetOplogSourceName(), stg, msg.GetCompressionType(), msg.GetCypher())
	if err != nil {
		return errors.Wrap(err, "restoreDBDump: cannot get a backup reader")
	}

	bsonReader, err := bsonfile.NewBSONReader(rdr)
	if err != nil {
		return errors.Wrap(err, "cannot open the oplog reader for restore")
	}

	di := &mgo.DialInfo{
		Addrs:    []string{fmt.Sprintf("%s:%s", msg.Host, msg.Port)},
		Username: c.connOpts.User,
		Password: c.connOpts.Password,
	}
	session, err := mgo.DialWithInfo(di)
	if err != nil {
		return errors.Wrap(err, "cannot connect to MongoDB to apply the oplog")
	}
	session.SetMode(mgo.Strong, true)
	// Replay the oplog
	oa, err := oplog.NewOplogApply(session, bsonReader)
	if err != nil {
		return errors.Wrap(err, "cannot instantiate the oplog applier")
	}

	if err := oa.Run(); err != nil {
		return errors.Wrap(err, "error while running the oplog applier")
	}

	return nil
}

func (c *Client) checkS3Access(opts storage.S3) (bool, bool, bool, error) {
	token := ""
	var valid, canRead, canWrite bool
	sess, err := session.NewSession(&aws.Config{
		Region:   aws.String(opts.Region),
		Endpoint: aws.String(opts.EndpointURL),
		Credentials: credentials.NewStaticCredentials(opts.Credentials.AccessKeyID,
			opts.Credentials.SecretAccessKey,
			token,
		),
		S3ForcePathStyle: aws.Bool(true),
	})

	if err != nil {
		return false, false, false, errors.Wrap(err, "Cannot create an AWS session")
	}
	svc := s3.New(sess)
	exists, err := awsutils.BucketExists(svc, opts.Bucket)
	if err != nil {
		return false, false, false, errors.Wrapf(err, "Cannot check if bucket %q exists", opts.Bucket)
	}

	if !exists {
		if err := awsutils.CreateBucket(svc, opts.Bucket); err != nil {
			return false, false, false, errors.Wrapf(err, "Unable to create bucket %q", opts.Bucket)
		}
		valid = true
		canRead = true
		canWrite = true
	} else {
		valid = true
		canRead = c.canListObjects(svc, opts.Bucket)
		canWrite = c.canPutObject(svc, opts.Bucket)
	}

	return valid, canRead, canWrite, nil
}

func (c *Client) checkFilesystemAccess(path string) (bool, bool, bool, error) {
	path = utils.Expand(path)
	fi, err := os.Stat(path)
	if err != nil {
		return false, false, false, err
	}
	if !fi.IsDir() {
		return false, false, false, fmt.Errorf("%s is not a directory", path)
	}
	var valid, canRead, canWrite bool
	valid = true

	_, err = filepath.Glob(filepath.Join(path, "*"))
	if err == nil {
		canRead = true
	}
	rand.Seed(time.Now().UnixNano())
	filename := fmt.Sprintf("temp_file_for_test_%6d", rand.Int63n(999999))
	fh, err := os.Create(filepath.Join(path, filename))
	if err == nil {
		canWrite = true
		fh.Close()
		os.Remove(filepath.Join(path, filename))
	}

	return valid, canRead, canWrite, nil
}

func (c *Client) canListObjects(svc *s3.S3, bucket string) bool {
	params := &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
	}

	_, err := svc.ListObjects(params)
	return err == nil
}

func (c *Client) canPutObject(svc *s3.S3, bucket string) bool {
	buffer := []byte("test can write to bucket")
	rand.Seed(time.Now().UnixNano())
	filename := fmt.Sprintf("temp_file_for_test_%6d", rand.Int63n(999999))
	_, err := svc.PutObject(&s3.PutObjectInput{
		Bucket:             aws.String(bucket),
		Key:                aws.String(filename),
		Body:               bytes.NewReader(buffer),
		ContentLength:      aws.Int64(int64(len(buffer))),
		ContentType:        aws.String(http.DetectContentType(buffer)),
		ContentDisposition: aws.String("attachment"),
	})
	if err != nil {
		return false
	}
	input := &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(filename),
	}
	if _, err := svc.DeleteObject(input); err != nil {
		c.logger.Printf("Cannot delete s3 can write test object %s in bucket %s: %s", filename, bucket, err)
	}

	return true
}
