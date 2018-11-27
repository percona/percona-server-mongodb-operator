package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk/mocks"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
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
	assert.Len(t, secret.StringData[mongoDBSecretMongoKeyVal], 1024)
}

func TestGetPSMDBSecret(t *testing.T) {
	psmdb := &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.Name(),
			Namespace: "test",
		},
	}

	// make mock SDK return secret with test mongoDBSecretMongoKeyVal key
	sdk := &mocks.Client{}
	sdk.On("Get", mock.AnythingOfType("*v1.Secret")).Return(nil).Run(func(args mock.Arguments) {
		obj := args.Get(0).(*corev1.Secret)
		obj.Data = map[string][]byte{
			mongoDBSecretMongoKeyVal: []byte(t.Name()),
		}
	})

	// call getPSMDBSecret() to get secret from mock SDK
	secret, err := getPSMDBSecret(psmdb, sdk, mongoDBSecretMongoKeyVal)
	assert.NoError(t, err)

	// test secret returned from mock SDK
	assert.Len(t, secret.Data, 1)
	assert.Equal(t, []byte(t.Name()), secret.Data[mongoDBSecretMongoKeyVal])
}
