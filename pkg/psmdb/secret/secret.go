package secret

import (
	"crypto/rand"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	keyName = "mongodb-key"
)

func InternalKeyMeta(name, namespace string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

// GenInternalKey generates an intenal mongodb key and returns a Secret.Data object
// See: https://docs.mongodb.com/manual/core/security-internal-authentication/#keyfiles
func GenInternalKey() (map[string][]byte, error) {
	key, err := generateKey1024()
	if err != nil {
		return nil, fmt.Errorf("key generation: %v", err)
	}

	return map[string][]byte{
		keyName: key,
	}, nil
}

func generateKey1024() ([]byte, error) {
	b := make([]byte, 768)
	_, err := rand.Read(b)
	return b, err
}
