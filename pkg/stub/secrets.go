package stub

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	mongoDBSecretsDir        = "/etc/mongodb-secrets"
	mongoDBSecretMongoKeyVal = "mongodb-key"
)

// generateMongoDBKey generates a 1024 byte-length random key for MongoDB Internal Authentication
//
// See: https://docs.mongodb.com/manual/core/security-internal-authentication/#keyfiles
//
func generateMongoDBKey() string {
	b := make([]byte, 768)
	_, _ = rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// newPSMDBMongoKeySecret returns a Core API Secret structure containing a new MongoDB Internal
// Authentication key
func newPSMDBMongoKeySecret(m *v1alpha1.PerconaServerMongoDB) *corev1.Secret {
	secret := &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      m.Spec.Secrets.Key,
			Namespace: m.Namespace,
		},
		StringData: map[string]string{
			mongoDBSecretMongoKeyVal: generateMongoDBKey(),
		},
	}
	addOwnerRefToObject(secret, asOwner(m))
	return secret
}

// getPSMDBSecret retrieves a Kubernetes Secret
func getPSMDBSecret(m *v1alpha1.PerconaServerMongoDB, client sdk.Client, secretName string) (*corev1.Secret, error) {
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
