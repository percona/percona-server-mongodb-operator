package perconaservermongodbclustersync

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
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

	admin = "admin"

	targetUserPasswordMACAnnotation = "psmdb.percona.com/target-user-password-mac"
	targetUserPasswordMACKey        = "psmdb-target-user-password-annotation-v1"
)

var syncTargetUserRoles = []mongo.Role{
	{Role: "restore", DB: "admin"},
	{Role: "clusterMonitor", DB: "admin"},
	{Role: "clusterManager", DB: "admin"},
	{Role: "readWriteAnyDatabase", DB: "admin"},
}

var sortRolesOpt = cmpopts.SortSlices(func(a, b mongo.Role) bool {
	if a.DB != b.DB {
		return a.DB < b.DB
	}
	return a.Role < b.Role
})

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
	sec, err := r.ensureTargetUserSecret(ctx, cr)
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

	mutated, err := ensureTargetMongoUser(ctx, mc, sec)
	if err != nil {
		return psmdb.Credentials{}, errors.Wrap(err, "ensure target mongo user")
	}
	if mutated {
		if err := r.client.Update(ctx, sec); err != nil {
			return psmdb.Credentials{}, errors.Wrap(err, "update target user secret annotations")
		}
	}
	return psmdb.Credentials{
		Username: string(sec.Data[targetUserSecretUsernameKey]),
		Password: string(sec.Data[targetUserSecretPasswordKey]),
	}, nil
}

func (r *ReconcilePerconaServerMongoDBClusterSync) ensureTargetUserSecret(
	ctx context.Context,
	cr *psmdbv1.PerconaServerMongoDBClusterSync,
) (*corev1.Secret, error) {
	nn := types.NamespacedName{Name: clustersync.TargetUserSecretName(cr), Namespace: cr.Namespace}
	existing := &corev1.Secret{}
	err := r.client.Get(ctx, nn, existing)
	switch {
	case err == nil:
		if v, ok := existing.Data[targetUserSecretUsernameKey]; !ok || len(v) == 0 {
			return nil, errors.Errorf("target user secret %s missing %s key", nn, targetUserSecretUsernameKey)
		}
		if v, ok := existing.Data[targetUserSecretPasswordKey]; !ok || len(v) == 0 {
			return nil, errors.Errorf("target user secret %s missing %s key", nn, targetUserSecretPasswordKey)
		}
		return existing, nil
	case !k8serrors.IsNotFound(err):
		return nil, errors.Wrapf(err, "get target user secret %s", nn)
	}

	password, err := secret.GeneratePassword()
	if err != nil {
		return nil, errors.Wrap(err, "generate target user password")
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
		return nil, errors.Wrap(err, "set owner reference on target user secret")
	}
	if err := r.client.Create(ctx, newSecret); err != nil {
		return nil, errors.Wrapf(err, "create target user secret %s", nn)
	}
	return newSecret, nil
}

func ensureTargetMongoUser(ctx context.Context, mc mongo.Client, sec *corev1.Secret) (bool, error) {
	username := string(sec.Data[targetUserSecretUsernameKey])
	password := sec.Data[targetUserSecretPasswordKey]

	existing, err := mc.GetUserInfo(ctx, username, admin)
	if err != nil {
		return false, errors.Wrap(err, "look up target user")
	}
	if existing == nil {
		if err := mc.CreateUser(ctx, admin, username, string(password), syncTargetUserRoles...); err != nil {
			return false, errors.Wrapf(err, "create target user %q", username)
		}
		setPasswordHashAnnotation(sec, password)
		return true, nil
	}

	mutated := false
	newMAC := passwordAnnotationMAC(password)
	if sec.Annotations[targetUserPasswordMACAnnotation] != newMAC {
		if err := mc.UpdateUserPass(ctx, admin, username, string(password)); err != nil {
			return false, errors.Wrapf(err, "sync target user password %q", username)
		}
		setPasswordHashAnnotation(sec, password)
		mutated = true
	}
	if !cmp.Equal(existing.Roles, syncTargetUserRoles, sortRolesOpt) {
		if err := mc.UpdateUserRoles(ctx, admin, username, syncTargetUserRoles); err != nil {
			return false, errors.Wrapf(err, "sync target user roles %q", username)
		}
	}
	return mutated, nil
}

func setPasswordHashAnnotation(sec *corev1.Secret, password []byte) {
	if sec.Annotations == nil {
		sec.Annotations = make(map[string]string)
	}
	sec.Annotations[targetUserPasswordMACAnnotation] = passwordAnnotationMAC(password)
}

func passwordAnnotationMAC(data []byte) string {
	mac := hmac.New(sha256.New, []byte(targetUserPasswordMACKey))
	mac.Write(data)
	return hex.EncodeToString(mac.Sum(nil))
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
