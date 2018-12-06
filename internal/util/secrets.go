package util

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewSecret returns a Core API Secret structure
func NewSecret(m *v1alpha1.PerconaServerMongoDB, name string, data map[string]string) *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.Namespace,
		},
		StringData: data,
	}
	AddOwnerRefToObject(secret, AsOwner(m))
	return secret
}

// GetSecret retrieves a Kubernetes Secret
func GetSecret(m *v1alpha1.PerconaServerMongoDB, client sdk.Client, secretName string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: m.Namespace,
		},
	}
	err := client.Get(secret)
	return secret, err
}
