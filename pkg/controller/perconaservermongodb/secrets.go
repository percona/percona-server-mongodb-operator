package perconaservermongodb

import (
	"context"
	"fmt"
	"net/url"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
)

const (
	envMongoDBDatabaseAdminUser      = "MONGODB_DATABASE_ADMIN_USER"
	envMongoDBDatabaseAdminPassword  = "MONGODB_DATABASE_ADMIN_PASSWORD"
	envMongoDBClusterAdminUser       = "MONGODB_CLUSTER_ADMIN_USER"
	envMongoDBClusterAdminPassword   = "MONGODB_CLUSTER_ADMIN_PASSWORD"
	envMongoDBUserAdminUser          = "MONGODB_USER_ADMIN_USER"
	envMongoDBUserAdminPassword      = "MONGODB_USER_ADMIN_PASSWORD"
	envMongoDBBackupUser             = "MONGODB_BACKUP_USER"
	envMongoDBBackupPassword         = "MONGODB_BACKUP_PASSWORD"
	envMongoDBClusterMonitorUser     = "MONGODB_CLUSTER_MONITOR_USER"
	envMongoDBClusterMonitorPassword = "MONGODB_CLUSTER_MONITOR_PASSWORD"
	envPMMServerUser                 = api.PMMUserKey
	envPMMServerPassword             = api.PMMPasswordKey
	envPMMServerAPIKey               = api.PMMAPIKey
)

type UserRole string

const (
	roleDatabaseAdmin  UserRole = "databaseAdmin"
	roleClusterAdmin   UserRole = "clusterAdmin"
	roleUserAdmin      UserRole = "userAdmin"
	roleClusterMonitor UserRole = "clusterMonitor"
	roleBackup         UserRole = "backup"
)

func (r *ReconcilePerconaServerMongoDB) getUserSecret(ctx context.Context, cr *api.PerconaServerMongoDB, name string) (corev1.Secret, error) {
	secrets := corev1.Secret{}
	err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, &secrets)
	return secrets, errors.Wrap(err, "get user secrets")
}

func (r *ReconcilePerconaServerMongoDB) getInternalCredentials(ctx context.Context, cr *api.PerconaServerMongoDB, role UserRole) (psmdb.Credentials, error) {
	return r.getCredentials(ctx, cr, api.UserSecretName(cr), role)
}

func (r *ReconcilePerconaServerMongoDB) getCredentials(ctx context.Context, cr *api.PerconaServerMongoDB, name string, role UserRole) (psmdb.Credentials, error) {
	creds := psmdb.Credentials{}
	usersSecret, err := r.getUserSecret(ctx, cr, name)
	if err != nil {
		return creds, errors.Wrap(err, "failed to get user secret")
	}

	switch role {
	case roleDatabaseAdmin:
		creds.Username = string(usersSecret.Data[envMongoDBDatabaseAdminUser])
		creds.Password = string(usersSecret.Data[envMongoDBDatabaseAdminPassword])
	case roleClusterAdmin:
		creds.Username = string(usersSecret.Data[envMongoDBClusterAdminUser])
		creds.Password = string(usersSecret.Data[envMongoDBClusterAdminPassword])
	case roleUserAdmin:
		creds.Username = string(usersSecret.Data[envMongoDBUserAdminUser])
		creds.Password = string(usersSecret.Data[envMongoDBUserAdminPassword])
	case roleClusterMonitor:
		creds.Username = string(usersSecret.Data[envMongoDBClusterMonitorUser])
		creds.Password = string(usersSecret.Data[envMongoDBClusterMonitorPassword])
	case roleBackup:
		creds.Username = string(usersSecret.Data[envMongoDBBackupUser])
		creds.Password = string(usersSecret.Data[envMongoDBBackupPassword])
	default:
		return creds, errors.Errorf("not implemented for role: %s", role)
	}

	if creds.Username == "" || creds.Password == "" {
		return creds, errors.Errorf("can't find credentials for role %s", role)
	}

	return creds, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileUsersSecret(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	err := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.Users,
		},
		&secretObj,
	)
	if err == nil {
		shouldUpdate, err := fillSecretData(cr, secretObj.Data)
		if err != nil {
			return errors.Wrap(err, "failed to fill secret data")
		}
		if cr.CompareVersion("1.2.0") < 0 {
			for _, v := range []string{envMongoDBClusterMonitorUser, envMongoDBClusterMonitorPassword} {
				escaped, ok := secretObj.Data[v+"_ESCAPED"]
				if !ok || url.QueryEscape(string(secretObj.Data[v])) != string(escaped) {
					secretObj.Data[v+"_ESCAPED"] = []byte(url.QueryEscape(string(secretObj.Data[v])))
					shouldUpdate = true
				}
			}
		}
		if shouldUpdate {
			err = r.client.Update(ctx, &secretObj)
		}
		return errors.Wrap(err, "update users secret")
	} else if k8serrors.IsNotFound(err) && cr.Spec.Unmanaged {
		return errors.Errorf("users secret '%s' is required for unmanaged clusters", cr.Spec.Secrets.Users)
	} else if !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get users secret")
	}

	data := make(map[string][]byte)
	_, err = fillSecretData(cr, data)
	if err != nil {
		return errors.Wrap(err, "fill users secret")
	}
	secretObj = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Spec.Secrets.Users,
			Namespace: cr.Namespace,
		},
		Data: data,
		Type: corev1.SecretTypeOpaque,
	}
	err = r.client.Create(ctx, &secretObj)
	if err != nil {
		return fmt.Errorf("create Users secret: %v", err)
	}

	return nil
}

func fillSecretData(cr *api.PerconaServerMongoDB, data map[string][]byte) (bool, error) {
	if data == nil {
		data = make(map[string][]byte)
	}
	userMap := map[string]string{
		envMongoDBBackupUser:         string(roleBackup),
		envMongoDBClusterAdminUser:   string(roleClusterAdmin),
		envMongoDBClusterMonitorUser: string(roleClusterMonitor),
		envMongoDBUserAdminUser:      string(roleUserAdmin),
	}
	passKeys := []string{
		envMongoDBClusterAdminPassword,
		envMongoDBUserAdminPassword,
		envMongoDBBackupPassword,
		envMongoDBClusterMonitorPassword,
	}
	if cr.CompareVersion("1.13.0") >= 0 {
		userMap[envMongoDBDatabaseAdminUser] = string(roleDatabaseAdmin)
		passKeys = append(passKeys, envMongoDBDatabaseAdminPassword)
	}

	changes := false
	for user, role := range userMap {
		if _, ok := data[user]; !ok {
			data[user] = []byte(role)
			changes = true
		}
	}

	var err error
	for _, k := range passKeys {
		if _, ok := data[k]; !ok {
			data[k], err = secret.GeneratePassword()
			if err != nil {
				return false, errors.Wrapf(err, "create %s password", k)
			}
			changes = true
		}
	}
	return changes, nil
}
