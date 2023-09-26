package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func (r *ReconcilePerconaServerMongoDB) mongoClientWithRoleExec(ctx context.Context, cr *api.PerconaServerMongoDB, rs api.ReplsetSpec, role api.UserRole) (mongo.Client, error) {
	c, err := getInternalCredentials(ctx, r.client, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return mongo.NewMongoClientExec(r.client, &rs, cr, c.Username, c.Password)
}

func (r *ReconcilePerconaServerMongoDB) mongosClientWithRoleExec(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole) (mongo.Client, error) {
	c, err := getInternalCredentials(ctx, r.client, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return mongo.NewMongosClientExec(r.client, cr, c.Username, c.Password)
}

func (r *ReconcilePerconaServerMongoDB) standaloneClientWithRoleiExec(ctx context.Context, cr *api.PerconaServerMongoDB, role api.UserRole, host string) (mongo.Client, error) {
	c, err := getInternalCredentials(ctx, r.client, cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return mongo.NewStandaloneClientExec(r.client, cr, host, c.Username, c.Password)
}
