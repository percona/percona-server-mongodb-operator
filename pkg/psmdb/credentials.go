package psmdb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type Credentials struct {
	Username string
	Password string
}

func GetUserSecret(ctx context.Context, cl client.Reader, cr *psmdbv1.PerconaServerMongoDB, name string) (corev1.Secret, error) {
	secrets := corev1.Secret{}
	err := cl.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, &secrets)
	return secrets, errors.Wrap(err, "get user secrets")
}

func GetCredentials(ctx context.Context, cl client.Reader, cr *psmdbv1.PerconaServerMongoDB, name string, role psmdbv1.SystemUserRole) (Credentials, error) {
	creds := Credentials{}
	usersSecret, err := GetUserSecret(ctx, cl, cr, name)
	if err != nil {
		return creds, errors.Wrap(err, "failed to get user secret")
	}

	switch role {
	case psmdbv1.RoleDatabaseAdmin:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBDatabaseAdminUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBDatabaseAdminPassword])
	case psmdbv1.RoleClusterAdmin:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterAdminUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterAdminPassword])
	case psmdbv1.RoleUserAdmin:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBUserAdminUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBUserAdminPassword])
	case psmdbv1.RoleClusterMonitor:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterMonitorUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBClusterMonitorPassword])
	case psmdbv1.RoleBackup:
		creds.Username = string(usersSecret.Data[psmdbv1.EnvMongoDBBackupUser])
		creds.Password = string(usersSecret.Data[psmdbv1.EnvMongoDBBackupPassword])
	default:
		return creds, errors.Errorf("not implemented for role: %s", role)
	}

	if creds.Username == "" || creds.Password == "" {
		return creds, errors.Errorf("can't find credentials for role %s", role)
	}

	return creds, nil
}

func GetInternalCredentials(ctx context.Context, cl client.Reader, cr *psmdbv1.PerconaServerMongoDB, role psmdbv1.SystemUserRole) (Credentials, error) {
	return GetCredentials(ctx, cl, cr, psmdbv1.UserSecretName(cr), role)
}
