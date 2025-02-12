package psmdb

import (
	"context"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

type MongoClientProvider struct {
	K8sClient client.Client
}

func NewClientProvider(k8sclient client.Client) *MongoClientProvider {
	return &MongoClientProvider{k8sclient}
}

func (p *MongoClientProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error) {
	c, err := GetInternalCredentials(ctx, p.K8sClient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return MongoClient(ctx, p.K8sClient, cr, rs, c)
}

func (p *MongoClientProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error) {
	c, err := GetInternalCredentials(ctx, p.K8sClient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return MongosClient(ctx, p.K8sClient, cr, c)
}

func (p *MongoClientProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool) (mongo.Client, error) {
	c, err := GetInternalCredentials(ctx, p.K8sClient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return StandaloneClient(ctx, p.K8sClient, cr, c, host, tlsEnabled)
}
