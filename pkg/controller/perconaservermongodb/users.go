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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const internalPrefix = "internal-"

type sysUser struct {
	Name string `yaml:"username"`
	Pass string `yaml:"password"`
}

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

	restartSfs, err := r.manageSysUsers(cr, &sysUsersSecretObj, &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "manage sys users")
	}

	internalSysSecretObj.Data = sysUsersSecretObj.Data
	err = r.client.Update(context.TODO(), &internalSysSecretObj)
	if err != nil {
		return errors.Wrap(err, "update internal sys users secret")
	}

	if restartSfs {
		err = r.restartStatefulset(cr, newSecretDataHash)
		if err != nil {
			return errors.Wrap(err, "restart statefulset")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) manageSysUsers(cr *api.PerconaServerMongoDB, sysUsersSecretObj, internalSysSecretObj *corev1.Secret) (bool, error) {
	var (
		sysUsers  []sysUser
		userAdmin *sysUser
	)
	restartSfs := false
	for key := range sysUsersSecretObj.Data {
		if bytes.Compare(sysUsersSecretObj.Data[key], internalSysSecretObj.Data[key]) == 0 {
			continue
		}
		switch key {
		case "MONGODB_BACKUP_PASSWORD":
			sysUsers = append(sysUsers, sysUser{
				Name: string(sysUsersSecretObj.Data["MONGODB_BACKUP_USER"]),
				Pass: string(sysUsersSecretObj.Data["MONGODB_BACKUP_PASSWORD"]),
			},
			)
			restartSfs = true
		case "MONGODB_CLUSTER_ADMIN_PASSWORD":
			sysUsers = append(sysUsers, sysUser{
				Name: string(sysUsersSecretObj.Data["MONGODB_CLUSTER_ADMIN_USER"]),
				Pass: string(sysUsersSecretObj.Data["MONGODB_CLUSTER_ADMIN_PASSWORD"]),
			},
			)
		case "MONGODB_CLUSTER_MONITOR_PASSWORD":
			sysUsers = append(sysUsers, sysUser{
				Name: string(sysUsersSecretObj.Data["MONGODB_CLUSTER_MONITOR_USER"]),
				Pass: string(sysUsersSecretObj.Data["MONGODB_CLUSTER_MONITOR_PASSWORD"]),
			},
			)
		case "MONGODB_USER_ADMIN_PASSWORD":
			userAdmin = &sysUser{
				Name: string(sysUsersSecretObj.Data["MONGODB_USER_ADMIN_USER"]),
				Pass: string(sysUsersSecretObj.Data["MONGODB_USER_ADMIN_PASSWORD"]),
			}
		case "PMM_SERVER_PASSWORD":
			sysUsers = append(sysUsers, sysUser{
				Name: string(sysUsersSecretObj.Data["PMM_SERVER_USER"]),
				Pass: string(sysUsersSecretObj.Data["PMM_SERVER_PASSWORD"]),
			},
			)
			restartSfs = true
		}
	}
	if userAdmin != nil {
		sysUsers = append(sysUsers, *userAdmin)
	}
	if len(sysUsers) > 0 {
		err := r.updateUsersPass(cr, sysUsers, string(internalSysSecretObj.Data["MONGODB_USER_ADMIN_USER"]), string(internalSysSecretObj.Data["MONGODB_USER_ADMIN_PASSWORD"]), internalSysSecretObj)
		if err != nil {
			return restartSfs, errors.Wrap(err, "update sys users pass")
		}
	}

	return restartSfs, nil
}

func (r *ReconcilePerconaServerMongoDB) updateUsersPass(cr *api.PerconaServerMongoDB, users []sysUser, adminUser, adminPass string, internalSysSecretObj *corev1.Secret) error {
	for i, replset := range cr.Spec.Replsets {
		if i > 0 {
			log.Info("sharded cluster not supported")
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
		password := string(internalSysSecretObj.Data[envMongoDBUserAdminPassword])
		username := string(internalSysSecretObj.Data[envMongoDBUserAdminUser])
		client, err := mongo.Dial(rsAddrs, replset.Name, username, password, true)
		if err != nil {
			client, err = mongo.Dial(rsAddrs, replset.Name, username, password, false)
			if err != nil {
				return errors.Wrap(err, "dial:")
			}
		}
		defer client.Disconnect(context.TODO())

		for _, user := range users {
			res := client.Database("admin").RunCommand(context.TODO(), bson.D{{Key: "updateUser", Value: user.Name}, {Key: "pwd", Value: user.Pass}})
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

func (r *ReconcilePerconaServerMongoDB) restartStatefulset(cr *api.PerconaServerMongoDB, newSecretDataHash string) error {
	for _, rs := range cr.Spec.Replsets {
		sfs := appsv1.StatefulSet{}
		err := r.client.Get(context.TODO(),
			types.NamespacedName{
				Namespace: cr.Namespace,
				Name:      cr.Name + rs.Name,
			},
			&sfs,
		)
		if err != nil {
			return errors.Wrapf(err, "failed to get stetefulset '%s'", rs.Name)
		}

		if sfs.Annotations == nil {
			sfs.Annotations = make(map[string]string)
		}
		sfs.Spec.Template.Annotations["last-applied-secret"] = newSecretDataHash

		err = r.client.Update(context.TODO(), &sfs)
		if err != nil {
			return errors.Wrapf(err, "update sfs '%s' last-applied annotation", rs.Name)
		}
	}
	return nil
}
