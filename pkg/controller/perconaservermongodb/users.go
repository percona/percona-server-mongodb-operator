package perconaservermongodb

import (
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

	newSysData, err := json.Marshal(sysUsersSecretObj.Data)
	if err != nil {
		return errors.Wrap(err, "marshal sys secret data")
	}
	newSecretDataHash := sha256Hash(newSysData)

	internalSysSecretObj, err := r.getInternalSysUsersSecret(cr, &sysUsersSecretObj)
	if err != nil {
		return errors.Wrap(err, "get internal sys users secret")
	}

	if cr.Status.State != api.AppStateReady {
		return nil
	}

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

	err = r.updateInternalSysUsersSecret(cr, &sysUsersSecretObj)
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
		if string(sysUsersSecretObj.Data[key]) == string(internalSysSecretObj.Data[key]) {
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
	for i, repleset := range cr.Spec.Replsets {
		if i > 0 {
			return nil
		}

		matchLabels := map[string]string{
			"app.kubernetes.io/name":       "percona-server-mongodb",
			"app.kubernetes.io/instance":   cr.Name,
			"app.kubernetes.io/replset":    repleset.Name,
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
			return errors.Errorf("get pods list for replset %s: %v", repleset.Name, err)
		}
		rsAddrs, err := psmdb.GetReplsetAddrs(r.client, cr, repleset, pods.Items)
		if err != nil {
			return errors.Wrap(err, "get replset addr")
		}
		fmt.Println("Try to update users", users)
		client, err := mongo.Dial(rsAddrs, repleset.Name, internalSysSecretObj, true, true)
		if err != nil {
			client, err = mongo.Dial(rsAddrs, repleset.Name, internalSysSecretObj, false, true)
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

// getInternalSysUsersSecret return secret created by operator for storing system users data
func (r *ReconcilePerconaServerMongoDB) getInternalSysUsersSecret(cr *api.PerconaServerMongoDB, sysUsersSecretObj *corev1.Secret) (corev1.Secret, error) {
	secretName := internalPrefix + cr.Name + "-users"
	internalSysUsersSecretObj, err := r.getInternalSysUsersSecretObj(cr, sysUsersSecretObj)
	if err != nil {
		return internalSysUsersSecretObj, errors.Wrap(err, "create internal sys users secret object")
	}
	err = r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      secretName,
		},
		&internalSysUsersSecretObj,
	)
	if err != nil && !k8serrors.IsNotFound(err) {
		return internalSysUsersSecretObj, errors.Wrap(err, "get internal sys users secret")
	}

	if k8serrors.IsNotFound(err) {
		err = r.client.Create(context.TODO(), &internalSysUsersSecretObj)
		if err != nil {
			return internalSysUsersSecretObj, errors.Wrap(err, "create internal sys users secret")
		}
	}

	return internalSysUsersSecretObj, nil
}

func (r *ReconcilePerconaServerMongoDB) updateInternalSysUsersSecret(cr *api.PerconaServerMongoDB, sysUsersSecretObj *corev1.Secret) error {
	internalAppUsersSecretObj, err := r.getInternalSysUsersSecretObj(cr, sysUsersSecretObj)
	if err != nil {
		return errors.Wrap(err, "get internal sys users secret object")
	}
	err = r.client.Update(context.TODO(), &internalAppUsersSecretObj)
	if err != nil {
		return errors.Wrap(err, "create internal sys users secret")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) getInternalSysUsersSecretObj(cr *api.PerconaServerMongoDB, sysUsersSecretObj *corev1.Secret) (corev1.Secret, error) {
	internalSysUsersSecretObj := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      internalPrefix + cr.Name + "-users",
			Namespace: cr.Namespace,
		},
		Data: sysUsersSecretObj.Data,
		Type: corev1.SecretTypeOpaque,
	}
	err := setControllerReference(cr, &internalSysUsersSecretObj, r.scheme)
	if err != nil {
		return internalSysUsersSecretObj, errors.Wrap(err, "set owner refs")
	}

	return internalSysUsersSecretObj, nil
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
	sfs := appsv1.StatefulSet{}
	err := r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Name + "-rs0",
		},
		&sfs,
	)
	if err != nil {
		return errors.Wrap(err, "failed to get stetefulset")
	}

	if sfs.Annotations == nil {
		sfs.Annotations = make(map[string]string)
	}
	sfs.Spec.Template.Annotations["last-applied-secret"] = newSecretDataHash

	err = r.client.Update(context.TODO(), &sfs)
	if err != nil {
		return errors.Wrap(err, "update sfs last-applied annotation")
	}

	return nil
}
