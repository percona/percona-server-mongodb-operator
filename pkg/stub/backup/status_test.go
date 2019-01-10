package backup

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk/mocks"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStubBackupUpdateStatus(t *testing.T) {
	client := &mocks.Client{}
	c := &Controller{
		client: client,
		psmdb: &v1alpha1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name(),
			},
			Spec: v1alpha1.PerconaServerMongoDBSpec{
				Backup: &v1alpha1.BackupSpec{
					Coordinator: &v1alpha1.BackupCoordinatorSpec{
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
					Tasks: []*v1alpha1.BackupTaskSpec{
						{
							Name:    t.Name(),
							Enabled: true,
						},
					},
				},
			},
		},
	}
	client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
	client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
	assert.NoError(t, c.updateStatus(c.psmdb.Spec.Backup.Tasks[0]))

	// test failures
	client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
	client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(mockUnexpectedError).Once()
	assert.Error(t, c.updateStatus(c.psmdb.Spec.Backup.Tasks[0]))
	client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(mockUnexpectedError).Once()
	assert.Error(t, c.updateStatus(c.psmdb.Spec.Backup.Tasks[0]))
}
