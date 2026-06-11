package perconaservermongodb

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
	pkgSecret "github.com/percona/percona-server-mongodb-operator/pkg/secret"
)

func getUserSecret(ctx context.Context, cl client.Reader, cr *api.PerconaServerMongoDB, name string) (corev1.Secret, error) {
	secrets := corev1.Secret{}
	err := cl.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, &secrets)
	return secrets, errors.Wrap(err, "get user secrets")
}

func getInternalCredentials(ctx context.Context, cl client.Reader, cr *api.PerconaServerMongoDB, role api.SystemUserRole) (psmdb.Credentials, error) {
	usersSecret, err := getUserSecret(ctx, cl, cr, api.UserSecretName(cr))
	if err != nil {
		return psmdb.Credentials{}, errors.Wrap(err, "failed to get user secret")
	}
	return getCredentials(&usersSecret, role)
}

func getCredentials(secret *corev1.Secret, role api.SystemUserRole) (psmdb.Credentials, error) {
	creds := psmdb.Credentials{}
	envKeyUser, envKeyPass := role.EnvKeyUsername(), role.EnvKeyPassword()
	if envKeyUser == "" || envKeyPass == "" {
		return creds, errors.Errorf("invalid role %s", string(role))
	}
	creds.Username = string(secret.Data[envKeyUser])
	creds.Password = string(secret.Data[envKeyPass])

	if creds.Username == "" || creds.Password == "" {
		return creds, errors.Errorf("can't find credentials for role %s", role)
	}

	return creds, nil
}

func ensureConnectionStringSecret(
	ctx context.Context,
	cl client.Client,
	cr *api.PerconaServerMongoDB,
	secretName, keyPrefix string,
	cred psmdb.Credentials,
	owner metav1.Object,
	includeReplsets bool,
) error {
	connStrSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cr.Namespace,
		},
	}

	_, err := controllerutil.CreateOrUpdate(ctx, cl, connStrSecret, func() error {
		connStrSecret.Data = make(map[string][]byte)
		if includeReplsets {
			for _, rs := range cr.GetAllReplsets() {
				cfg, err := psmdb.MongoConfig(ctx, cl, cr, rs, cred, false)
				if err != nil {
					return errors.Wrap(err, "mongo config")
				}

				connStr := cfg.URI()
				key := keyPrefix + "_" + rs.Name
				connStrSecret.Data[key+"_connectionString"] = []byte(connStr)
				connStrSecret.Data[key+"_connectionStringSrv"] = []byte(cfg.SRVURI(strings.Join([]string{
					naming.ServiceName(cr, rs),
					cr.Namespace,
					cr.Spec.ClusterServiceDNSSuffix,
				}, ".")))

				if rs.Expose.Enabled {
					cfg, err := psmdb.MongoConfig(ctx, cl, cr, rs, cred, true)
					if err != nil {
						return errors.Wrap(err, "mongo config")
					}
					if exposedConnStr := cfg.URI(); exposedConnStr != connStr {
						connStrSecret.Data[key+"_connectionStringExposed"] = []byte(exposedConnStr)
					}
				}
			}
		}

		if cr.Spec.Sharding.Enabled {
			servicePerPod := cr.Spec.Sharding.Mongos.Expose.ServicePerPod
			mongosCfg, err := psmdb.MongosConfig(ctx, cl, cr, cred, true, servicePerPod)
			if err != nil {
				return errors.Wrap(err, "mongos config")
			}
			connStrSecret.Data[keyPrefix+"_mongos_connectionString"] = []byte(mongosCfg.URI())

			if servicePerPod {
				mongosCfg, err := psmdb.MongosConfig(ctx, cl, cr, cred, false, true)
				if err != nil {
					return errors.Wrap(err, "mongos config")
				}
				connStrSecret.Data[keyPrefix+"_mongos_connectionStringExposed"] = []byte(mongosCfg.URI())
			}
		}
		if err := controllerutil.SetOwnerReference(owner, connStrSecret, cl.Scheme()); err != nil {
			return errors.Wrap(err, "set owner reference")
		}
		return nil
	})
	return errors.Wrap(err, "create or update")
}

func (r *ReconcilePerconaServerMongoDB) reconcileUsersSecret(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	err := r.client.Get(
		ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.Users,
		},
		&secretObj,
	)
	if err == nil {
		shouldUpdate, err := fillSecretData(ctx, cr, secretObj.Data, true, r.secretProviderHandler)
		if err != nil {
			return errors.Wrap(err, "failed to fill secret data")
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
	_, err = fillSecretData(ctx, cr, data, false, r.secretProviderHandler)
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

func fillSecretData(ctx context.Context, cr *api.PerconaServerMongoDB, data map[string][]byte, secretExists bool, ph *pkgSecret.ProviderHandler) (bool, error) {
	log := logf.FromContext(ctx)

	if data == nil {
		data = make(map[string][]byte)
	}

	var changes bool
	var err error

	if ph != nil {
		changes, err = ph.FillSecretData(ctx, cr, data)
		if err != nil {
			if pkgSecret.IsCriticalErr(err) || !secretExists {
				return false, errors.Wrap(err, "failed to fill secret from secret provider")
			}
			log.Error(err, "failed to fill secret from secret provider")
		}
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

	for user, role := range userMap {
		if _, ok := data[user]; !ok {
			data[user] = []byte(role)
			changes = true
		}
	}

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
