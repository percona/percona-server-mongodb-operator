package backup

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk/mocks"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/testutil"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	batchv1 "k8s.io/api/batch/v1"
	batchv1b "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestStubBackupEnsureBackupTasks(t *testing.T) {
	c := New(nil, &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: &v1alpha1.BackupSpec{
				Tasks: []*v1alpha1.BackupTaskSpec{},
			},
		},
		Status: v1alpha1.PerconaServerMongoDBStatus{
			Backups: []*v1alpha1.BackupTaskStatus{},
		},
	}, nil, nil)

	t.Run("enable", func(t *testing.T) {
		client := &mocks.Client{}
		c.client = client

		c.psmdb.Spec.Backup.Tasks = append(c.psmdb.Spec.Backup.Tasks, &v1alpha1.BackupTaskSpec{
			Name:            t.Name(),
			Enabled:         true,
			Schedule:        "* * * * *",
			DestinationType: v1alpha1.BackupDestinationS3,
		})

		// test success
		client.On("Create", mock.AnythingOfType("*v1beta1.CronJob")).Return(nil)
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
		client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Run(func(args mock.Arguments) {
			psmdb := args.Get(0).(*v1alpha1.PerconaServerMongoDB)
			assert.Len(t, psmdb.Status.Backups, 1)
			assert.Equal(t, t.Name(), psmdb.Status.Backups[0].Name)
		}).Return(nil).Once()
		assert.NoError(t, c.EnsureBackupTasks())

		// test failure
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.EnsureBackupTasks())
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
		client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.EnsureBackupTasks())

		client.AssertExpectations(t)
	})

	t.Run("update", func(t *testing.T) {
		client := &mocks.Client{}
		c.client = client

		// test update
		client.On("Create", mock.AnythingOfType("*v1beta1.CronJob")).Return(testutil.AlreadyExistsError).Once()
		client.On("Get", mock.AnythingOfType("*v1beta1.CronJob")).Run(func(args mock.Arguments) {
			cronJob := args.Get(0).(*batchv1b.CronJob)
			cronJob.Spec = batchv1b.CronJobSpec{
				JobTemplate: batchv1b.JobTemplateSpec{
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								Containers: []corev1.Container{
									{
										Name: backupCtlContainerName,
									},
								},
							},
						},
					},
				},
			}
		}).Return(nil).Once()
		client.On("Update", mock.AnythingOfType("*v1beta1.CronJob")).Return(nil).Once()
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
		client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
		assert.NoError(t, c.EnsureBackupTasks())

		client.AssertExpectations(t)
	})

	t.Run("disable", func(t *testing.T) {
		client := &mocks.Client{}
		c.client = client

		c.psmdb.Spec.Backup.Tasks[0].Name = t.Name()
		c.psmdb.Spec.Backup.Tasks[0].Enabled = false
		c.psmdb.Status.Backups = append(c.psmdb.Status.Backups, &v1alpha1.BackupTaskStatus{
			Name:    t.Name(),
			Enabled: true,
		})

		// test success
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Run(func(args mock.Arguments) {
			cronJob := args.Get(0).(*batchv1b.CronJob)
			assert.Equal(t, c.psmdb.Name+"-backup-"+t.Name(), cronJob.Name)
		}).Return(nil).Once()
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil)
		client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil)
		assert.NoError(t, c.EnsureBackupTasks())

		// test failure of delete
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Return(testutil.UnexpectedError)
		assert.Error(t, c.EnsureBackupTasks())

		client.AssertExpectations(t)
	})
}

func TestStubBackupDeleteBackupTasks(t *testing.T) {
	c := New(nil, &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: t.Name(),
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Backup: &v1alpha1.BackupSpec{
				Tasks: []*v1alpha1.BackupTaskSpec{},
			},
		},
		Status: v1alpha1.PerconaServerMongoDBStatus{
			Backups: []*v1alpha1.BackupTaskStatus{},
		},
	}, nil, nil)

	// test remove using spec as source
	t.Run("spec", func(t *testing.T) {
		client := &mocks.Client{}
		c.client = client

		// test success
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Run(func(args mock.Arguments) {
			cronJob := args.Get(0).(*batchv1b.CronJob)
			assert.Equal(t, c.psmdb.Name+"-backup-"+t.Name(), cronJob.Name)
		}).Return(nil).Once()
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil).Once()
		c.psmdb.Spec.Backup.Tasks = append(c.psmdb.Spec.Backup.Tasks, &v1alpha1.BackupTaskSpec{
			Name:    t.Name(),
			Enabled: true,
		})
		assert.NoError(t, c.DeleteBackupTasks())

		// test failures
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Return(nil).Once()
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.DeleteBackupTasks())
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.DeleteBackupTasks())

		client.AssertExpectations(t)
	})

	// test remove using status as source
	t.Run("status", func(t *testing.T) {
		client := &mocks.Client{}
		c.client = client

		// test success
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Return(nil)
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil)
		client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Run(func(args mock.Arguments) {
			psmdb := args.Get(0).(*v1alpha1.PerconaServerMongoDB)
			assert.Len(t, psmdb.Status.Backups, 0)
		}).Return(nil).Once()
		c.psmdb.Status.Backups = append(c.psmdb.Status.Backups, &v1alpha1.BackupTaskStatus{
			Name:    t.Name(),
			Enabled: true,
		})
		assert.NoError(t, c.DeleteBackupTasks())

		// test failures
		client = &mocks.Client{}
		c.client = client
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Return(nil)
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(nil)
		client.On("Update", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(testutil.UnexpectedError)
		assert.Error(t, c.DeleteBackupTasks())

		client = &mocks.Client{}
		c.client = client
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Return(nil).Once()
		client.On("Get", mock.AnythingOfType("*v1alpha1.PerconaServerMongoDB")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.DeleteBackupTasks())

		client = &mocks.Client{}
		c.client = client
		client.On("Delete", mock.AnythingOfType("*v1beta1.CronJob")).Return(testutil.UnexpectedError).Once()
		assert.Error(t, c.DeleteBackupTasks())

		client.AssertExpectations(t)
	})
}
