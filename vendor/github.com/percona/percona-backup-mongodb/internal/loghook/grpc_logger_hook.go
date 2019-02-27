package loghook

import (
	"context"
	"fmt"
	"os"
	"time"

	pb "github.com/percona/percona-backup-mongodb/proto/messages"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

type GrpcLogging struct {
	clientID string
	ctx      context.Context
	grpcConn *grpc.ClientConn
	level    logrus.Level
	stream   pb.Messages_LoggingClient
}

func NewGrpcLogging(ctx context.Context, clientID string, grpcConn *grpc.ClientConn) (*GrpcLogging, error) {
	glog := &GrpcLogging{
		ctx:      ctx,
		clientID: clientID,
		grpcConn: grpcConn,
		level:    logrus.ErrorLevel,
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

func (hook *GrpcLogging) Fire(entry *logrus.Entry) error {
	msg := &pb.LogEntry{
		ClientId: hook.clientID,
		Level:    uint32(entry.Level),
		Ts:       time.Now().UTC().Unix(),
		Message:  entry.Message,
	}

	if err := hook.stream.Send(msg); err != nil {
		fmt.Fprintf(os.Stderr, "Unable to send log to stream: %v", err)
	}

	return nil
}

func (hook *GrpcLogging) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (hook *GrpcLogging) SetLevel(level logrus.Level) {
	hook.level = level
}
