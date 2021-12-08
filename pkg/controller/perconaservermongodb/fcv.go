package perconaservermongodb

import (
	"context"

	v "github.com/hashicorp/go-version"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func (r *ReconcilePerconaServerMongoDB) getFCV(cr *api.PerconaServerMongoDB) (string, error) {
	c, err := r.mongoClientWithRole(cr, *cr.Spec.Replsets[0], roleClusterAdmin)
	if err != nil {
		return "", errors.Wrap(err, "failed to get connection")
	}
	defer c.Disconnect(context.Background())

	return mongo.GetFCV(context.TODO(), c)
}

func (r *ReconcilePerconaServerMongoDB) setFCV(cr *api.PerconaServerMongoDB, version string) error {
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
		cli, connErr = r.mongosClientWithRole(cr, roleClusterAdmin)
	} else {
		cli, connErr = r.mongoClientWithRole(cr, *cr.Spec.Replsets[0], roleClusterAdmin)
	}
	defer cli.Disconnect(context.Background())

	if connErr != nil {
		return errors.Wrap(connErr, "failed to get connection")
	}

	return mongo.SetFCV(context.TODO(), cli, MajorMinor(v))
}
