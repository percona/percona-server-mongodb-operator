package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/sdk/mocks"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewPSMDBMongoKeySecret(t *testing.T) {
	secret := newPSMDBMongoKeySecret(&v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.Name(),
			Namespace: "test",
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Secrets: &v1alpha1.SecretsSpec{
				Key: t.Name(),
			},
		},
	})
	assert.NotNil(t, secret)
	assert.Equal(t, t.Name(), secret.Name)
	assert.Len(t, secret.StringData[mongoDbSecretMongoKeyVal], 1024)
}

func TestGetPSMDBSecret(t *testing.T) {
	sdk := &mocks.Client{}
}
