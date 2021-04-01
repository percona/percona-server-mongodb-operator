package perconaservermongodb

import (
	"context"
	"strings"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/mod/semver"
)

func (r *ReconcilePerconaServerMongoDB) getFCV(cr *api.PerconaServerMongoDB) (string, error) {
	c, err := r.mongoClientWithRole(cr, *cr.Spec.Replsets[0], roleClusterAdmin)
	if err != nil {
		return "", errors.Wrap(err, "failed to get connection")
	}

	return mongo.GetFCV(context.TODO(), c)

}

func (r *ReconcilePerconaServerMongoDB) setFCV(cr *api.PerconaServerMongoDB, version string) error {
	if len(version) == 0 {
		return errors.New("empty version")
	}

	v, err := toGoSemver(version)
	if err != nil {
		return errors.Wrap(err, "failed to get go semver")
	}

	v = strings.TrimPrefix(semver.MajorMinor(v), "v")

	var cli *mgo.Client

	if cr.Spec.Sharding.Enabled {
		c, err := r.mongosClientWithRole(cr, roleClusterAdmin)
		if err != nil {
			return errors.Wrap(err, "failed to get connection")
		}

		cli = c
	} else {
		c, err := r.mongoClientWithRole(cr, *cr.Spec.Replsets[0], roleClusterAdmin)
		if err != nil {
			return errors.Wrap(err, "failed to get connection")
		}

		cli = c
	}

	return mongo.SetFCV(context.TODO(), cli, v)
}
