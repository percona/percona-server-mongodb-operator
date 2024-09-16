package perconaservermongodb

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func (r *ReconcilePerconaServerMongoDB) reconcileCustomUsers(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Users == nil && len(cr.Spec.Users) == 0 && cr.Spec.Roles == nil && len(cr.Spec.Roles) == 0 {
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

	err = handleRoles(ctx, cr, cli)
	if err != nil {
		return errors.Wrap(err, "handle roles")
	}

	if cr.Spec.Users == nil || len(cr.Spec.Users) == 0 {
		return nil
	}

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

		if user.DB == "" {
			user.DB = "admin"
		}

		if user.PasswordSecretRef.Key == "" {
			user.PasswordSecretRef.Key = "password"
		}

		sec, err := getUserSecret(ctx, r.client, cr, user.PasswordSecretRef.Name)
		if err != nil {
			log.Error(err, "failed to get user secret", "user", user)
			continue
		}

		userInfo, err := cli.GetUserInfo(ctx, user.Name, user.DB)
		if err != nil {
			log.Error(err, "get user info")
			continue
		}

		err = updatePass(ctx, r.client, cli, &user, userInfo, &sec)
		if err != nil {
			log.Error(err, "update user pass", "user", user.Name)
			continue
		}

		err = updateRoles(ctx, cli, &user, userInfo)
		if err != nil {
			log.Error(err, "update user roles", "user", user.Name)
			continue
		}

		err = createUser(ctx, r.client, cli, &user, userInfo, &sec)
		if err != nil {
			return errors.Wrapf(err, "create user %s", user.Name)
		}
	}

	return nil
}

func handleRoles(ctx context.Context, cr *api.PerconaServerMongoDB, cli mongo.Client) error {
	if len(cr.Spec.Roles) == 0 {
		return nil
	}

	for _, role := range cr.Spec.Roles {
		roleInfo, err := cli.GetRole(ctx, role.DB, role.Role)
		if err != nil {
			return errors.Wrap(err, "mongo get role")
		}

		mr, err := toMongoRoleModel(role)
		if err != nil {
			return err
		}

		if roleInfo == nil {
			println("AAAAAAAAAAAAAAAAAAA CREAAYEEEE")
			err = cli.CreateRole(ctx, role.DB, *mr)
			return errors.Wrapf(err, "create role %s", role.Role)
		}

		// err = cli.UpdateRole(ctx, role.DB, mr)
		// return errors.Wrapf(err, "update role %s", role.Role)
		if !reflect.DeepEqual(mr, roleInfo) {
			println("AAAAAAAAAAAAAAAAAAAAAA UPDAAAYEEEE")
			logf.FromContext(ctx).Info("AAAAAA Updating role", "role", role)
			logf.FromContext(ctx).Info("AAAAAA RoleInfo", "roleInfo", roleInfo)
			err = cli.UpdateRole(ctx, role.DB, *mr)
			return errors.Wrapf(err, "update role %s", role.Role)
		}
	}

	return nil
}

func toMongoRoleModel(role api.Role) (*mongo.Role, error) {
	mr := &mongo.Role{
		Role: role.Role,
		DB:   role.DB,
	}

	for _, r := range role.Roles {
		mr.Roles = append(mr.Roles, mongo.InheritenceRole{
			Role: r.Role,
			DB:   r.DB,
		})
	}

	for _, p := range role.Privileges {
		if p.Resource.Cluster != nil && (p.Resource.DB != "" || p.Resource.Collection != "") {
			return nil, errors.New("field role.privilege.resource must have exactly db and collection set, or have only cluster set")
		}

		rp := mongo.RolePrivilege{
			Actions:  p.Actions,
			Resource: make(map[string]interface{}, 3),
		}

		if p.Resource.Cluster != nil {
			rp.Resource["cluster"] = p.Resource.Cluster
		} else {
			rp.Resource["db"] = p.Resource.DB
			rp.Resource["collection"] = p.Resource.Collection
		}

		mr.Privileges = append(mr.Privileges, rp)
	}

	if role.AuthenticationRestrictions != nil {
		for _, ar := range role.AuthenticationRestrictions {
			mr.AuthenticationRestrictions = append(mr.AuthenticationRestrictions, mongo.RoleAuthenticationRestriction{
				ClientSource:  ar.ClientSource,
				ServerAddress: ar.ServerAddress,
			})
		}
	}

	return mr, nil
}

// sysUserNames returns a set of system user names from the sysUsersSecret.
func sysUserNames(sysUsersSecret corev1.Secret) map[string]struct{} {
	sysUserNames := make(map[string]struct{}, len(sysUsersSecret.Data))
	for k, v := range sysUsersSecret.Data {
		if strings.Contains(k, "_USER") {
			sysUserNames[string(v)] = struct{}{}
		}
	}
	return sysUserNames
}

func updatePass(
	ctx context.Context,
	cli client.Client,
	mongoCli mongo.Client,
	user *api.User,
	userInfo *mongo.User,
	secret *corev1.Secret) error {
	log := logf.FromContext(ctx)

	if userInfo == nil {
		return nil
	}

	annotationKey := fmt.Sprintf("percona.com/%s-hash", user.Name)

	newHash := sha256Hash(secret.Data[user.PasswordSecretRef.Key])

	hash, ok := secret.Annotations[annotationKey]
	if ok && hash == newHash {
		return nil
	}

	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	log.Info("User password changed, updating it.", "user", user.UserID())

	err := mongoCli.UpdateUserPass(ctx, user.DB, user.Name, string(secret.Data[user.PasswordSecretRef.Key]))
	if err != nil {
		return errors.Wrapf(err, "update user %s password", user.Name)
	}

	secret.Annotations[annotationKey] = string(newHash)
	if err := cli.Update(ctx, secret); err != nil {
		return errors.Wrapf(err, "update secret %s", secret.Name)
	}

	log.Info("User updated", "user", user.UserID())

	return nil
}

func updateRoles(
	ctx context.Context,
	mongoCli mongo.Client,
	user *api.User,
	userInfo *mongo.User) error {
	log := logf.FromContext(ctx)

	if userInfo == nil {
		return nil
	}

	roles := make([]map[string]interface{}, 0)
	for _, role := range user.Roles {
		roles = append(roles, map[string]interface{}{
			"role": role.Name,
			"db":   role.DB,
		})
	}

	if reflect.DeepEqual(userInfo.Roles, roles) {
		return nil
	}

	log.Info("User roles changed, updating them.", "user", user.UserID())
	err := mongoCli.UpdateUserRoles(ctx, user.DB, user.Name, roles)
	if err != nil {
		return err
	}

	return nil
}

func createUser(
	ctx context.Context,
	cli client.Client,
	mongoCli mongo.Client,
	user *api.User,
	userInfo *mongo.User,
	secret *corev1.Secret) error {
	log := logf.FromContext(ctx)

	if userInfo != nil {
		return nil
	}

	annotationKey := fmt.Sprintf("percona.com/%s-hash", user.Name)

	roles := make([]map[string]interface{}, 0)
	for _, role := range user.Roles {
		roles = append(roles, map[string]interface{}{
			"role": role.Name,
			"db":   role.DB,
		})
	}

	log.Info("Creating user", "user", user.UserID())
	err := mongoCli.CreateUser(ctx, user.DB, user.Name, string(secret.Data[user.PasswordSecretRef.Key]), roles...)
	if err != nil {
		return err
	}

	secret.Annotations[annotationKey] = string(sha256Hash(secret.Data[user.PasswordSecretRef.Key]))
	if err := cli.Update(ctx, secret); err != nil {
		return err
	}

	log.Info("User created", "user", user.UserID())
	return nil
}
