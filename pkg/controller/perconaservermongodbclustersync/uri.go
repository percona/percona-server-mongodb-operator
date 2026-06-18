package perconaservermongodbclustersync

import (
	"context"
	"fmt"
	"net/url"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func buildSourceURI(ctx context.Context, cl client.Client, cr *psmdbv1.PerconaServerMongoDBClusterSync) (string, error) {
	nn := types.NamespacedName{Name: cr.Spec.Source.CredentialsSecret, Namespace: cr.Namespace}
	secret := &corev1.Secret{}
	if err := cl.Get(ctx, nn, secret); err != nil {
		return "", errors.Wrapf(err, "get source credentials secret %s", nn)
	}
	username := string(secret.Data["username"])
	password := string(secret.Data["password"])
	if username == "" || password == "" {
		return "", errors.Errorf("source credentials secret %s missing username/password keys", nn)
	}

	u, err := url.Parse(cr.Spec.Source.URI)
	if err != nil {
		return "", errors.Wrapf(err, "parse source uri %q", cr.Spec.Source.URI)
	}
	u.User = url.UserPassword(username, password)
	return u.String(), nil
}

func buildTargetURI(target *psmdbv1.PerconaServerMongoDB, username, password string) (string, error) {
	if username == "" || password == "" {
		return "", errors.New("syncTargetUser credentials are empty")
	}

	dnsSuffix := target.Spec.ClusterServiceDNSSuffix
	if dnsSuffix == "" {
		dnsSuffix = psmdbv1.DefaultDNSSuffix
	}

	var host, rsName string
	if target.Spec.Sharding.Enabled {
		port := target.Spec.Sharding.Mongos.GetPort()
		host = fmt.Sprintf("%s-mongos.%s.%s:%d", target.Name, target.Namespace, dnsSuffix, port)
	} else {
		if len(target.Spec.Replsets) == 0 {
			return "", errors.Errorf("target cluster %s has no replsets", target.Name)
		}
		rs := target.Spec.Replsets[0]
		rsName = rs.Name
		host = fmt.Sprintf("%s-%s.%s.%s:%d", target.Name, rs.Name, target.Namespace, dnsSuffix, rs.GetPort())
	}

	u := &url.URL{
		Scheme: "mongodb",
		User:   url.UserPassword(username, password),
		Host:   host,
	}
	if rsName != "" {
		q := u.Query()
		q.Set("replicaSet", rsName)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}
