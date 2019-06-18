package perconaservermongodb

import (
	"context"
	"encoding/base64"
	"fmt"
	"math/rand"
	"time"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (r *ReconcilePerconaServerMongoDB) reconcileSecrets(cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	err := r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      "my-cluster-name-secrets",
		},
		&secretObj,
	)
	if err == nil {
		return nil
	} else if !errors.IsNotFound(err) {
		return fmt.Errorf("get secret: %v", err)
	}

	data := make(map[string][]byte)
	data["MONGODB_BACKUP_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("backup")))
	data["MONGODB_BACKUP_PASSWORD"] = generatePass()
	data["MONGODB_CLUSTER_ADMIN_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("clusterAdminUser")))
	data["MONGODB_CLUSTER_ADMIN_PASSWORD"] = generatePass()
	data["MONGODB_CLUSTER_MONITOR_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("monitorUser")))
	data["MONGODB_CLUSTER_MONITOR_PASSWORD"] = generatePass()
	data["MONGODB_USER_ADMIN_USER"] = []byte(base64.StdEncoding.EncodeToString([]byte("adminUser")))
	data["MONGODB_USER_ADMIN_PASSWORD"] = generatePass()
	data["PMM_SERVER_USER"] = []byte("cG1t")
	data["PMM_SERVER_PASSWORD"] = generatePass()
	secretObj = corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster-name-secrets",
			Namespace: cr.Namespace,
		},
		Data: data,
		Type: corev1.SecretTypeOpaque,
	}
	err = r.client.Create(context.TODO(), &secretObj)
	if err != nil {
		return fmt.Errorf("create TLS secret: %v", err)
	}

	return nil
}

func generatePass() []byte {
	rand.Seed(time.Now().UnixNano())
	all := "ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"abcdefghijklmnopqrstuvwxyz" +
		"0123456789" +
		"~=+%^*/()[]{}/!@#$?|"
	length := 16
	buf := make([]byte, length)

	for i := 0; i < length; i++ {
		buf[i] = all[rand.Intn(len(all))]
	}
	rand.Shuffle(len(buf), func(i, j int) {
		buf[i], buf[j] = buf[j], buf[i]
	})
	b64Pass := make([]byte, base64.StdEncoding.EncodedLen(len(buf)))
	base64.StdEncoding.Encode(b64Pass, buf)

	return b64Pass
}
