package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

type MongoClientProvider interface {
	Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error)
	Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error)
	Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool, opts ...mongo.ConfigOption) (mongo.Client, error)
}

func (r *ReconcilePerconaServerMongoDB) getMongoClientProvider() MongoClientProvider {
	if r.mongoClientProvider == nil {
		return &mongoClientProvider{k8sclient: r.client, apiReader: r.apiReader}
	}
	return r.mongoClientProvider
}

type mongoClientProvider struct {
	k8sclient client.Client
	apiReader client.Reader
}

type connectFunc func(ctx context.Context, creds psmdb.Credentials) (mongo.Client, error)

// connect tries cached credentials first. On authentication failure, it reads the secret directly and retries.
func (p *mongoClientProvider) connect(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, f connectFunc) (mongo.Client, error) {
	cached, err := getInternalCredentials(ctx, p.k8sclient, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	conn, err := f(ctx, cached)
	if mongo.IsAuthenticationError(err) {
		fresh, refreshErr := getInternalCredentials(ctx, p.apiReader, cr, role)
		if refreshErr != nil {
			return nil, errors.Wrap(refreshErr, "refresh credentials after failed authentication")
		}
		if fresh == cached {
			return nil, err
		}

		return f(ctx, fresh)
	}

	return conn, err
}

func (p *mongoClientProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error) {
	f := func(ctx context.Context, creds psmdb.Credentials) (mongo.Client, error) {
		return psmdb.MongoClient(ctx, p.k8sclient, cr, rs, creds, opts...)
	}

	return p.connect(ctx, cr, role, f)
}

func (p *mongoClientProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error) {
	f := func(ctx context.Context, creds psmdb.Credentials) (mongo.Client, error) {
		return psmdb.MongosClient(ctx, p.k8sclient, cr, creds, opts...)
	}

	return p.connect(ctx, cr, role, f)
}

func (p *mongoClientProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool, opts ...mongo.ConfigOption) (mongo.Client, error) {
	f := func(ctx context.Context, creds psmdb.Credentials) (mongo.Client, error) {
		return psmdb.StandaloneClient(ctx, p.k8sclient, cr, creds, host, tlsEnabled, opts...)
	}

	return p.connect(ctx, cr, role, f)
}

func (r *ReconcilePerconaServerMongoDB) mongoClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error) {
	return r.getMongoClientProvider().Mongo(ctx, cr, rs, role, opts...)
}

func (r *ReconcilePerconaServerMongoDB) mongosClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error) {
	return r.getMongoClientProvider().Mongos(ctx, cr, role, opts...)
}

func (r *ReconcilePerconaServerMongoDB) standaloneClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, pod corev1.Pod, opts ...mongo.ConfigOption) (mongo.Client, error) {
	host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, rs, rs.Expose.Enabled, pod)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get mongo host")
	}
	return r.getMongoClientProvider().Standalone(ctx, cr, role, host, cr.TLSEnabled(), opts...)
}
