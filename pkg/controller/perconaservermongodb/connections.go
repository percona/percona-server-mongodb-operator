package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

func (r *ReconcilePerconaServerMongoDB) mongoClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs api.ReplsetSpec,
	role UserRole) (*mgo.Client, error) {

	c, err := r.getInternalCredentials(ctx, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return psmdb.MongoClient(ctx, r.client, cr, rs, c)
}

func (r *ReconcilePerconaServerMongoDB) mongosClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, role UserRole) (*mgo.Client, error) {
	c, err := r.getInternalCredentials(ctx, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return psmdb.MongosClient(ctx, r.client, cr, c)
}
