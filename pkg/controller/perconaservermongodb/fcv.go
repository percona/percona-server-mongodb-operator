package perconaservermongodb

import (
	"context"

	v "github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func (r *ReconcilePerconaServerMongoDB) getFCV(ctx context.Context, cr *api.PerconaServerMongoDB) (string, error) {
	c, err := r.mongoClientWithRole(ctx, cr, *cr.Spec.Replsets[0], roleClusterAdmin)
	if err != nil {
		return "", errors.Wrap(err, "failed to get connection")
	}

	defer func() {
		if err := c.Disconnect(ctx); err != nil {
			log.Error(err, "close client connection")
		}
	}()

	return mongo.GetFCV(ctx, c)
}

func (r *ReconcilePerconaServerMongoDB) setFCV(ctx context.Context, cr *api.PerconaServerMongoDB, version string) error {
	if len(version) == 0 {
		return errors.New("empty version")
	}

	v, err := v.NewSemver(version)
	if err != nil {
		return errors.Wrap(err, "failed to get go semver")
	}

	var cli *mgo.Client
	var connErr error

	if cr.Spec.Sharding.Enabled {
		cli, connErr = r.mongosClientWithRole(ctx, cr, roleClusterAdmin)
	} else {
		cli, connErr = r.mongoClientWithRole(ctx, cr, *cr.Spec.Replsets[0], roleClusterAdmin)
	}

	if connErr != nil {
		return errors.Wrap(connErr, "failed to get connection")
	}

	defer func() {
		if err := cli.Disconnect(ctx); err != nil {
			log.Error(err, "close client connection")
		}
	}()

	return mongo.SetFCV(ctx, cli, MajorMinor(v))
}
