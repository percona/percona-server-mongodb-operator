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
	c := New(client, &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: &v1alpha1.BackupSpec{
				Tasks: []*v1alpha1.BackupTaskSpec{
					{
						Name:    t.Name(),
						Enabled: true,
					},
				},
			},
		},
		Status: v1alpha1.PerconaServerMongoDBStatus{
			Backups: []*v1alpha1.BackupTaskStatus{},
		},
	}, nil, nil)

	client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
	client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Run(func(args mock.Arguments) {
		psmdb := args.Get(0).(*v1alpha1.PerconaServerMongoDB)
		assert.Len(t, psmdb.Status.Backups, 1)
	}).Return(nil).Once()
	assert.NoError(t, c.updateStatus(c.psmdb.Spec.Backup.Tasks[0]))
	client.AssertExpectations(t)

	// test failures
	client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
	client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(mockUnexpectedError).Once()
	assert.Error(t, c.updateStatus(c.psmdb.Spec.Backup.Tasks[0]))
	client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(mockUnexpectedError).Once()
	assert.Error(t, c.updateStatus(c.psmdb.Spec.Backup.Tasks[0]))
	client.AssertExpectations(t)
}

func TestStubBackupDeleteStatus(t *testing.T) {
	client := &mocks.Client{}
	c := New(client, &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: &v1alpha1.BackupSpec{
				Tasks: []*v1alpha1.BackupTaskSpec{
					{
						Name:    t.Name(),
						Enabled: true,
					},
				},
			},
		},
		Status: v1alpha1.PerconaServerMongoDBStatus{
			Backups: []*v1alpha1.BackupTaskStatus{
				{
					Name:    t.Name(),
					Enabled: true,
				},
			},
		},
	}, nil, nil)

	client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
	client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Run(func(args mock.Arguments) {
		psmdb := args.Get(0).(*v1alpha1.PerconaServerMongoDB)
		assert.Len(t, psmdb.Status.Backups, 0)
	}).Return(nil).Once()
	assert.NoError(t, c.deleteStatus(c.psmdb.Spec.Backup.Tasks[0]))
	client.AssertExpectations(t)
}
