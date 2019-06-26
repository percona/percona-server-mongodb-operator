package perconaservermongodb

import (
	"context"
	"encoding/base64"
	"fmt"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/secret"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (r *ReconcilePerconaServerMongoDB) reconcileUsersSecret(cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	err := r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.Users,
		},
		&secretObj,
	)
	if err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("get users secret: %v", err)
	}

	data := make(map[string][]byte)
	data["MONGODB_BACKUP_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("backup")))
	data["MONGODB_BACKUP_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create backup users pass: %v", err)
	}
	data["MONGODB_CLUSTER_ADMIN_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("clusterAdmin")))
	data["MONGODB_CLUSTER_ADMIN_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create cluster admin users pass: %v", err)
	}
	data["MONGODB_CLUSTER_MONITOR_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("clusterMonitor")))
	data["MONGODB_CLUSTER_MONITOR_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create cluster monitor users pass: %v", err)
	}
	data["MONGODB_USER_ADMIN_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("userAdmin")))
	data["MONGODB_USER_ADMIN_PASSWORD"], err = secret.GeneratePassword()
	if err != nil {
		return fmt.Errorf("create admin users pass: %v", err)
	}

	secretObj = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Spec.Secrets.Users,
			Namespace: cr.Namespace,
		},
		Data: data,
		Type: corev1.SecretTypeOpaque,
	}
	err = r.client.Create(context.TODO(), &secretObj)
	if err != nil {
		return fmt.Errorf("create Users secret: %v", err)
	}

	return nil
}
