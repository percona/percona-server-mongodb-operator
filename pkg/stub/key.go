package stub

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func generateMongoDBKey() (string, error) {
	b := make([]byte, 768)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), err
}

func newPSMDBMongoKeySecret(m *v1alpha1.PerconaServerMongoDB) (*corev1.Secret, error) {
	key, err := generateMongoDBKey()
	if err != nil {
		return nil, err
	}
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Name + "-mongodb-key",
			Namespace: m.Namespace,
		},
		StringData: map[string]string{
			"key": key,
		},
	}, nil
}
