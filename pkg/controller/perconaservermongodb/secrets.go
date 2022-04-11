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
	envMongoDBClusterAdminUser       = "MONGODB_CLUSTER_ADMIN_USER"
	envMongoDBClusterAdminPassword   = "MONGODB_CLUSTER_ADMIN_PASSWORD"
	envMongoDBUserAdminUser          = "MONGODB_USER_ADMIN_USER"
	envMongoDBUserAdminPassword      = "MONGODB_USER_ADMIN_PASSWORD"
	envMongoDBBackupUser             = "MONGODB_BACKUP_USER"
	envMongoDBBackupPassword         = "MONGODB_BACKUP_PASSWORD"
	envMongoDBClusterMonitorUser     = "MONGODB_CLUSTER_MONITOR_USER"
	envMongoDBClusterMonitorPassword = "MONGODB_CLUSTER_MONITOR_PASSWORD"
	envPMMServerUser                 = "PMM_SERVER_USER"
	envPMMServerPassword             = "PMM_SERVER_PASSWORD"
)

type UserRole string

const (
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
		if cr.CompareVersion("1.2.0") < 0 {
			shouldUpdate := false
			for _, v := range []string{envMongoDBClusterMonitorUser, envMongoDBClusterMonitorPassword} {
				escaped, ok := secretObj.Data[v+"_ESCAPED"]
				if !ok || url.QueryEscape(string(secretObj.Data[v])) != string(escaped) {
					secretObj.Data[v+"_ESCAPED"] = []byte(url.QueryEscape(string(secretObj.Data[v])))
					shouldUpdate = true
				}
			}
			if shouldUpdate {
				err = r.client.Update(ctx, &secretObj)
			}
		}
		return errors.Wrap(err, "escape users secret")
	} else if k8serrors.IsNotFound(err) && cr.Spec.Unmanaged {
		return errors.Errorf("users secret '%s' is required for unmanaged clusters", cr.Spec.Secrets.Users)
	} else if !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get users secret")
	}

	data := make(map[string][]byte)
	data["MONGODB_BACKUP_USER"] = []byte(roleBackup)
	data["MONGODB_BACKUP_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create backup users pass: %v", err)
	}
	data["MONGODB_CLUSTER_ADMIN_USER"] = []byte(roleClusterAdmin)
	data["MONGODB_CLUSTER_ADMIN_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create cluster admin users pass: %v", err)
	}
	data["MONGODB_CLUSTER_MONITOR_USER"] = []byte(roleClusterMonitor)
	data["MONGODB_CLUSTER_MONITOR_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create cluster monitor users pass: %v", err)
	}
	if cr.CompareVersion("1.2.0") < 0 {
		data["MONGODB_CLUSTER_MONITOR_PASSWORD_ESCAPED"] = []byte(url.QueryEscape(string(data["MONGODB_CLUSTER_MONITOR_PASSWORD"])))
	}
	data["MONGODB_USER_ADMIN_USER"] = []byte(roleUserAdmin)
	data["MONGODB_USER_ADMIN_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create admin users pass: %v", err)
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
