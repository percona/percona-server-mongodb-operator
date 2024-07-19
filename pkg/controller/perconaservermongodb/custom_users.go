package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
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

	for _, user := range cr.Spec.Users {
		sec, err := getUserSecret(ctx, r.client, cr, user.PasswordSecretRef.Name)
		if err != nil {
			log.Error(err, "failed to get user secret", "user", user)
			continue
		}

		newHash := sha256Hash(sec.Data[user.PasswordSecretRef.Key])

		hash, ok := sec.Annotations["percona.com/user-hash"]
		if ok && hash == newHash {
			continue
		}

		if !ok {
			log.Info("CCCCCCCCCC NOT OK")
		}


		if hash != newHash {
			log.Info("AAAAAAAAA User password changed", "user", user.Name)
		}

		// not ok - user doesn't exist
		// ok but hash is different - user password changed

		// userInfo, err := cli.GetUserInfo(ctx, user.Name)
		// if err != nil {
		// 	errors.Wrap(err, "get user info")
		// 	continue
		// }

		// if userInfo != nil && hash == newHash {
		// 	continue
		// }

		roles := make([]map[string]interface{}, 0)
		for _, role := range user.Roles {
			roles = append(roles, map[string]interface{}{
				"role": role.Name,
				"db":   role.Db,
			})

		}

		log.Info("XXXXXXX creating user", "user", user.Name)
		err = cli.CreateUser(ctx, user.Name, string(sec.Data[user.PasswordSecretRef.Key]), roles...)
		if err != nil {
			log.Error(err, "failed to create user %s", user.Name)
			continue
		}

		if sec.Annotations == nil {
			sec.Annotations = make(map[string]string)
		}
		sec.Annotations["percona.com/user-hash"] = string(newHash)
		if err := r.client.Update(ctx, &sec); err != nil {
			log.Error(err, "update ca secret")
			continue
		}

		log.Info("ZZZZZZZZZZZZ User created", "user", user.Name)
	}

	return nil
}
