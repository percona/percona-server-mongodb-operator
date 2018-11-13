package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/sdk/mocks"

	"github.com/operator-framework/operator-sdk/pkg/sdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHandlerHandle(t *testing.T) {
	event := sdk.Event{
		Object: &v1alpha1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      t.Name(),
				Namespace: "test",
			},
			Spec: v1alpha1.PerconaServerMongoDBSpec{
				Secrets: &v1alpha1.SecretsSpec{
					Key:   defaultKeySecretName,
					Users: defaultUsersSecretName,
				},
				Replsets: []*v1alpha1.ReplsetSpec{
					{
						Name: defaultReplsetName,
						Size: defaultMongodSize,
						ResourcesSpec: &v1alpha1.ResourcesSpec{
							Limits: &v1alpha1.ResourceSpecRequirements{
								Cpu:     "1",
								Memory:  "1G",
								Storage: "1G",
							},
							Requests: &v1alpha1.ResourceSpecRequirements{
								Cpu:    "1",
								Memory: "1G",
							},
						},
					},
				},
				Mongod: &v1alpha1.MongodSpec{
					Net: &v1alpha1.MongodSpecNet{
						Port: 99999,
					},
				},
			},
		},
	}

	client := &mocks.Client{}
	client.On("Create", mock.AnythingOfType("*v1.Secret")).Return(nil)
	client.On("Create", mock.AnythingOfType("*v1.Service")).Return(nil)
	client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(nil)
	client.On("Get", mock.AnythingOfType("*v1.Secret")).Return(nil)
	client.On("Get", mock.AnythingOfType("*v1.StatefulSet")).Return(nil)
	client.On("List",
		"test",
		mock.AnythingOfType("*v1.PodList"),
		mock.AnythingOfType("sdk.ListOption"),
	).Return(nil)

	h := &Handler{
		client: client,
		serverVersion: &v1alpha1.ServerVersion{
			Platform: v1alpha1.PlatformKubernetes,
		},
	}

	assert.NoError(t, h.Handle(nil, event))

	client.AssertExpectations(t)

	// check last call was a Create with a corev1.Service object:
	calls := len(client.Calls)
	lastCall := client.Calls[calls-1]
	assert.Equal(t, "Create", lastCall.Method)
	assert.IsType(t, &corev1.Service{}, lastCall.Arguments.Get(0))
}
