package perconaservermongodb

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	s "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
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
	var mongoCli mongo.Client
	if cr.Spec.Sharding.Enabled {
		mongoCli, err = r.mongosClientWithRole(ctx, cr, api.RoleUserAdmin)
	} else {
		mongoCli, err = r.mongoClientWithRole(ctx, cr, cr.Spec.Replsets[0], api.RoleUserAdmin)
	}
	if err != nil {
		return errors.Wrap(err, "failed to get mongo client")
	}
	defer func() {
		err := mongoCli.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close mongo connection")
		}
	}()

	handleRoles(ctx, cr, mongoCli)

	err = handleUsers(ctx, cr, mongoCli, r.client, "")
	if err != nil {
		return errors.Wrap(err, "handle users")
	}

	// For sharded clusters, custom users are created via mongos which stores them
	// on the config server only. Individual shard replica sets maintain their own
	// local admin databases and are unaware of users created at the cluster level.
	// Only clusterSuperAdmin needs direct shard access for platform operations;
	// other custom users should remain cluster-level only (accessed via mongos).
	if cr.Spec.Sharding.Enabled {
		shardUsers := filterShardUsers(cr.Spec.Users)
		if len(shardUsers) > 0 {
			shardCR := cr.DeepCopy()
			shardCR.Spec.Users = shardUsers

			for i := range cr.Spec.Replsets {
				rs := cr.Spec.Replsets[i]

				// Guard: check if all shard users already have matching password
				// hashes for this replset's annotation key. If so, skip connecting
				// entirely — this is the steady-state fast path that avoids opening
				// a TLS+SCRAM connection to every shard on every 5-second reconcile.
				if shardUsersUpToDate(ctx, r.client, cr, shardUsers, rs.Name) {
					continue
				}

				shardCli, err := r.mongoClientWithRole(ctx, cr, rs, api.RoleUserAdmin)
				if err != nil {
					log.Error(err, "failed to get mongo client for shard", "replset", rs.Name)
					continue
				}

				if err := handleUsers(ctx, shardCR, shardCli, r.client, rs.Name); err != nil {
					log.Error(err, "failed to handle users on shard", "replset", rs.Name)
				}

				if err := shardCli.Disconnect(ctx); err != nil {
					log.Error(err, "failed to close shard mongo connection", "replset", rs.Name)
				}
			}
		}
	}

	return nil
}

// filterShardUsers returns only the users that need to be created directly on
// each shard replica set. Currently this is limited to clusterSuperAdmin which
// requires direct shard access for platform operations (compaction, backups, etc).
func filterShardUsers(users []api.User) []api.User {
	var filtered []api.User
	for i := range users {
		if users[i].Name == "clusterSuperAdmin" {
			filtered = append(filtered, users[i])
		}
	}
	return filtered
}

// shardUsersUpToDate returns true if all shard users already have password hashes
// committed for the given replset. This is the steady-state check that avoids
// opening a mongo connection to the shard when nothing has changed.
//
// External-DB users have no password and are excluded from this check (they are
// currently not included in filterShardUsers, but this is defensive).
//
// Returns false (triggering a connection) if:
//   - Any user's secret cannot be read
//   - Any user's per-replset annotation is missing or doesn't match the current hash
func shardUsersUpToDate(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, users []api.User, rsName string) bool {
	log := logf.FromContext(ctx)

	for i := range users {
		user := &users[i]

		// External-DB users have no password — skip them in the hash check.
		if user.IsExternalDB() {
			continue
		}

		passKey := user.Name
		if user.PasswordSecretRef != nil && user.PasswordSecretRef.Key != "" {
			passKey = user.PasswordSecretRef.Key
		}

		sec, err := getCustomUserSecret(ctx, cl, cr, user, passKey)
		if err != nil {
			log.V(1).Info("Cannot read secret for shard guard, will connect", "user", user.Name, "replset", rsName)
			return false
		}

		annotationKey := fmt.Sprintf("percona.com/%s-%s-%s-hash", cr.Name, user.Name, rsName)
		currentHash, ok := sec.Annotations[annotationKey]
		if !ok {
			return false
		}

		if currentHash != sha256Hash(sec.Data[passKey]) {
			return false
		}
	}

	return true
}

func handleUsers(ctx context.Context, cr *api.PerconaServerMongoDB, mongoCli mongo.Client, client client.Client, annotationKeySuffix string) error {
	log := logf.FromContext(ctx)

	if len(cr.Spec.Users) == 0 {
		return nil
	}

	systemUserNames, err := fetchSystemUserNames(ctx, cr, client)
	if err != nil {
		return err
	}

	uniqueUserNames := make(map[string]struct{}, len(cr.Spec.Users))

	for _, user := range cr.Spec.Users {
		err := validateUser(&user, systemUserNames, uniqueUserNames)
		if err != nil {
			log.Error(err, "invalid user", "user", user)
			continue
		}

		userInfo, err := mongoCli.GetUserInfo(ctx, user.Name, user.DB)
		if err != nil {
			log.Error(err, "get user info")
			continue
		}

		if user.IsExternalDB() && userInfo == nil {
			err = createExternalUser(ctx, mongoCli, &user)
			if err != nil {
				return errors.Wrapf(err, "create user %s", user.Name)
			}
			continue
		}

		userSecretPassKey := user.Name
		if user.PasswordSecretRef != nil {
			userSecretPassKey = user.PasswordSecretRef.Key
		}

		sec, err := getCustomUserSecret(ctx, client, cr, &user, userSecretPassKey)
		if err != nil {
			log.Error(err, "failed to get user secret", "user", user)
			continue
		}

		var annotationKey string
		if annotationKeySuffix == "" {
			annotationKey = fmt.Sprintf("percona.com/%s-%s-hash", cr.Name, user.Name)
		} else {
			annotationKey = fmt.Sprintf("percona.com/%s-%s-%s-hash", cr.Name, user.Name, annotationKeySuffix)
		}

		if userInfo == nil && !user.IsExternalDB() {
			err = createUser(ctx, client, mongoCli, &user, sec, annotationKey, userSecretPassKey)
			if err != nil {
				return errors.Wrapf(err, "create user %s", user.Name)
			}
			continue
		}

		err = updatePass(ctx, client, mongoCli, &user, userInfo, sec, annotationKey, userSecretPassKey)
		if err != nil {
			log.Error(err, "update user pass", "user", user.Name)
			continue
		}

		err = updateRoles(ctx, mongoCli, &user, userInfo)
		if err != nil {
			log.Error(err, "update user roles", "user", user.Name)
			continue
		}
	}

	return nil
}

func validateUser(user *api.User, sysUserNames, uniqueUserNames map[string]struct{}) error {
	if sysUserNames == nil || uniqueUserNames == nil {
		return errors.New("invalid sys or unique usernames config")
	}

	if _, reserved := sysUserNames[user.Name]; reserved {
		return fmt.Errorf("creating user with reserved user name %s is forbidden", user.Name)
	}

	if _, exists := uniqueUserNames[user.Name]; exists {
		return fmt.Errorf("username %s should be unique", user.Name)
	}
	uniqueUserNames[user.Name] = struct{}{}

	if len(user.Roles) == 0 {
		return fmt.Errorf("user %s must have at least one role", user.Name)
	}

	if user.DB == "" {
		user.DB = "admin"
	}

	if user.PasswordSecretRef != nil && user.PasswordSecretRef.Key == "" {
		user.PasswordSecretRef.Key = "password"
	}

	return nil
}

func fetchSystemUserNames(ctx context.Context, cr *api.PerconaServerMongoDB, client client.Client) (map[string]struct{}, error) {
	sysUsersSecret := corev1.Secret{}
	err := client.Get(ctx, types.NamespacedName{
		Namespace: cr.Namespace,
		Name:      api.InternalUserSecretName(cr),
	}, &sysUsersSecret)

	if err != nil && !k8serrors.IsNotFound(err) {
		return nil, errors.Wrap(err, "get internal sys users secret")
	}

	return sysUserNames(sysUsersSecret), nil
}

func handleRoles(ctx context.Context, cr *api.PerconaServerMongoDB, cli mongo.Client) {
	log := logf.FromContext(ctx)
	if len(cr.Spec.Roles) == 0 {
		return
	}

	for _, role := range cr.Spec.Roles {
		roleInfo, err := cli.GetRole(ctx, role.DB, role.Role)
		if err != nil {
			log.Error(err, "get role info", "role", role.Role)
			continue
		}

		mr, err := toMongoRoleModel(role)
		if err != nil {
			log.Error(err, "to mongo role model", "role", role.Role)
			continue
		}

		if roleInfo == nil {
			log.Info("Creating role", "role", role.Role)
			err := cli.CreateRole(ctx, role.DB, *mr)
			if err != nil {
				log.Error(err, "create role", "role", role.Role)
				continue
			}
			log.Info("Role created", "role", role.Role)
			continue
		}

		if rolesChanged(mr, roleInfo) {
			log.Info("Updating role", "role", role.Role)
			err := cli.UpdateRole(ctx, role.DB, *mr)
			if err != nil {
				log.Error(err, "update role %s", role.Role)
				continue
			}
			log.Info("Role updated", "role", role.Role)
		}
	}
}

func rolesChanged(r1, r2 *mongo.Role) bool {
	if len(r1.Privileges) != len(r2.Privileges) {
		return true
	}
	if len(r1.AuthenticationRestrictions) != len(r2.AuthenticationRestrictions) {
		return true
	}
	if len(r1.Roles) != len(r2.Roles) {
		return true
	}

	opts := cmp.Options{
		cmpopts.SortSlices(func(x, y string) bool { return x < y }),
		cmpopts.EquateEmpty(),
	}

	if !cmp.Equal(r1.Privileges, r2.Privileges, opts) {
		return true
	}
	if !cmp.Equal(r1.AuthenticationRestrictions, r2.AuthenticationRestrictions, opts) {
		return true
	}
	if !cmp.Equal(r1.Roles, r2.Roles, opts) {
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
			rp.Resource["cluster"] = *p.Resource.Cluster
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

// sysUserNames returns a set of system usernames from the sysUsersSecret.
func sysUserNames(sysUsersSecret corev1.Secret) map[string]struct{} {
	names := make(map[string]struct{}, len(sysUsersSecret.Data))
	for k, v := range sysUsersSecret.Data {
		if strings.Contains(k, "_USER") {
			names[string(v)] = struct{}{}
		}
	}
	return names
}

func updatePass(
	ctx context.Context,
	cli client.Client,
	mongoCli mongo.Client,
	user *api.User,
	userInfo *mongo.User,
	secret *corev1.Secret,
	annotationKey, passKey string) error {
	log := logf.FromContext(ctx)

	if userInfo == nil || user.IsExternalDB() {
		return nil
	}

	newHash := sha256Hash(secret.Data[passKey])

	hash, ok := secret.Annotations[annotationKey]
	if ok && hash == newHash {
		return nil
	}

	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	log.Info("User password changed, updating it.", "user", user.UserID())

	err := mongoCli.UpdateUserPass(ctx, user.DB, user.Name, string(secret.Data[passKey]))
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

// roleKey is a comparable projection of a mongo.Role containing only the fields
// that matter for user-role assignment comparison (DB and Role name). MongoDB
// does not guarantee ordering of roles in usersInfo output, and the returned
// Role structs may have extra fields (Roles, Privileges, AuthenticationRestrictions)
// populated that the desired state never sets. Comparing only {DB, Role} avoids
// false positives from both ordering differences and extra-field presence.
type roleKey struct {
	DB   string
	Role string
}

func updateRoles(ctx context.Context, mongoCli mongo.Client, user *api.User, userInfo *mongo.User) error {
	log := logf.FromContext(ctx)

	if userInfo == nil {
		return nil
	}

	desiredRoles := make([]mongo.Role, 0, len(user.Roles))
	desiredKeys := make([]roleKey, 0, len(user.Roles))
	for _, role := range user.Roles {
		desiredRoles = append(desiredRoles, mongo.Role{DB: role.DB, Role: role.Name})
		desiredKeys = append(desiredKeys, roleKey{DB: role.DB, Role: role.Name})
	}

	currentKeys := make([]roleKey, 0, len(userInfo.Roles))
	for _, role := range userInfo.Roles {
		currentKeys = append(currentKeys, roleKey{DB: role.DB, Role: role.Role})
	}

	opts := cmp.Options{
		cmpopts.SortSlices(func(a, b roleKey) bool {
			if a.DB != b.DB {
				return a.DB < b.DB
			}
			return a.Role < b.Role
		}),
		cmpopts.EquateEmpty(),
	}

	if cmp.Equal(currentKeys, desiredKeys, opts...) {
		return nil
	}

	log.Info("User roles changed, updating them.", "user", user.UserID())
	err := mongoCli.UpdateUserRoles(ctx, user.DB, user.Name, desiredRoles)
	if err != nil {
		return err
	}

	return nil
}

// createExternalUser creates a user with $external database authentication method.
func createExternalUser(ctx context.Context, mongoCli mongo.Client, user *api.User) error {
	log := logf.FromContext(ctx)

	roles := make([]mongo.Role, 0)
	for _, role := range user.Roles {
		roles = append(roles, mongo.Role{DB: role.DB, Role: role.Name})
	}

	log.Info("Creating user", "user", user.UserID())
	err := mongoCli.CreateUser(ctx, user.DB, user.Name, "", roles...)
	if err != nil {
		return err
	}

	log.Info("User created", "user", user.UserID())
	return nil
}

func createUser(
	ctx context.Context,
	cli client.Client,
	mongoCli mongo.Client,
	user *api.User,
	secret *corev1.Secret,
	annotationKey, passKey string) error {
	log := logf.FromContext(ctx)

	roles := make([]mongo.Role, 0)
	for _, role := range user.Roles {
		roles = append(roles, mongo.Role{DB: role.DB, Role: role.Name})
	}

	log.Info("Creating user", "user", user.UserID())
	err := mongoCli.CreateUser(ctx, user.DB, user.Name, string(secret.Data[passKey]), roles...)
	if err != nil {
		return err
	}

	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}

	secret.Annotations[annotationKey] = string(sha256Hash(secret.Data[passKey]))
	if err := cli.Update(ctx, secret); err != nil {
		return err
	}

	log.Info("User created", "user", user.UserID())
	return nil
}

// getCustomUserSecret gets secret by name defined by `user.PasswordSecretRef.Name` or returns a secret
// with newly generated password if name matches defaultName
func getCustomUserSecret(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB, user *api.User, passKey string) (*corev1.Secret, error) {
	log := logf.FromContext(ctx)

	if user.IsExternalDB() {
		return nil, nil
	}

	defaultSecretName := fmt.Sprintf("%s-custom-user-secret", cr.Name)

	secretName := defaultSecretName
	if user.PasswordSecretRef != nil {
		secretName = user.PasswordSecretRef.Name
	}

	secret := &corev1.Secret{}
	err := cl.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cr.Namespace}, secret)

	if err != nil && secretName != defaultSecretName {
		return nil, errors.Wrap(err, "failed to get user secret")
	}

	if err != nil && !k8serrors.IsNotFound(err) && secretName == defaultSecretName {
		return nil, errors.Wrap(err, "failed to get user secret")
	}

	if err != nil && k8serrors.IsNotFound(err) {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: cr.Namespace,
			},
		}

		pass, err := s.GeneratePassword()
		if err != nil {
			return nil, errors.Wrap(err, "generate custom user password")
		}

		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}
		secret.Data[passKey] = pass

		err = cl.Create(ctx, secret)
		if err != nil {
			return nil, errors.Wrap(err, "create custom users secret")
		}

		log.Info("Created custom user secrets", "secrets", secret.Name)
	}

	_, hasPass := secret.Data[passKey]
	if !hasPass && secretName == defaultSecretName {
		pass, err := s.GeneratePassword()
		if err != nil {
			return nil, errors.Wrap(err, "generate custom user password")
		}

		if secret.Data == nil {
			secret.Data = make(map[string][]byte)
		}

		secret.Data[passKey] = pass

		err = cl.Update(ctx, secret)
		if err != nil {
			return nil, errors.Wrap(err, "failed to update user secret")
		}
		// given that the secret was updated, the password now exists
		hasPass = true
	}

	// pass key should be present in the user provided secret
	if !hasPass {
		return nil, errors.New("password key not found in secret")
	}

	return secret, nil
}
