package perconaservermongodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

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

const internalPrefix = "internal-"

func (r *ReconcilePerconaServerMongoDB) reconcileUsers(cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) (sfsTemplateAnn, mongosTemplateAnn map[string]string, err error) {
	sysUsersSecretObj := corev1.Secret{}
	err = r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.Users,
		},
		&sysUsersSecretObj,
	)
	if err != nil && k8serrors.IsNotFound(err) {
		return nil, nil, nil
	} else if err != nil {
		return nil, nil, errors.Wrapf(err, "get sys users secret '%s'", cr.Spec.Secrets.Users)
	}

	secretName := internalPrefix + cr.Name + "-users"
	internalSysSecretObj := corev1.Secret{}

	err = r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      secretName,
		},
		&internalSysSecretObj,
	)
	if err != nil && !k8serrors.IsNotFound(err) {
		return nil, nil, errors.Wrap(err, "get internal sys users secret")
	}

	if k8serrors.IsNotFound(err) {
		internalSysUsersSecret := sysUsersSecretObj.DeepCopy()
		internalSysUsersSecret.ObjectMeta = metav1.ObjectMeta{
			Name:      secretName,
			Namespace: cr.Namespace,
		}
		err = r.client.Create(context.TODO(), internalSysUsersSecret)
		if err != nil {
			return nil, nil, errors.Wrap(err, "create internal sys users secret")
		}
		return nil, nil, nil
	}

	// we do this check after work with secret objects because in case of upgrade cluster we need to be sure that internal secret exist
	if cr.Status.State != api.AppStateReady {
		return nil, nil, nil
	}

	newSysData, err := json.Marshal(sysUsersSecretObj.Data)
	if err != nil {
		return nil, nil, errors.Wrap(err, "marshal sys secret data")
	}
	newSecretDataHash := sha256Hash(newSysData)
	dataChanged, err := sysUsersSecretDataChanged(newSecretDataHash, &internalSysSecretObj)
	if err != nil {
		return nil, nil, errors.Wrap(err, "check sys users data changes")
	}

	if !dataChanged {
		return nil, nil, nil
	}

	restartSfs, restartMongos, err := r.updateSysUsers(cr, &sysUsersSecretObj, &internalSysSecretObj, repls)
	if err != nil {
		return nil, nil, errors.Wrap(err, "manage sys users")
	}

	internalSysSecretObj.Data = sysUsersSecretObj.Data
	err = r.client.Update(context.TODO(), &internalSysSecretObj)
	if err != nil {
		return nil, nil, errors.Wrap(err, "update internal sys users secret")
	}
	ann := map[string]string{
		"last-applied-secret":    newSecretDataHash,
		"last-applied-secret-ts": time.Now().UTC().String(),
	}
	if restartSfs {
		sfsTemplateAnn = ann
	}
	if restartMongos {
		mongosTemplateAnn = ann
	}

	return
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

func (r *ReconcilePerconaServerMongoDB) updateSysUsers(cr *api.PerconaServerMongoDB, newUsersSec, currUsersSec *corev1.Secret, repls []*api.ReplsetSpec) (restartSfs, restartMongos bool, err error) {
	su := systemUsers{
		currData: currUsersSec.Data,
		newData:  newUsersSec.Data,
	}

	type user struct {
		nameKey, passKey  string
		needRestartSfs    bool
		needRestartMongos bool
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
			nameKey:        envMongoDBBackupUser,
			passKey:        envMongoDBBackupPassword,
			needRestartSfs: true,
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
				nameKey:        envPMMServerUser,
				passKey:        envPMMServerPassword,
				needRestartSfs: true,
			},
		}, users...)
	}

	for _, u := range users {
		changed, err := su.add(u.nameKey, u.passKey)
		if err != nil {
			return false, false, err
		}
		if u.needRestartSfs && changed {
			restartSfs = true
		}
		if u.needRestartMongos && changed {
			restartMongos = true
		}
	}

	if su.len() == 0 {
		return false, false, nil
	}

	err = r.updateUsers(cr, su.users, repls)

	return restartSfs, restartMongos, errors.Wrap(err, "mongo: update system users")
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
