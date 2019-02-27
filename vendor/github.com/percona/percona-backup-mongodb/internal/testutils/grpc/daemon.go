package grpc

import (
	"context"
	"fmt"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/percona/percona-backup-mongodb/grpc/api"
	"github.com/percona/percona-backup-mongodb/grpc/client"
	"github.com/percona/percona-backup-mongodb/grpc/server"
	"github.com/percona/percona-backup-mongodb/internal/testutils"
	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/percona/percona-backup-mongodb/storage"
	"github.com/prometheus/common/log"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

const (
	TestGrpcMessagesPort = "10000"
	TestGrpcAPIPort      = "10001"
)

var grpcServerShutdownTimeout = 30

type Daemon struct {
	grpcServer4Api     *grpc.Server
	grpcServer4Clients *grpc.Server
	MessagesServer     *server.MessagesServer
	APIServer          *api.ApiServer
	msgListener        net.Listener
	apiListener        net.Listener
	wg                 *sync.WaitGroup
	ctx                context.Context
	cancelFunc         context.CancelFunc
	logger             *logrus.Logger
	lock               *sync.Mutex
	clients            []*client.Client
	workDir            string
	clientConn         *grpc.ClientConn
	storages           *storage.Storages
}

type PortRs struct {
	Port string
	Rs   string
}

func NewDaemon(ctx context.Context, workDir string, storages *storage.Storages, t *testing.T, logger *logrus.Logger) (*Daemon, error) {
	if logger == nil {
		logger = &logrus.Logger{
			Out: os.Stderr,
			Formatter: &logrus.TextFormatter{
				FullTimestamp:          true,
				DisableLevelTruncation: true,
			},
			Hooks: make(logrus.LevelHooks),
			Level: logrus.DebugLevel,
		}
		logger.SetLevel(logrus.StandardLogger().Level)
		logger.Out = logrus.StandardLogger().Out
	}
	var opts []grpc.ServerOption
	d := &Daemon{
		clients:  make([]*client.Client, 0),
		wg:       &sync.WaitGroup{},
		logger:   logger,
		lock:     &sync.Mutex{},
		workDir:  workDir,
		storages: storages,
	}
	var err error

	// Start the grpc server
	d.msgListener, err = net.Listen("tcp", fmt.Sprintf("localhost:%s", TestGrpcMessagesPort))
	if err != nil {
		return nil, fmt.Errorf("cannot listen on port %s for the gRPC messages server, %s", TestGrpcMessagesPort, err)
	}

	d.ctx, d.cancelFunc = context.WithCancel(ctx)
	// This is the sever/agents gRPC server
	d.grpcServer4Clients = grpc.NewServer(opts...)
	d.MessagesServer = server.NewMessagesServer(workDir, 60, logger)
	pb.RegisterMessagesServer(d.grpcServer4Clients, d.MessagesServer)

	d.wg.Add(1)
	logger.Printf("Starting agents gRPC server. Listening on %s", d.msgListener.Addr().String())
	d.runAgentsGRPCServer(d.ctx, d.grpcServer4Clients, d.msgListener, grpcServerShutdownTimeout, d.wg)

	//
	d.apiListener, err = net.Listen("tcp", fmt.Sprintf("localhost:%s", TestGrpcAPIPort))
	if err != nil {
		return nil, fmt.Errorf("cannot listen on port %s for the gRPC API server, %s", TestGrpcAPIPort, err)
	}

	// This is the server gRPC API
	d.grpcServer4Api = grpc.NewServer(opts...)
	d.APIServer = api.NewApiServer(d.MessagesServer)
	pbapi.RegisterApiServer(d.grpcServer4Api, d.APIServer)

	d.wg.Add(1)
	logger.Printf("Starting API gRPC server. Listening on %s", d.apiListener.Addr().String())
	d.runAgentsGRPCServer(d.ctx, d.grpcServer4Api, d.apiListener, grpcServerShutdownTimeout, d.wg)

	clientOpts := []grpc.DialOption{grpc.WithInsecure()}

	clientServerAddr := fmt.Sprintf("127.0.0.1:%s", TestGrpcMessagesPort)
	d.clientConn, err = grpc.Dial(clientServerAddr, clientOpts...)
	if err != nil {
		return nil, fmt.Errorf("cannot dail gRPC address %s: %v", clientServerAddr, err)
	}

	return d, nil
}

func (d *Daemon) StartAgents(portRsList []PortRs) error {
	for _, portRs := range portRsList {
		di, err := testutils.DialInfoForPort(portRs.Rs, portRs.Port)
		if err != nil {
			return err
		}

		dbConnOpts := client.ConnectionOptions{
			Host:           testutils.MongoDBHost,
			Port:           portRs.Port,
			User:           di.Username,
			Password:       di.Password,
			ReplicasetName: di.ReplicaSetName,
		}

		input := client.InputOptions{
			BackupDir:     d.workDir,
			DbConnOptions: dbConnOpts,
			//DbSSLOptions  SSLOptions
			GrpcConn: d.clientConn,
			Logger:   d.logger,
			Storages: d.storages,
		}
		c, err := client.NewClient(d.ctx, input)
		if err != nil {
			return fmt.Errorf("Cannot create an agent instance %s:%s: %s", dbConnOpts.Host, dbConnOpts.Port, err)
		}
		if err := c.Start(); err != nil {
			return fmt.Errorf("Cannot start agent instance %s:%s: %s", dbConnOpts.Host, dbConnOpts.Port, err)
		}

		d.clients = append(d.clients, c)

	}
	return nil
}

func (d *Daemon) StartAllAgents() error {
	portRsList := []PortRs{
		{Port: testutils.MongoDBShard1PrimaryPort, Rs: testutils.MongoDBShard1ReplsetName},
		{Port: testutils.MongoDBShard1Secondary1Port, Rs: testutils.MongoDBShard1ReplsetName},
		{Port: testutils.MongoDBShard1Secondary2Port, Rs: testutils.MongoDBShard1ReplsetName},

		{Port: testutils.MongoDBShard2PrimaryPort, Rs: testutils.MongoDBShard2ReplsetName},
		{Port: testutils.MongoDBShard2Secondary1Port, Rs: testutils.MongoDBShard2ReplsetName},
		{Port: testutils.MongoDBShard2Secondary2Port, Rs: testutils.MongoDBShard2ReplsetName},

		{Port: testutils.MongoDBConfigsvr1Port, Rs: testutils.MongoDBConfigsvrReplsetName},

		{Port: testutils.MongoDBMongosPort, Rs: ""},
	}
	return d.StartAgents(portRsList)
}

func (d *Daemon) APIClient() *grpc.Server {
	return d.grpcServer4Api
}

func (d *Daemon) MessagesClient() *grpc.Server {
	return d.grpcServer4Clients
}

func (d *Daemon) Stop() {
	d.lock.Lock()
	defer d.lock.Unlock()

	for _, client := range d.clients {
		if err := client.Stop(); err != nil {
			log.Errorf("Cannot stop client %s: %s", client.ID(), err)
		}
	}
	d.MessagesServer.Stop()
	d.cancelFunc()
	d.wg.Wait()
	d.msgListener.Close()
	d.apiListener.Close()
}

func (d *Daemon) ClientsCount() int {
	d.lock.Lock()
	defer d.lock.Unlock()
	return len(d.clients)
}

func (d *Daemon) Clients() []*client.Client {
	return d.clients
}

func (d *Daemon) runAgentsGRPCServer(ctx context.Context, grpcServer *grpc.Server, lis net.Listener,
	shutdownTimeout int, wg *sync.WaitGroup) {
	go func() {
		err := grpcServer.Serve(lis)
		if err != nil {
			d.logger.Printf("Cannot start agents gRPC server: %s", err)
		}
		d.logger.Println("Stopping server " + lis.Addr().String())
		wg.Done()
		fmt.Printf("Stopped server at %s\n", lis.Addr().String())
	}()

	go func() {
		<-ctx.Done()
		d.logger.Printf("Gracefully stopping server at %s", lis.Addr().String())
		// Try to Gracefully stop the gRPC server.
		c := make(chan struct{})
		go func() {
			grpcServer.GracefulStop()
			c <- struct{}{}
		}()

		// If after shutdownTimeout seconds the server hasn't stop, just kill it.
		select {
		case <-c:
			return
		case <-time.After(time.Duration(shutdownTimeout) * time.Second):
			grpcServer.Stop()
		}
	}()

	time.Sleep(2 * time.Second)
}
