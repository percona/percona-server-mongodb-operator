package perconaservermongodbrestore

import (
	"context"

	pbapi "github.com/percona/percona-backup-mongodb/proto/api"
	psmdbv1alpha1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"google.golang.org/grpc"
)

// newRestoreHandler return new RestoreHandler
func newRestoreHandler(cluster string) (RestoreHandler, error) {
	r := RestoreHandler{}
	grpcOps := grpc.WithInsecure()
	conn, err := grpc.Dial(cluster+backup.GetCoordinatorSuffix()+":10001", grpcOps)
	if err != nil {
		return r, err
	}
	client := pbapi.NewApiClient(conn)

	r = RestoreHandler{
		client: client,
	}

	return r, nil
}

// RestoreHandler is for working with backup coordinator
type RestoreHandler struct {
	client pbapi.ApiClient
}

// StartRestore is for starting new restore
func (r *RestoreHandler) StartRestore(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) error {
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
