package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk/mocks"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	motPkg "github.com/percona/mongodb-orchestration-tools/pkg"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGetReplsetDialInfo(t *testing.T) {
	di := getReplsetDialInfo(
		&v1alpha1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      t.Name(),
				Namespace: "default",
			},
			Spec: v1alpha1.PerconaServerMongoDBSpec{
				Mongod: &v1alpha1.MongodSpec{
					Net: &v1alpha1.MongodSpecNet{
						Port: 99999,
					},
				},
			},
		},
		&v1alpha1.ReplsetSpec{
			Name: defaultReplsetName,
		},
		[]corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "testPod",
				},
			},
		},
		&corev1.Secret{
			Data: map[string][]byte{
				motPkg.EnvMongoDBClusterAdminUser:     []byte("clusterAdmin"),
				motPkg.EnvMongoDBClusterAdminPassword: []byte("123456"),
			},
		},
	)
	assert.NotNil(t, di)
	assert.Equal(t, defaultReplsetName, di.ReplicaSetName)
	assert.Len(t, di.Addrs, 1)
	assert.Equal(t, "testPod."+t.Name()+"-"+defaultReplsetName+".default.svc.cluster.local:99999", di.Addrs[0])
	assert.Equal(t, "clusterAdmin", di.Username)
	assert.Equal(t, "123456", di.Password)
	assert.Equal(t, MongoDBTimeout, di.Timeout)
	assert.True(t, di.FailFast)
}

func TestEnsureReplsetStatefulSet(t *testing.T) {
	client := &mocks.Client{}
	h := &Handler{client: client}
	client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(nil)
	client.On("Get", mock.AnythingOfType("*v1.StatefulSet")).Return(nil)
	client.On("Update", mock.AnythingOfType("*v1.StatefulSet")).Return(nil)

	psmdb := &v1alpha1.PerconaServerMongoDB{}
	replset := &v1alpha1.ReplsetSpec{
		Name: t.Name(),
		Size: 3,
		ResourcesSpec: &v1alpha1.ResourcesSpec{
			Limits: &v1alpha1.ResourceSpecRequirements{
				Cpu:     "1m",
				Memory:  "1m",
				Storage: "1G",
			},
			Requests: &v1alpha1.ResourceSpecRequirements{
				Cpu:    "1m",
				Memory: "1m",
			},
		},
	}
	ss, err := h.ensureReplsetStatefulSet(psmdb, replset)
	assert.NoError(t, err)
	assert.NotNil(t, ss)

	// test an error is returned when no storage limit is set
	// https://jira.percona.com/browse/CLOUD-42
	replset.ResourcesSpec.Limits.Storage = ""
	_, err = h.ensureReplsetStatefulSet(psmdb, replset)
	assert.Error(t, err)
}

//func TestIsReplsetInitialized(t *testing.T) {}
//func TestHandlerHandleReplsetInit(t *testing.T) {}
//func TestHandlerEnsureReplset(t *testing.T) {}
