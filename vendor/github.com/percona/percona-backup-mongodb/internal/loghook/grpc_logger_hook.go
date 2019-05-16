package loghook

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// GrpcLogging is a hook for Logrus logger, that forwards all the logs to the server
// so on the server we can see all the logs from all the clients
type GrpcLogging struct {
	clientID     string
	ctx          context.Context
	level        logrus.Level
	reconnecting bool
	stream       pb.Messages_LoggingClient
	lock         *sync.Mutex
	grpcConn     *grpc.ClientConn
	timeouts     []time.Duration // reconnection timeouts
}

func NewGrpcLogging(ctx context.Context, clientID string, grpcConn *grpc.ClientConn) (*GrpcLogging, error) {
	glog := &GrpcLogging{
		ctx:      ctx,
		clientID: clientID,
		grpcConn: grpcConn,
		level:    logrus.ErrorLevel,
		lock:     &sync.Mutex{},
		timeouts: []time.Duration{
			1 * time.Second,
			5 * time.Second,
			30 * time.Second,
			60 * time.Second,
		},
	}
	if err := glog.connect(); err != nil {
		return nil, err
	}
	return glog, nil
}

func (hook *GrpcLogging) connect() (err error) {
	grpcClient := pb.NewMessagesClient(hook.grpcConn)
	hook.stream, err = grpcClient.Logging(hook.ctx)
	if err != nil {
		return errors.Wrap(err, "cannot connect to the gRPC server")
	}
	return nil
}

func (hook *GrpcLogging) reconnect() {
	if hook.isReconnecting() {
		return
	}

	hook.setReconnecting(true)
	defer hook.setReconnecting(false)

	i := 0
	for {
		if err := hook.connect(); err == nil {
			return
		}
		if i < len(hook.timeouts) {
			time.Sleep(hook.timeouts[i])
		}
		if i+1 < len(hook.timeouts) {
			i++
		}
	}
}

func (hook *GrpcLogging) isReconnecting() bool {
	hook.lock.Lock()
	defer hook.lock.Unlock()
	return hook.reconnecting
}

func (hook *GrpcLogging) setReconnecting(status bool) {
	hook.lock.Lock()
	defer hook.lock.Unlock()
	hook.reconnecting = status
}

func (hook *GrpcLogging) Fire(entry *logrus.Entry) error {
	if hook.isReconnecting() {
		return fmt.Errorf("log gRPC is reconnecting to the server stream")
	}
	msg := &pb.LogEntry{
		ClientId: hook.clientID,
		Level:    uint32(entry.Level),
		Ts:       time.Now().UTC().Unix(),
		Message:  entry.Message,
	}

	if err := hook.stream.Send(msg); err != nil {
		fmt.Fprintf(os.Stderr, "Unable to send log to stream: %v", err)
		go hook.reconnect()
		return err
	}

	return nil
}

func (hook *GrpcLogging) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (hook *GrpcLogging) SetLevel(level logrus.Level) {
	hook.level = level
}
