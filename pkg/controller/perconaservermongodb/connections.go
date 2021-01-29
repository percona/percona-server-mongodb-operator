package perconaservermongodb

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"strconv"
	"strings"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	mgo "go.mongodb.org/mongo-driver/mongo"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (r *ReconcilePerconaServerMongoDB) mongoClientWithRole(cr *api.PerconaServerMongoDB, rsName string,
	rsExposed bool, pods corev1.PodList, role UserRole) (*mgo.Client, error) {

	c, err := r.getInternalCredentials(cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return r.mongoClient(cr, rsName, rsExposed, pods, c)
}

func (r *ReconcilePerconaServerMongoDB) mongoClient(cr *api.PerconaServerMongoDB, rsName string, rsExposed bool, pods corev1.PodList,
	c Credentials) (*mgo.Client, error) {
	rsAddrs, err := psmdb.GetReplsetAddrs(r.client, cr, rsName, rsExposed, pods.Items)
	if err != nil {
		return nil, errors.Wrap(err, "get replset addr")
	}

	conf := &mongo.Config{
		ReplSetName: rsName,
		Hosts:       rsAddrs,
		Username:    c.Username,
		Password:    c.Password,
	}

	if !cr.Spec.UnsafeConf {
		certSecret := &corev1.Secret{}
		err := r.client.Get(context.TODO(), types.NamespacedName{
			Name:      cr.Spec.Secrets.SSL,
			Namespace: cr.Namespace,
		}, certSecret)
		if err != nil {
			return nil, errors.Wrap(err, "get ssl certSecret")
		}
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(certSecret.Data["ca.crt"])

		var clientCerts []tls.Certificate
		cert, err := tls.X509KeyPair(certSecret.Data["tls.crt"], certSecret.Data["tls.key"])
		if err != nil {
			return nil, errors.Wrap(err, "load keypair")
		}
		clientCerts = append(clientCerts, cert)
		conf.TLSConf = &tls.Config{
			InsecureSkipVerify: true,
			RootCAs:            pool,
			Certificates:       clientCerts,
		}
	}

	return mongo.Dial(conf)
}

func (r *ReconcilePerconaServerMongoDB) mongosClientWithRole(cr *api.PerconaServerMongoDB, role UserRole) (*mgo.Client, error) {
	c, err := r.getInternalCredentials(cr, role)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get credentials")
	}

	return mongosClient(cr, c)
}

func mongosClient(cr *api.PerconaServerMongoDB, c Credentials) (*mgo.Client, error) {
	conf := mongo.Config{
		Hosts: []string{strings.Join([]string{cr.Name + "-mongos", cr.Namespace, cr.Spec.ClusterServiceDNSSuffix}, ".") +
			":" + strconv.Itoa(int(cr.Spec.Sharding.Mongos.Port))},
		Username: c.Username,
		Password: c.Password,
	}

	return mongo.Dial(&conf)
}
