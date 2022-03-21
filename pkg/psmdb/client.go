package psmdb

import (
	"context"

	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
)

type Credentials struct {
	Username string
	Password string
}

func MongoClient(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, rs api.ReplsetSpec, c Credentials) (*mgo.Client, error) {
	pods, err := GetRSPods(ctx, k8sclient, cr, rs.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	rsAddrs, err := GetReplsetAddrs(ctx, k8sclient, cr, rs.Name, rs.Expose.Enabled, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get replset addr")
	}

	conf := &mongo.Config{
		ReplSetName: rs.Name,
		Hosts:       rsAddrs,
		Username:    c.Username,
		Password:    c.Password,
	}

	if !cr.Spec.UnsafeConf {
		tlsCfg, err := tls.Config(ctx, k8sclient, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}

	return mongo.Dial(conf)
}

func MongosClient(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, c Credentials) (*mgo.Client, error) {
	hosts, err := GetMongosAddrs(ctx, k8sclient, cr)
	if err != nil {
		return nil, errors.Wrap(err, "get mongos addrs")
	}
	conf := mongo.Config{
		Hosts:    hosts,
		Username: c.Username,
		Password: c.Password,
	}

	if !cr.Spec.UnsafeConf {
		tlsCfg, err := tls.Config(ctx, k8sclient, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}

	return mongo.Dial(&conf)
}
