package perconaservermongodb

import (
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

func (r *ReconcilePerconaServerMongoDB) mongoClientWithRole(cr *api.PerconaServerMongoDB, rs api.ReplsetSpec,
	role UserRole) (*mgo.Client, error) {

	c, err := r.getInternalCredentials(cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return psmdb.MongoClient(r.client, cr, rs, c)
}

func (r *ReconcilePerconaServerMongoDB) mongosClientWithRole(cr *api.PerconaServerMongoDB, role UserRole) (*mgo.Client, error) {
	c, err := r.getInternalCredentials(cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return psmdb.MongosClient(r.client, cr, c)
}
