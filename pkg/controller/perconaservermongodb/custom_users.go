package perconaservermongodb

import (
	"context"
	"fmt"
	"reflect"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func (r *ReconcilePerconaServerMongoDB) reconcileCustomUsers(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Users == nil || len(cr.Spec.Users) == 0 {
		return nil
	}

	if cr.Status.State != api.AppStateReady {
		return nil
	}

	log := logf.FromContext(ctx)

	var err error
	var cli mongo.Client
	if cr.Spec.Sharding.Enabled {
		cli, err = r.mongosClientWithRole(ctx, cr, api.RoleUserAdmin)
	} else {
		cli, err = r.mongoClientWithRole(ctx, cr, *cr.Spec.Replsets[0], api.RoleUserAdmin)
	}
	if err != nil {
		return errors.Wrap(err, "failed to get mongo client")
	}
	defer func() {
		err := cli.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close mongo connection")
		}
	}()

	sysUsersSecret := corev1.Secret{}
	err = r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      api.InternalUserSecretName(cr),
		},
		&sysUsersSecret,
	)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get internal sys users secret")
	}

	sysUserNames := sysUserNames(sysUsersSecret)

	for _, user := range cr.Spec.Users {
		if _, ok := sysUserNames[user.Name]; ok {
			log.Error(nil, "creating user with reserved user name is forbidden", "user", user.Name)
			continue
		}

		sec, err := getUserSecret(ctx, r.client, cr, user.PasswordSecretRef.Name)
		if err != nil {
			log.Error(err, "failed to get user secret", "user", user)
			continue
		}

		annotationKey := fmt.Sprintf("percona.com/%s-hash", user.Name)

		newHash := sha256Hash(sec.Data[user.PasswordSecretRef.Key])

		hash, ok := sec.Annotations[annotationKey]
		if ok && hash == newHash {
			continue
		}

		if sec.Annotations == nil {
			sec.Annotations = make(map[string]string)
		}

		userInfo, err := cli.GetUserInfo(ctx, user.Name)
		if err != nil {
			log.Error(err, "get user info")
			continue
		}

		if userInfo != nil && hash != newHash {
			log.Info("User password changed, updating it.", "user", user.Name)
			err := cli.UpdateUserPass(ctx, user.Db, user.Name, string(sec.Data[user.PasswordSecretRef.Key]))
			if err != nil {
				log.Error(err, "failed to update user pass", "user", user.Name)
				continue
			}
			sec.Annotations[annotationKey] = string(newHash)
			if err := r.client.Update(ctx, &sec); err != nil {
				log.Error(err, "update user secret", "user", user.Name, "secret", sec.Name)
				continue
			}
			log.Info("User updated", "user", user.Name)
		}

		roles := make([]map[string]interface{}, 0)
		for _, role := range user.Roles {
			roles = append(roles, map[string]interface{}{
				"role": role.Name,
				"db":   role.Db,
			})
		}

		if userInfo != nil && !reflect.DeepEqual(userInfo.Roles, roles) {
			log.Info("User roles changed, updating them.", "user", user.Name)
			err := cli.UpdateUserRoles(ctx, user.Db, user.Name, roles)
			if err != nil {
				log.Error(err, "failed to update user roles", "user", user.Name)
				continue
			}
		}

		if userInfo != nil {
			continue
		}

		log.Info("Creating user", "user", user.Name)
		err = cli.CreateUser(ctx, user.Db, user.Name, string(sec.Data[user.PasswordSecretRef.Key]), roles...)
		if err != nil {
			log.Error(err, "failed to create user", "user", user.Name)
			continue
		}

		sec.Annotations[annotationKey] = string(newHash)
		if err := r.client.Update(ctx, &sec); err != nil {
			log.Error(err, "update user secret", "user", user.Name, "secret", sec.Name)
			continue
		}

		log.Info("User created", "user", user.Name)
	}

	return nil
}

func sysUserNames(sysUsersSecret corev1.Secret) map[string]struct{} {
	sysUserNames := make(map[string]struct{}, len(sysUsersSecret.Data))

	sysUserNames[string(sysUsersSecret.Data[api.EnvMongoDBClusterAdminUser])] = struct{}{}
	sysUserNames[string(sysUsersSecret.Data[api.EnvMongoDBDatabaseAdminUser])] = struct{}{}
	sysUserNames[string(sysUsersSecret.Data[api.EnvMongoDBUserAdminUser])] = struct{}{}
	sysUserNames[string(sysUsersSecret.Data[api.EnvMongoDBBackupUser])] = struct{}{}
	sysUserNames[string(sysUsersSecret.Data[api.EnvMongoDBClusterMonitorUser])] = struct{}{}

	return sysUserNames
}