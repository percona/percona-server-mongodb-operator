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

func validFCVUpgrade(current, new string) (bool, error) {
	if current == "" {
		return true, nil
	}

	if new == "" {
		return false, errors.New("empty new FCV")
	}

	cursv, err := toGoSemver(current)
	if err != nil {
		return false, errors.Wrap(err, "failed to get current semver")
	}

	newsv, err := toGoSemver(new)
	if err != nil {
		return false, errors.Wrap(err, "failed to get new semver")
	}

	return semver.Compare(cursv, newsv) == 0, nil
}

func (r *ReconcilePerconaServerMongoDB) getFCV(cr *api.PerconaServerMongoDB, replset api.ReplsetSpec) (string, error) {
	c, err := r.mongoClientWithRole(cr, replset, roleClusterAdmin)
	if err != nil {
		return "", errors.Wrap(err, "failed to get connection")
	}

	return mongo.GetFCV(context.TODO(), c)

}

func (r *ReconcilePerconaServerMongoDB) setFCV(cr *api.PerconaServerMongoDB, version string, replset api.ReplsetSpec) error {
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
		c, err := r.mongoClientWithRole(cr, replset, roleClusterAdmin)
		if err != nil {
			return errors.Wrap(err, "failed to get connection")
		}

		cli = c
	}

	return mongo.SetFCV(context.TODO(), cli, v)
}
