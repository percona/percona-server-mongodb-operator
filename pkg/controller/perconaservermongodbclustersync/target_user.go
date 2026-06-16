package perconaservermongodbclustersync

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
)

const (
	targetUserSecretUsernameKey = "username"
	targetUserSecretPasswordKey = "password"
)

var syncTargetUserRoles = []mongo.Role{
	{Role: "restore", DB: "admin"},
	{Role: "clusterMonitor", DB: "admin"},
	{Role: "clusterManager", DB: "admin"},
	{Role: "readWriteAnyDatabase", DB: "admin"},
}

// Scoped to the CR name so a new CR after a finalized one gets its own user.
func syncTargetUsername(cr *psmdbv1.PerconaServerMongoDBClusterSync) string {
	return fmt.Sprintf("clustersync-%s", cr.Name)
}

type targetMongoClientFn func(ctx context.Context, cl client.Client, target *psmdbv1.PerconaServerMongoDB) (mongo.Client, error)

func (r *ReconcilePerconaServerMongoDBClusterSync) ensureSyncTargetUser(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDBClusterSync,
	target *psmdbv1.PerconaServerMongoDB,
) (psmdb.Credentials, error) {
	log := logf.FromContext(ctx)
	creds, err := r.ensureTargetUserSecret(ctx, cr)
	if err != nil {
		return psmdb.Credentials{}, errors.Wrap(err, "ensure target user secret")
	}

	mc, err := r.newTargetMongoClient(ctx, r.client, target)
	if err != nil {
		return psmdb.Credentials{}, errors.Wrap(err, "connect to target cluster")
	}
	defer func(mc mongo.Client, ctx context.Context) {
		err := mc.Disconnect(ctx)
		if err != nil {
			log.Error(err, "close target cluster")
		}
	}(mc, ctx)

	if err := ensureTargetMongoUser(ctx, mc, creds); err != nil {
		return psmdb.Credentials{}, errors.Wrap(err, "ensure target mongo user")
	}
	return creds, nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) ensureTargetUserSecret(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDBClusterSync,
) (psmdb.Credentials, error) {
	nn := types.NamespacedName{Name: clustersync.TargetUserSecretName(cr), Namespace: cr.Namespace}
	existing := &corev1.Secret{}
	err := r.client.Get(ctx, nn, existing)
	switch {
	case err == nil:
		username := string(existing.Data[targetUserSecretUsernameKey])
		password := string(existing.Data[targetUserSecretPasswordKey])
		if username == "" || password == "" {
			return psmdb.Credentials{}, errors.Errorf("target user secret %s missing %s/%s keys",
				nn, targetUserSecretUsernameKey, targetUserSecretPasswordKey)
		}
		return psmdb.Credentials{Username: username, Password: password}, nil
	case !k8serrors.IsNotFound(err):
		return psmdb.Credentials{}, errors.Wrapf(err, "get target user secret %s", nn)
	}

	password, err := secret.GeneratePassword()
	if err != nil {
		return psmdb.Credentials{}, errors.Wrap(err, "generate target user password")
	}
	username := syncTargetUsername(cr)

	newSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nn.Name,
			Namespace: nn.Namespace,
			Labels:    clustersync.Labels(cr),
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			targetUserSecretUsernameKey: []byte(username),
			targetUserSecretPasswordKey: password,
		},
	}
	if err := controllerutil.SetControllerReference(cr, newSecret, r.scheme); err != nil {
		return psmdb.Credentials{}, errors.Wrap(err, "set owner reference on target user secret")
	}
	if err := r.client.Create(ctx, newSecret); err != nil {
		return psmdb.Credentials{}, errors.Wrapf(err, "create target user secret %s", nn)
	}
	return psmdb.Credentials{Username: username, Password: string(password)}, nil
}

// ensureTargetMongoUser converges the sync user on the target cluster: it
// creates the user when missing, and re-applies password and roles when it
// already exists. The latter heals the case where the CR (and its owned
// Secret) was deleted and recreated while the Mongo user, which has no owner
// ref, kept its old password.
func ensureTargetMongoUser(ctx context.Context, mc mongo.Client, creds psmdb.Credentials) error {
	existing, err := mc.GetUserInfo(ctx, creds.Username, "admin")
	if err != nil {
		return errors.Wrap(err, "look up target user")
	}
	if existing == nil {
		if err := mc.CreateUser(ctx, "admin", creds.Username, creds.Password, syncTargetUserRoles...); err != nil {
			return errors.Wrapf(err, "create target user %q", creds.Username)
		}
		return nil
	}
	if err := mc.UpdateUserPass(ctx, "admin", creds.Username, creds.Password); err != nil {
		return errors.Wrapf(err, "sync target user password %q", creds.Username)
	}
	if err := mc.UpdateUserRoles(ctx, "admin", creds.Username, syncTargetUserRoles); err != nil {
		return errors.Wrapf(err, "sync target user roles %q", creds.Username)
	}
	return nil
}

func defaultTargetMongoClient(ctx context.Context, cl client.Client, target *psmdbv1.PerconaServerMongoDB) (mongo.Client, error) {
	if target.Spec.Secrets == nil || target.Spec.Secrets.Users == "" {
		return nil, errors.Errorf("target cluster %s has no users secret configured", target.Name)
	}

	usersSecret := &corev1.Secret{}
	nn := types.NamespacedName{Name: target.Spec.Secrets.Users, Namespace: target.Namespace}
	if err := cl.Get(ctx, nn, usersSecret); err != nil {
		return nil, errors.Wrapf(err, "get target users secret %s", nn)
	}

	creds := psmdb.Credentials{
		Username: string(usersSecret.Data[psmdbv1.EnvMongoDBUserAdminUser]),
		Password: string(usersSecret.Data[psmdbv1.EnvMongoDBUserAdminPassword]),
	}
	if creds.Username == "" || creds.Password == "" {
		return nil, errors.Errorf("target users secret %s missing %s/%s keys",
			nn, psmdbv1.EnvMongoDBUserAdminUser, psmdbv1.EnvMongoDBUserAdminPassword)
	}

	if target.Spec.Sharding.Enabled {
		return psmdb.MongosClient(ctx, cl, target, creds)
	}
	if len(target.Spec.Replsets) == 0 {
		return nil, errors.Errorf("target cluster %s has no replsets", target.Name)
	}
	return psmdb.MongoClient(ctx, cl, target, target.Spec.Replsets[0], creds)
}
