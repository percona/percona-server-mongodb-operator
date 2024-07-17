package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func (r *ReconcilePerconaServerMongoDB) reconcileCustomUsers(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	if cr.Spec.Users == nil || len(cr.Spec.Users) == 0 {
		return nil
	}

	if cr.Status.State != api.AppStateReady {
		return nil
	}

	log := logf.FromContext(ctx)

	// TODO: this could be good for a sharded cluster, for non sharded not so good
	// - Should we create custom users the same way we create system users?
	// - - Sys users are created in `reconileCluster` for every replset.
	cli, err := r.mongosClientWithRole(ctx, cr, api.RoleUserAdmin)
	if err != nil {
		return errors.Wrap(err, "get mongos client")
	}
	defer func() {
		err := cli.Disconnect(ctx)
		if err != nil {
			log.Error(err, "failed to close mongo connection")
		}
	}()

	for _, user := range cr.Spec.Users {

		// TODO: validate user
		// - collect all invalid users and return
		// - or return on first invalid user

		sec, err := getUserSecret(ctx, r.client, cr, user.PasswordSecretRef.Name)
		if err != nil {
			log.Error(err, "failed to get user secret", "user", user)
			continue
		}
		userInfo, err := cli.GetUserInfo(ctx, user.Name)
		if err != nil {
			return errors.Wrap(err, "get user info")
		}
		if userInfo == nil {
			roles := make([]map[string]interface{}, 0)
			for _, role := range user.Roles {
				roles = append(roles, map[string]interface{}{
					"role": role.Name,
					"db":   role.Db,
				})

				err = cli.CreateUser(ctx, user.Name, string(sec.Data[user.PasswordSecretRef.Key]), roles...)
				if err != nil {
					return errors.Wrapf(err, "failed to create user %s", user.Name)
				}
			}
		}
	}

	return nil
}

func validateUsers(client client.Client, users []api.User) error {


	return nil
}
