package perconaservermongodb

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
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
		cli, err = r.mongoClientWithRole(ctx, cr, cr.Spec.Replsets[0], api.RoleUserAdmin)
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

	if len(cr.Spec.Users) == 0 {
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

		annotationKey := fmt.Sprintf("percona.com/%s-%s-hash", cr.Name, user.Name)

		if userInfo == nil {
			err = createUser(ctx, r.client, cli, &user, &sec, annotationKey)
			if err != nil {
				return errors.Wrapf(err, "create user %s", user.Name)
			}
			continue
		}

		err = updatePass(ctx, r.client, cli, &user, userInfo, &sec, annotationKey)
		if err != nil {
			log.Error(err, "update user pass", "user", user.Name)
			continue
		}

		err = updateRoles(ctx, cli, &user, userInfo)
		if err != nil {
			log.Error(err, "update user roles", "user", user.Name)
			continue
		}
	}

	return nil
}

func handleRoles(ctx context.Context, cr *api.PerconaServerMongoDB, cli mongo.Client) error {
	log := logf.FromContext(ctx)
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
			log.Info("Creating role", "role", role.Role)
			err := cli.CreateRole(ctx, role.DB, *mr)
			if err != nil {
				return errors.Wrapf(err, "create role %s", role.Role)
			}
			log.Info("Role created", "role", role.Role)
			continue
		}

		if rolesChanged(mr, roleInfo) {
			log.Info("Updating role", "role", role.Role)
			err := cli.UpdateRole(ctx, role.DB, *mr)
			if err != nil {
				return errors.Wrapf(err, "update role %s", role.Role)
			}
			log.Info("Role updated", "role", role.Role)
		}
	}

	return nil
}

func rolesChanged(r1, r2 *mongo.Role) bool {

	log := log.FromContext(context.TODO())

	log.Info("AAAAAAAAAAAAAAAAA CR ROLEEE", "role", r1)
	log.Info("AAAAAAAAAAAAAAAAA DB ROLEEE", "role", r2)

	opts := cmp.Options{
		cmpopts.SortSlices(func(x, y string) bool { return x < y }),
		cmpopts.EquateEmpty(),
	}

	if len(r1.Privileges) != len(r2.Privileges) {
		log.Info("AAAAAAAAAAAAAAA")
		return true
	}

	if !cmp.Equal(r1.Privileges, r2.Privileges, opts) {
		log.Info("AAAAAAAAAAAAAAABBBBBBBBBBB")
		log.Info("AAAAAAAAAAAAAAABBBBBBBBBBB CR PRIV", "role", r1.Privileges)
		log.Info("AAAAAAAAAAAAAAABBBBBBBBBBB DB PRIV", "role", r2.Privileges)
		return true
	}

	// if privilegesChanged(r1.Privileges, r2.Privileges) {
	// 	log.Info("AAAAAAAAAAAAAAA")
	// 	return true
	// }

	if len(r1.AuthenticationRestrictions) != len(r2.AuthenticationRestrictions) {
		log.Info("BBBBBBBBBBBBBBBBBBBB")
		return true
	}

	// opts := cmp.Options{
	// 	cmpopts.SortSlices(func(x, y string) bool { return x < y }),
	// 	cmpopts.EquateEmpty(),
	// }

	if !cmp.Equal(r1.AuthenticationRestrictions, r2.AuthenticationRestrictions, opts) {
		log.Info("CCCCCCCCCCCCCCCCCCCCCC")
		return true
	}

	if len(r1.Roles) != len(r2.Roles) {
		log.Info("DDDDDDDDDDDDDDDDDDD")
		return true
	}

	if !cmp.Equal(r1.Roles, r2.Roles, opts) {
		log.Info("EEEEEEEEEEEEEEEEEEEE")
		return true
	}

	return false
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
	} else {
		mr.AuthenticationRestrictions = nil
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
	secret *corev1.Secret,
	annotationKey string) error {
	log := logf.FromContext(ctx)

	if userInfo == nil {
		return nil
	}

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
	secret *corev1.Secret,
	annotationKey string) error {
	log := logf.FromContext(ctx)

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

	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	secret.Annotations[annotationKey] = string(sha256Hash(secret.Data[user.PasswordSecretRef.Key]))
	if err := cli.Update(ctx, secret); err != nil {
		return err
	}

	log.Info("User created", "user", user.UserID())
	return nil
}
