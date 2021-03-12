package perconaservermongodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	"github.com/pkg/errors"
	mongod "go.mongodb.org/mongo-driver/mongo"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

func (r *ReconcilePerconaServerMongoDB) reconcileUsers(cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) error {
	sysUsersSecretObj := corev1.Secret{}
	err := r.client.Get(context.TODO(),
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

	secretName := api.InternalUserSecretName(cr)
	internalSysSecretObj := corev1.Secret{}

	err = r.client.Get(context.TODO(),
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
		}
		err = r.client.Create(context.TODO(), internalSysUsersSecret)
		if err != nil {
			return errors.Wrap(err, "create internal sys users secret")
		}
		return nil
	}

	// we do this check after work with secret objects because in case of upgrade cluster we need to be sure that internal secret exist
	if cr.Status.State != api.AppStateReady {
		return nil
	}

	newSysData, err := json.Marshal(sysUsersSecretObj.Data)
	if err != nil {
		return errors.Wrap(err, "marshal sys secret data")
	}

	newSecretDataHash := sha256Hash(newSysData)
	dataChanged, err := sysUsersSecretDataChanged(newSecretDataHash, &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "check sys users data changes")
	}

	if !dataChanged {
		return nil
	}

	containers, err := r.updateSysUsers(cr, &sysUsersSecretObj, &internalSysSecretObj, repls)
	if err != nil {
		return errors.Wrap(err, "manage sys users")
	}

	if len(containers) > 0 {
		rsPodList, err := r.getMongodPods(cr)
		if err != nil {
			return errors.Wrap(err, "failed to get mongos pods")
		}

		pods := rsPodList.Items

		if cr.Spec.Sharding.Enabled {
			mongosList, err := r.getMongosPods(cr)
			if err != nil {
				return errors.Wrap(err, "failed to get mongos pods")
			}

			pods = append(pods, mongosList.Items...)

			cfgPodlist, err := r.getRSPods(cr, api.ConfigReplSetName)
			if err != nil {
				return errors.Wrap(err, "failed to get mongos pods")
			}

			pods = append(pods, cfgPodlist.Items...)
		}

		for _, name := range containers {
			err = r.killcontainer(pods, name)
			if err != nil {
				return errors.Wrapf(err, "failed to kill %s container", name)
			}
		}
	}

	internalSysSecretObj.Data = sysUsersSecretObj.Data
	err = r.client.Update(context.TODO(), &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "update internal sys users secret")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) killcontainer(pods []corev1.Pod, containerName string) error {
	for _, pod := range pods {
		for _, c := range pod.Spec.Containers {
			if c.Name == containerName {
				log.Info("restart container", "pod", pod.Name, "container", c.Name)

				stderrBuf := &bytes.Buffer{}

				err := r.clientcmd.Exec(&pod, containerName, []string{"/bin/sh", "-c", "kill 1"}, nil, nil, stderrBuf, false)
				if err != nil {
					return errors.Wrap(err, "exec command in pod")
				}

				if stderrBuf.Len() != 0 {
					return errors.Errorf("exec command return error: %s", stderrBuf.String())
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
	if nameKey == envPMMServerUser {
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

func (r *ReconcilePerconaServerMongoDB) updateSysUsers(cr *api.PerconaServerMongoDB, newUsersSec, currUsersSec *corev1.Secret,
	repls []*api.ReplsetSpec) ([]string, error) {
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
			nameKey: envMongoDBClusterAdminUser,
			passKey: envMongoDBClusterAdminPassword,
		},

		{
			nameKey: envMongoDBClusterMonitorUser,
			passKey: envMongoDBClusterMonitorPassword,
		},

		{
			nameKey: envMongoDBBackupUser,
			passKey: envMongoDBBackupPassword,
		},

		// !!! UserAdmin always must be the last to update since we're using it for the mongo connection
		{
			nameKey: envMongoDBUserAdminUser,
			passKey: envMongoDBUserAdminPassword,
		},
	}
	if cr.Spec.PMM.Enabled {
		// insert in front
		users = append([]user{
			{
				nameKey: envPMMServerUser,
				passKey: envPMMServerPassword,
			},
		}, users...)
	}

	for _, u := range users {
		changed, err := su.add(u.nameKey, u.passKey)
		if err != nil {
			return nil, err
		}

		if changed {
			switch u.nameKey {
			case envMongoDBBackupUser:
				containers = append(containers, "backup-agent")
			case envPMMServerUser:
				containers = append(containers, "pmm-client")
			}
		}
	}

	if su.len() == 0 {
		return containers, nil
	}

	err := r.updateUsers(cr, su.users, repls)

	return containers, errors.Wrap(err, "mongo: update system users")
}

func (r *ReconcilePerconaServerMongoDB) updateUsers(cr *api.PerconaServerMongoDB, users []systemUser, repls []*api.ReplsetSpec) error {
	for _, replset := range repls {
		matchLabels := map[string]string{
			"app.kubernetes.io/name":       "percona-server-mongodb",
			"app.kubernetes.io/instance":   cr.Name,
			"app.kubernetes.io/replset":    replset.Name,
			"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
			"app.kubernetes.io/part-of":    "percona-server-mongodb",
		}

		pods := corev1.PodList{}
		err := r.client.List(context.TODO(),
			&pods,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(matchLabels),
			},
		)

		if err != nil {
			return errors.Wrap(err, "failed to get pods for RS")
		}

		client, err := r.mongoClientWithRole(cr, replset.Name, replset.Expose.Enabled, pods.Items, roleUserAdmin)
		if err != nil {
			return errors.Wrap(err, "dial:")
		}

		defer func() {
			err := client.Disconnect(context.TODO())
			if err != nil {
				log.Error(err, "failed to close connection")
			}
		}()

		for _, user := range users {
			err := user.updateMongo(client)
			if err != nil {
				return errors.Wrapf(err, "update users in mongo for replset %s", replset.Name)
			}
		}
	}

	return nil
}

func (u *systemUser) updateMongo(c *mongod.Client) error {
	if bytes.Equal(u.currName, u.name) {
		err := mongo.UpdateUserPass(context.TODO(), c, string(u.name), string(u.pass))
		return errors.Wrapf(err, "change password for user %s", u.name)
	}

	err := mongo.UpdateUser(context.TODO(), c, string(u.currName), string(u.name), string(u.pass))
	return errors.Wrapf(err, "update user %s -> %s", u.currName, u.name)
}

func sysUsersSecretDataChanged(newHash string, usersSecret *corev1.Secret) (bool, error) {
	secretData, err := json.Marshal(usersSecret.Data)
	if err != nil {
		return false, err
	}
	oldHash := sha256Hash(secretData)

	return oldHash != newHash, nil
}

func sha256Hash(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
