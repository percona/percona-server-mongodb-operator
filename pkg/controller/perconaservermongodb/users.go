package perconaservermongodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/pkg/errors"
	"golang.org/x/sync/errgroup"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func getInternalSecretData(cr *api.PerconaServerMongoDB, secret *corev1.Secret) map[string][]byte {
	m := secret.DeepCopy().Data
	if cr.CompareVersion("1.19.0") >= 0 {
		for k, v := range secret.Data {
			m[k+"_ESCAPED"] = []byte(url.QueryEscape(string(v)))
		}
	}
	return m
}

func (r *ReconcilePerconaServerMongoDB) reconcileUsers(ctx context.Context, cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) error {
	log := logf.FromContext(ctx)

	sysUsersSecretObj := corev1.Secret{}
	err := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.Users,
		},
		&sysUsersSecretObj,
	)
	if err != nil && k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return errors.Wrapf(err, "get sys users secret '%s'", cr.Spec.Secrets.Users)
	}

	if !cr.Spec.PMM.HasSecret(&sysUsersSecretObj) && cr.Spec.PMM.Enabled {
		log.Error(errors.New(fmt.Sprintf(`Can't enable PMM: "%s" or "%s" with "%s" keys don't exist in the secrets, or secrets and internal secrets are out of sync`,
			api.PMMAPIKey, api.PMMUserKey, api.PMMPasswordKey)), "secrets", cr.Spec.Secrets.Users, "internalSecrets", api.InternalUserSecretName(cr))
	}

	secretName := api.InternalUserSecretName(cr)
	internalSysSecretObj := corev1.Secret{}

	err = r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      secretName,
		},
		&internalSysSecretObj,
	)
	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get internal sys users secret")
	}

	if k8serrors.IsNotFound(err) {
		internalSysUsersSecret := sysUsersSecretObj.DeepCopy()
		internalSysUsersSecret.ObjectMeta = metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cr.Namespace,
			Labels:    naming.ClusterLabels(cr),
		}
		if cr.CompareVersion("1.17.0") < 0 {
			internalSysUsersSecret.Labels = nil
		}
		if cr.CompareVersion("1.19.0") >= 0 {
			internalSysUsersSecret.Data = getInternalSecretData(cr, &sysUsersSecretObj)
		}
		err = r.client.Create(ctx, internalSysUsersSecret)
		if err != nil {
			return errors.Wrap(err, "create internal sys users secret")
		}
		return nil
	}

	// we do this check after work with secret objects because in case of upgrade cluster we need to be sure that internal secret exist
	if cr.Status.State != api.AppStateReady {
		return nil
	}

	dataChanged, err := sysUsersSecretDataChanged(cr, &sysUsersSecretObj, &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "check sys users data changes")
	}

	if !dataChanged || cr.Spec.Unmanaged {
		return nil
	}

	logf.FromContext(ctx).Info("Secret data changed. Updating users...")

	containers, err := r.updateSysUsers(ctx, cr, &sysUsersSecretObj, &internalSysSecretObj, repls)
	if err != nil {
		return errors.Wrap(err, "manage sys users")
	}

	if len(containers) > 0 {
		rsPodList, err := r.getMongodPods(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "failed to get mongos pods")
		}

		pods := rsPodList.Items

		if cr.Spec.Sharding.Enabled {
			mongosList, err := r.getMongosPods(ctx, cr)
			if err != nil {
				return errors.Wrap(err, "failed to get mongos pods")
			}

			pods = append(pods, mongosList.Items...)

			cfgPodlist, err := psmdb.GetRSPods(ctx, r.client, cr, api.ConfigReplSetName)
			if err != nil {
				return errors.Wrap(err, "failed to get mongos pods")
			}

			pods = append(pods, cfgPodlist.Items...)
		}

		for _, name := range containers {
			err = r.killcontainer(ctx, pods, name)
			if err != nil {
				return errors.Wrapf(err, "failed to kill %s container", name)
			}
		}
	}

	internalSysSecretObj.Data = sysUsersSecretObj.Data
	if cr.CompareVersion("1.19.0") >= 0 {
		internalSysSecretObj.Data = getInternalSecretData(cr, &sysUsersSecretObj)
	}
	err = r.client.Update(ctx, &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "update internal sys users secret")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) killcontainer(ctx context.Context, pods []corev1.Pod, containerName string) error {
	for _, pod := range pods {
		for _, c := range pod.Spec.Containers {
			if c.Name == containerName {
				logf.FromContext(ctx).Info("Restarting container", "pod", pod.Name, "container", c.Name)

				err := retry.OnError(retry.DefaultBackoff, func(_ error) bool { return true }, func() error {
					stderrBuf := &bytes.Buffer{}

					err := r.clientcmd.Exec(ctx, &pod, containerName, []string{"/bin/sh", "-c", "kill 1"}, nil, nil, stderrBuf, false)
					if err != nil {
						return errors.Wrap(err, "exec command in pod")
					}

					if stderrBuf.Len() != 0 {
						return errors.Errorf("exec command return error: %s", stderrBuf.String())
					}

					return nil
				})
				if err != nil {
					return errors.Wrap(err, "failed to restart container")
				}
			}
		}
	}

	return nil
}

type systemUser struct {
	currName []byte
	name     []byte
	pass     []byte
}

type systemUsers struct {
	currData map[string][]byte // data stored in internal secret
	newData  map[string][]byte // data stored in users secret
	users    []systemUser
}

// add appends user to su.users by given keys if user should be changed
func (su *systemUsers) add(nameKey, passKey string) (changed bool, err error) {
	if len(su.newData[nameKey]) == 0 {
		return false, errors.New("undefined or not exist user name " + nameKey)
	}
	if len(su.newData[passKey]) == 0 {
		return false, errors.New("undefined or not exist user pass " + nameKey)
	}

	// no changes, nothing to do with that user
	if bytes.Equal(su.newData[nameKey], su.currData[nameKey]) &&
		bytes.Equal(su.newData[passKey], su.currData[passKey]) {
		return false, nil
	}
	if nameKey == api.EnvPMMServerUser || passKey == api.EnvPMMServerAPIKey {
		return true, nil
	}
	su.users = append(su.users, systemUser{
		currName: su.currData[nameKey],
		name:     su.newData[nameKey],
		pass:     su.newData[passKey],
	})

	return true, nil
}

func (su *systemUsers) len() int {
	return len(su.users)
}

func (r *ReconcilePerconaServerMongoDB) updateSysUsers(ctx context.Context, cr *api.PerconaServerMongoDB, newUsersSec, currUsersSec *corev1.Secret,
	repls []*api.ReplsetSpec,
) ([]string, error) {
	su := systemUsers{
		currData: currUsersSec.Data,
		newData:  newUsersSec.Data,
	}

	containers := []string{}

	type user struct {
		nameKey, passKey string
	}
	users := []user{
		{
			nameKey: api.EnvMongoDBClusterAdminUser,
			passKey: api.EnvMongoDBClusterAdminPassword,
		},

		{
			nameKey: api.EnvMongoDBClusterMonitorUser,
			passKey: api.EnvMongoDBClusterMonitorPassword,
		},

		{
			nameKey: api.EnvMongoDBBackupUser,
			passKey: api.EnvMongoDBBackupPassword,
		},

		// !!! UserAdmin always must be the last to update since we're using it for the mongo connection
		{
			nameKey: api.EnvMongoDBUserAdminUser,
			passKey: api.EnvMongoDBUserAdminPassword,
		},
	}
	if _, ok := currUsersSec.Data[api.EnvMongoDBDatabaseAdminUser]; cr.CompareVersion("1.13.0") >= 0 && ok {
		users = append([]user{
			{
				nameKey: api.EnvMongoDBDatabaseAdminUser,
				passKey: api.EnvMongoDBDatabaseAdminPassword,
			},
		}, users...)
	}
	if cr.Spec.PMM.Enabled && cr.Spec.PMM.HasSecret(newUsersSec) {
		// insert in front
		if cr.Spec.PMM.ShouldUseAPIKeyAuth(newUsersSec) {
			users = append([]user{
				{
					nameKey: api.EnvPMMServerAPIKey,
					passKey: api.EnvPMMServerAPIKey,
				},
			}, users...)
		} else {
			users = append([]user{
				{
					nameKey: api.EnvPMMServerUser,
					passKey: api.EnvPMMServerPassword,
				},
			}, users...)
		}
	}

	for _, u := range users {
		changed, err := su.add(u.nameKey, u.passKey)
		if err != nil {
			return nil, err
		}

		if changed {
			switch u.nameKey {
			case api.EnvMongoDBBackupUser:
				containers = append(containers, naming.ContainerBackupAgent)
			case api.EnvPMMServerUser, api.EnvPMMServerAPIKey:
				containers = append(containers, "pmm-client")
			}
		}
	}

	if su.len() == 0 {
		return containers, nil
	}

	err := r.updateUsers(ctx, cr, su.users, repls)

	return containers, errors.Wrap(err, "mongo: update system users")
}

func (r *ReconcilePerconaServerMongoDB) updateUsers(ctx context.Context, cr *api.PerconaServerMongoDB, users []systemUser, repls []*api.ReplsetSpec) error {
	grp, gCtx := errgroup.WithContext(ctx)

	for i := range repls {
		replset := repls[i]
		grp.Go(func() error {
			client, err := r.MongoClientWithRole(gCtx, cr, replset, api.RoleUserAdmin)
			if err != nil {
				return errors.Wrap(err, "dial:")
			}

			defer func() {
				if err := client.Disconnect(gCtx); err != nil {
					logf.FromContext(ctx).Error(err, "failed to close connection")
				}
			}()

			for _, user := range users {
				if err := user.updateMongo(gCtx, client); err != nil {
					return errors.Wrapf(err, "update users in mongo for replset %s", replset.Name)
				}
			}
			return nil
		})
	}

	return grp.Wait()
}

func (u *systemUser) updateMongo(ctx context.Context, c mongo.Client) error {
	if bytes.Equal(u.currName, u.name) {
		err := c.UpdateUserPass(ctx, "admin", string(u.name), string(u.pass))
		return errors.Wrapf(err, "change password for user %s", u.name)
	}

	err := c.UpdateUser(ctx, string(u.currName), string(u.name), string(u.pass))
	return errors.Wrapf(err, "update user %s -> %s", u.currName, u.name)
}

func sysUsersSecretDataChanged(cr *api.PerconaServerMongoDB, usersSecret *corev1.Secret, internalSecret *corev1.Secret) (bool, error) {
	newData := getInternalSecretData(cr, usersSecret)
	newDataJSON, err := json.Marshal(newData)
	if err != nil {
		return false, err
	}
	newHash := sha256Hash(newDataJSON)

	oldDataJSON, err := json.Marshal(internalSecret.Data)
	if err != nil {
		return false, err
	}
	oldHash := sha256Hash(oldDataJSON)

	return oldHash != newHash, nil
}

func sha256Hash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
