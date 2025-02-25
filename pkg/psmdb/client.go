package psmdb

import (
	"context"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
)

type Credentials struct {
	Username string
	Password string
}

func MongoClient(ctx context.Context, k8sClient client.Client, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, c Credentials) (mongo.Client, error) {
	pods, err := GetRSPods(ctx, k8sClient, cr, rs.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	// `GetRSPods` returns truncated list of pods.
	// If `rs.Size` is 0 or replicaset doesn't exist in the cr the list of pods will be empty.
	// If there is empty pod list we should use `GetOutdatedRSPods` which returns list of pods without truncating it.
	if len(pods.Items) == 0 {
		pods, err = GetOutdatedRSPods(ctx, k8sClient, cr, rs.Name)
		if err != nil {
			return nil, errors.Wrapf(err, "get outdated pods list for replset %s", rs.Name)
		}
	}

	rsAddrs, err := GetReplsetAddrs(ctx, k8sClient, cr, cr.Spec.ClusterServiceDNSMode, rs, false, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get replset addr")
	}

	rsName := rs.Name
	name, err := rs.CustomReplsetName()
	if err == nil {
		rsName = name
	}

	conf := &mongo.Config{
		ReplSetName: rsName,
		Hosts:       rsAddrs,
		Username:    c.Username,
		Password:    c.Password,
	}

	if cr.TLSEnabled() {
		tlsCfg, err := tls.Config(ctx, k8sClient, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}

	return mongo.Dial(conf)
}

func MongosClient(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, c Credentials) (mongo.Client, error) {
	hosts, err := GetMongosAddrs(ctx, k8sclient, cr, true)
	if err != nil {
		return nil, errors.Wrap(err, "get mongos addrs")
	}
	conf := mongo.Config{
		Hosts:    hosts,
		Username: c.Username,
		Password: c.Password,
	}

	if cr.TLSEnabled() {
		tlsCfg, err := tls.Config(ctx, k8sclient, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}

	return mongo.Dial(&conf)
}

func StandaloneClient(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, c Credentials, host string, tlsEnabled bool) (mongo.Client, error) {
	conf := mongo.Config{
		Hosts:    []string{host},
		Username: c.Username,
		Password: c.Password,
		Direct:   true,
	}

	if tlsEnabled {
		tlsCfg, err := tls.Config(ctx, k8sclient, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}

	return mongo.Dial(&conf)
}
