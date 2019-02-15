package backup

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk/mocks"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/testutil"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStubBackupEnsureCoordinator(t *testing.T) {
	client := &mocks.Client{}
	c := New(client, &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: v1alpha1.BackupSpec{
				Coordinator: &v1alpha1.BackupCoordinatorSpec{
					EnableClientsLogging: DefaultEnableClientsLogging,
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
		},
	}, nil, nil)

	t.Run("create", func(t *testing.T) {
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
		client.On("Create", mock.AnythingOfType("*v1.Service")).Return(nil).Once()
		assert.NoError(t, c.EnsureCoordinator())

		// test failures
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
		client.On("Create", mock.AnythingOfType("*v1.Service")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.EnsureCoordinator())
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.EnsureCoordinator())
		client.AssertExpectations(t)
	})

	t.Run("update", func(t *testing.T) {
		client.On("Create", mock.AnythingOfType("*v1.StatefulSet")).Return(testutil.AlreadyExistsError).Once()
		client.On("Create", mock.AnythingOfType("*v1.Service")).Return(testutil.AlreadyExistsError).Once()
		client.On("Update", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
		assert.NoError(t, c.EnsureCoordinator())
		client.AssertExpectations(t)
	})
}

func TestStubBackupDeleteCoordinator(t *testing.T) {
	client := &mocks.Client{}
	c := New(client, &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: v1alpha1.BackupSpec{
				Coordinator: &v1alpha1.BackupCoordinatorSpec{
					EnableClientsLogging: DefaultEnableClientsLogging,
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
		},
	}, nil, nil)

	// test success
	client.On("Delete", mock.AnythingOfType("*v1.Service")).Return(nil).Once()
	client.On("Delete", mock.AnythingOfType("*v1.StatefulSet")).Return(nil).Once()
	assert.NoError(t, c.DeleteCoordinator())

	// test failures
	client.On("Delete", mock.AnythingOfType("*v1.Service")).Return(nil).Once()
	client.On("Delete", mock.AnythingOfType("*v1.StatefulSet")).Return(testutil.UnexpectedError).Once()
	assert.Error(t, c.DeleteCoordinator())
	client.On("Delete", mock.AnythingOfType("*v1.Service")).Return(testutil.UnexpectedError).Once()
	assert.Error(t, c.DeleteCoordinator())
}
