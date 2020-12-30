package perconaservermongodb

import (
	"strconv"
	"strings"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
)

func (r *ReconcilePerconaServerMongoDB) mongosConnection(cr *api.PerconaServerMongoDB, user, pass string) (*mgo.Client, error) {
	conf := mongo.Config{
		Hosts: []string{strings.Join([]string{cr.Name + "-mongos", cr.Namespace, cr.Spec.ClusterServiceDNSSuffix}, ".") +
			":" + strconv.Itoa(int(cr.Spec.Sharding.Mongos.Port))},
		Username: user,
		Password: pass,
	}

	mongosSession, err := mongo.Dial(&conf)
	if err != nil {
		return nil, errors.Wrap(err, "failed to dial to mongos")
	}

	return mongosSession, nil
}
