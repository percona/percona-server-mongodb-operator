package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

type MongoClientProvider interface {
	Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error)
	Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error)
	Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool) (mongo.Client, error)
}

func (r *ReconcilePerconaServerMongoDB) MongoClientProvider() MongoClientProvider {
	if r.mongoClientProvider == nil {
		return &psmdb.MongoClientProvider{K8sClient: r.client}
	}
	return r.mongoClientProvider
}

func (r *ReconcilePerconaServerMongoDB) mongoClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole) (mongo.Client, error) {
	return r.MongoClientProvider().Mongo(ctx, cr, rs, role)
}

func (r *ReconcilePerconaServerMongoDB) mongosClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (mongo.Client, error) {
	return r.MongoClientProvider().Mongos(ctx, cr, role)
}

func (r *ReconcilePerconaServerMongoDB) standaloneClientWithRole(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, pod corev1.Pod) (mongo.Client, error) {
	host, err := psmdb.MongoHost(ctx, r.client, cr, cr.Spec.ClusterServiceDNSMode, rs, rs.Expose.Enabled, pod)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get mongo host")
	}
	return r.MongoClientProvider().Standalone(ctx, cr, role, host, cr.TLSEnabled())
}
