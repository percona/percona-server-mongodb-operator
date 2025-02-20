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
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
)

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
			for _, v := range []string{api.EnvMongoDBClusterMonitorUser, api.EnvMongoDBClusterMonitorPassword} {
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
			Labels:    naming.ClusterLabels(cr),
		},
		Data: data,
		Type: corev1.SecretTypeOpaque,
	}
	if cr.CompareVersion("1.17.0") < 0 {
		secretObj.Labels = nil
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
		api.EnvMongoDBBackupUser:         string(api.RoleBackup),
		api.EnvMongoDBClusterAdminUser:   string(api.RoleClusterAdmin),
		api.EnvMongoDBClusterMonitorUser: string(api.RoleClusterMonitor),
		api.EnvMongoDBUserAdminUser:      string(api.RoleUserAdmin),
	}
	passKeys := []string{
		api.EnvMongoDBClusterAdminPassword,
		api.EnvMongoDBUserAdminPassword,
		api.EnvMongoDBBackupPassword,
		api.EnvMongoDBClusterMonitorPassword,
	}
	if cr.CompareVersion("1.13.0") >= 0 {
		userMap[api.EnvMongoDBDatabaseAdminUser] = string(api.RoleDatabaseAdmin)
		passKeys = append(passKeys, api.EnvMongoDBDatabaseAdminPassword)
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
