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
	Username   string
	Password   string
	AuthSource string
}

func MongoClient(ctx context.Context, k8sClient client.Client, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, c Credentials) (mongo.Client, error) {
	conf, err := MongoConfig(ctx, k8sClient, cr, rs, c, false)
	if err != nil {
		return nil, errors.Wrap(err, "mongo config")
	}

	return mongo.Dial(ctx, conf)
}

func MongoConfig(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, c Credentials, rsExposed bool) (*mongo.Config, error) {
	pods, err := GetRSPods(ctx, cl, cr, rs.Name)
	if err != nil {
		return nil, errors.Wrapf(err, "get pods list for replset %s", rs.Name)
	}

	// `GetRSPods` returns truncated list of pods.
	// If `rs.Size` is 0 or replicaset doesn't exist in the cr the list of pods will be empty.
	// If there is empty pod list we should use `GetOutdatedRSPods` which returns list of pods without truncating it.
	if len(pods.Items) == 0 {
		pods, err = GetOutdatedRSPods(ctx, cl, cr, rs.Name)
		if err != nil {
			return nil, errors.Wrapf(err, "get outdated pods list for replset %s", rs.Name)
		}
	}

	rsAddrs, err := GetReplsetAddrs(ctx, cl, cr, cr.Spec.ClusterServiceDNSMode, rs, rsExposed, pods.Items)
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
		AuthSource:  c.AuthSource,
	}

	if cr.TLSEnabled() {
		tlsCfg, err := tls.Config(ctx, cl, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}

	return conf, nil
}

func MongosClient(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, c Credentials) (mongo.Client, error) {
	conf, err := MongosConfig(ctx, k8sclient, cr, c, true, cr.Spec.Sharding.Mongos.Expose.ServicePerPod)
	if err != nil {
		return nil, errors.Wrap(err, "get mongos config")
	}
	return mongo.Dial(ctx, conf)
}

func MongosConfig(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, c Credentials, useInternalAddr, servicePerPod bool) (*mongo.Config, error) {
	hosts, err := GetMongosAddrs(ctx, cl, cr, useInternalAddr, servicePerPod)
	if err != nil {
		return nil, errors.Wrap(err, "get mongos addrs")
	}
	conf := mongo.Config{
		Hosts:      hosts,
		Username:   c.Username,
		Password:   c.Password,
		AuthSource: c.AuthSource,
	}

	if cr.TLSEnabled() {
		tlsCfg, err := tls.Config(ctx, cl, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}
	return &conf, nil
}

func StandaloneClient(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB, c Credentials, host string, tlsEnabled bool) (mongo.Client, error) {
	conf := mongo.Config{
		Hosts:      []string{host},
		Username:   c.Username,
		Password:   c.Password,
		AuthSource: c.AuthSource,
		Direct:     true,
	}

	if tlsEnabled {
		tlsCfg, err := tls.Config(ctx, k8sclient, cr)
		if err != nil {
			return nil, errors.Wrap(err, "failed to get TLS config")
		}

		conf.TLSConf = &tlsCfg
	}

	return mongo.Dial(ctx, &conf)
}
