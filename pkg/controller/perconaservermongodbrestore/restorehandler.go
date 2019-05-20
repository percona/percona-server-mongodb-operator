package perconaservermongodbrestore

import (
	"context"

	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// newRestoreHandler return new RestoreHandler
func newRestoreHandler(cluster string) (RestoreHandler, error) {
	r := RestoreHandler{}
	grpcOpts := []grpc.DialOption{
		grpc.WithUnaryInterceptor(makeUnaryInterceptor("")),
		grpc.WithStreamInterceptor(makeStreamInterceptor("")),
		grpc.WithInsecure(),
	}
	conn, err := grpc.Dial(cluster+backup.GetCoordinatorSuffix()+":10001", grpcOpts...)
	if err != nil {
		return r, err
	}

	client := pbapi.NewApiClient(conn)

	r = RestoreHandler{
		client: client,
	}

	return r, nil
}

func makeUnaryInterceptor(token string) func(ctx context.Context, method string, req interface{}, reply interface{},
	cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
	return func(ctx context.Context, method string, req interface{}, reply interface{},
		cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		md := metadata.Pairs("authorization", "bearer "+token)
		ctx = metadata.NewOutgoingContext(ctx, md)

		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

func makeStreamInterceptor(token string) func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn,
	method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {

	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string,
		streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		md := metadata.Pairs("authorization", "bearer "+token)
		ctx = metadata.NewOutgoingContext(ctx, md)
		return streamer(ctx, desc, cc, method, opts...)
	}
}

// RestoreHandler is for working with backup coordinator
type RestoreHandler struct {
	client pbapi.ApiClient
}

// StartRestore is for starting new restore
func (r *RestoreHandler) StartRestore(cr *psmdbv1.PerconaServerMongoDBBackup) error {
	msg := &pbapi.RunRestoreParams{
		StorageName:  cr.Spec.StorageName,
		MetadataFile: cr.Status.Destination,
	}
	_, err := r.client.RunRestore(context.Background(), msg)
	if err != nil {
		return err
	}
	return nil
}
