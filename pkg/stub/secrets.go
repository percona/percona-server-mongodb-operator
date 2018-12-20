package stub

import (
	"crypto/rand"
	"encoding/base64"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
)

const (
	mongoDBSecretMongoKeyVal = "mongodb-key"
)

var (
	secretFileMode int32 = 0060
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

func newMongoKeySecret(m *v1alpha1.PerconaServerMongoDB) *corev1.Secret {
	return util.NewSecret(m, m.Spec.Secrets.Key, map[string]string{
		mongoDBSecretMongoKeyVal: generateMongoDBKey(),
	})
}
