package perconaservermongodb

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const internalPrefix = "internal-"

func (r *ReconcilePerconaServerMongoDB) reconcileUsers(cr *api.PerconaServerMongoDB) error {
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

	restartSfs, err := r.updateSysUsers(cr, &sysUsersSecretObj, &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "manage sys users")
	}

	internalSysSecretObj.Data = sysUsersSecretObj.Data
	err = r.client.Update(context.TODO(), &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "update internal sys users secret")
	}

	if restartSfs {
		r.sfsTemplateAnnotations["last-applied-secret"] = newSecretDataHash
	}

	return nil
}

type systemUser struct {
	currName []byte
	name     []byte
	pass     []byte
}

type systemUsers struct {
	curr  map[string][]byte
	new   map[string][]byte
	users []systemUser
}

// add checks if user should be changed
func (su *systemUsers) add(name, pass string) (changed bool, err error) {
	if len(su.new[name]) == 0 {
		return false, errors.New("undefined user name " + name)
	}
	if len(su.new[pass]) == 0 {
		return false, errors.New("undefined user pass " + name)
	}

	// no changes, nothing to do with that user
	if bytes.Compare(su.new[name], su.curr[name]) == 0 || bytes.Compare(su.new[pass], su.curr[pass]) == 0 {
		return false, nil
	}

	su.users = append(su.users, systemUser{
		currName: su.curr[name],
		name:     su.new[name],
		pass:     su.new[pass],
	})

	return true, nil
}

func (su *systemUsers) len() int {
	return len(su.users)
}

func (r *ReconcilePerconaServerMongoDB) updateSysUsers(cr *api.PerconaServerMongoDB, usersSec, currUsersSec *corev1.Secret) (restartSfs bool, err error) {
	su := systemUsers{
		curr: currUsersSec.Data,
		new:  usersSec.Data,
	}

	type user struct {
		name, pass  string
		needRestart bool
	}
	users := []user{
		{
			name: envMongoDBClusterAdminUser,
			pass: envMongoDBClusterAdminPassword,
		},
		{
			name: envMongoDBClusterMonitorUser,
			pass: envMongoDBClusterMonitorPassword,
		},
		{
			name:        envMongoDBBackupUser,
			pass:        envMongoDBBackupPassword,
			needRestart: true,
		},
		// !!! UserAdmin always must be the last to update since we're using it for the mongo connection
		{
			name:        envMongoDBUserAdminUser,
			pass:        envMongoDBUserAdminPassword,
			needRestart: true,
		},
	}
	if cr.Spec.PMM.Enabled {
		// insert in front
		users = append([]user{
			{
				name:        envPMMServerUser,
				pass:        envPMMServerPassword,
				needRestart: true,
			},
		}, users...)
	}

	for _, u := range users {
		changed, err := su.add(u.name, u.pass)
		if err != nil {
			return false, err
		}
		if !restartSfs && u.needRestart && changed {
			restartSfs = true
		}
	}

	if su.len() == 0 {
		return false, nil
	}

	err = r.updateUsersPass(cr, su.users, string(currUsersSec.Data[envMongoDBUserAdminUser]), string(currUsersSec.Data[envMongoDBUserAdminPassword]))

	return restartSfs, errors.Wrap(err, "mongo: update system users")
}

func (r *ReconcilePerconaServerMongoDB) updateUsersPass(cr *api.PerconaServerMongoDB, users []systemUser, adminUser, adminPass string) error {
	for i, replset := range cr.Spec.Replsets {
		if i > 0 {
			log.Info("update users: multiple replica sets is not yet supported")
			return nil
		}

		matchLabels := map[string]string{
			"app.kubernetes.io/name":       "percona-server-mongodb",
			"app.kubernetes.io/instance":   cr.Name,
			"app.kubernetes.io/replset":    replset.Name,
			"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
			"app.kubernetes.io/part-of":    "percona-server-mongodb",
		}

		pods := &corev1.PodList{}
		err := r.client.List(context.TODO(),
			pods,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(matchLabels),
			},
		)
		if err != nil {
			return errors.Wrapf(err, "get pods list for replset %s", replset.Name)
		}
		rsAddrs, err := psmdb.GetReplsetAddrs(r.client, cr, replset, pods.Items)
		if err != nil {
			return errors.Wrap(err, "get replset addr")
		}
		client, err := mongo.Dial(rsAddrs, replset.Name, adminUser, adminPass, true)
		if err != nil {
			client, err = mongo.Dial(rsAddrs, replset.Name, adminUser, adminPass, false)
			if err != nil {
				return errors.Wrap(err, "dial:")
			}
		}
		defer client.Disconnect(context.TODO())

		for _, user := range users {
			res := client.Database("admin").RunCommand(context.TODO(), bson.D{{Key: "updateUser", Value: user.name}, {Key: "pwd", Value: user.pass}})
			if res.Err() != nil {
				return errors.Wrap(res.Err(), "change pass")
			}
		}
	}

	return nil
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
