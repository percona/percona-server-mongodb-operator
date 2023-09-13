package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

type MongoClientProvider interface {
	Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs api.ReplsetSpec, role api.UserRole) (mongo.Client, error)
	Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole) (mongo.Client, error)
	Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole, host string) (mongo.Client, error)
}

func (r *ReconcilePerconaServerMongoDB) MongoClientProvider() MongoClientProvider {
	if r.mongoClientProvider == nil {
		return &mongoClientProvider{r.client}
	}
	return r.mongoClientProvider
}

type mongoClientProvider struct {
	k8sclient client.Client
}

func (p *mongoClientProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs api.ReplsetSpec, role api.UserRole) (mongo.Client, error) {
	c, err := getInternalCredentials(ctx, p.k8sclient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return psmdb.MongoClient(ctx, p.k8sclient, cr, rs, c)
}

func (p *mongoClientProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole) (mongo.Client, error) {
	c, err := getInternalCredentials(ctx, p.k8sclient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return psmdb.MongosClient(ctx, p.k8sclient, cr, c)
}

func (p *mongoClientProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole, host string) (mongo.Client, error) {
	c, err := getInternalCredentials(ctx, p.k8sclient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return psmdb.StandaloneClient(ctx, p.k8sclient, cr, c, host)
}

func (r *ReconcilePerconaServerMongoDB) mongoClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs api.ReplsetSpec, role api.UserRole) (mongo.Client, error) {
	return r.MongoClientProvider().Mongo(ctx, cr, rs, role)
}

func (r *ReconcilePerconaServerMongoDB) mongosClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole) (mongo.Client, error) {
	return r.MongoClientProvider().Mongos(ctx, cr, role)
}

func (r *ReconcilePerconaServerMongoDB) standaloneClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole, host string) (mongo.Client, error) {
	return r.MongoClientProvider().Standalone(ctx, cr, role, host)
}
