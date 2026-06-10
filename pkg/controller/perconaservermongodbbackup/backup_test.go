//go:generate ../../../bin/mockgen -source=../../../pkg/psmdb/backup/pbm.go -destination=mock_pbm.go -package=perconaservermongodbbackup

package perconaservermongodbbackup

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"go.mongodb.org/mongo-driver/bson/primitive"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	pbmBackup "github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestBackup_Status(t *testing.T) {
	ctx := context.Background()

	tests := map[string]struct {
		setupMock      func(*MockPBM)
		inputCR        *api.PerconaServerMongoDBBackup
		expectedStatus api.PerconaServerMongoDBBackupStatus
		cluster        *api.PerconaServerMongoDB
	}{
		"backup metadata not found": {
			setupMock: func(mockPBM *MockPBM) {
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(nil, pbmErrors.ErrNotFound)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname: "test-backup",
			},
		},
		"backup in error state": {
			setupMock: func(mockPBM *MockPBM) {
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:             "test-backup",
						Status:           defs.StatusError,
						StartTS:          1234567890,
						LastTransitionTS: 1234567900,
						Err:              "backup failed",
					}, nil)
				mockPBM.EXPECT().
					Node(gomock.Any()).
					Return("test-node", nil)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:     "test-backup",
						Replsets: []pbmBackup.BackupReplset{{Name: "rs0", Node: "node1"}},
					}, nil)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Spec:       api.PerconaServerMongoDBBackupSpec{Type: defs.LogicalBackup},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname:        "test-backup",
				State:          api.BackupStateError,
				Error:          "backup failed",
				StartAt:        &metav1.Time{Time: time.Unix(1234567890, 0)},
				LastTransition: &metav1.Time{Time: time.Unix(1234567900, 0)},
				Type:           defs.LogicalBackup,
				PBMPods:        map[string]string{"rs0": "node1"},
			},
		},
		"backup completed successfully": {
			setupMock: func(mockPBM *MockPBM) {
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:             "test-backup",
						Status:           defs.StatusDone,
						StartTS:          1234567890,
						LastTransitionTS: 1234567900,
						LastWriteTS:      primitive.Timestamp{T: 1234567950},
						Size:             1073741824, // 1GB
					}, nil)
				mockPBM.EXPECT().
					Node(gomock.Any()).
					Return("test-node", nil)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:     "test-backup",
						Replsets: []pbmBackup.BackupReplset{{Name: "rs0", Node: "node1"}},
					}, nil)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Spec:       api.PerconaServerMongoDBBackupSpec{Type: defs.LogicalBackup},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname:        "test-backup",
				State:          api.BackupStateReady,
				Size:           storage.PrettySize(1073741824),
				StartAt:        &metav1.Time{Time: time.Unix(1234567890, 0)},
				CompletedAt:    &metav1.Time{Time: time.Unix(1234567900, 0)},
				LastWriteAt:    &metav1.Time{Time: time.Unix(1234567950, 0)},
				LastTransition: &metav1.Time{Time: time.Unix(1234567900, 0)},
				Type:           defs.LogicalBackup,
				PBMPods:        map[string]string{"rs0": "node1"},
			},
		},
		"backup in starting state within deadline": {
			setupMock: func(mockPBM *MockPBM) {
				now := time.Now().UTC()
				startTime := now.Add(-30 * time.Second) // Within deadline
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:             "test-backup",
						Status:           defs.StatusStarting,
						StartTS:          startTime.Unix(),
						LastTransitionTS: startTime.Unix(),
					}, nil)
				mockPBM.EXPECT().
					Node(gomock.Any()).
					Return("test-node", nil)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:     "test-backup",
						Replsets: []pbmBackup.BackupReplset{{Name: "rs0", Node: "node1"}},
					}, nil)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Spec:       api.PerconaServerMongoDBBackupSpec{Type: defs.LogicalBackup},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname: "test-backup",
				State:   api.BackupStateRequested,
				Type:    defs.LogicalBackup,
				PBMPods: map[string]string{"rs0": "node1"},
			},
		},
		"backup in starting state beyond deadline": {
			setupMock: func(mockPBM *MockPBM) {
				now := time.Now().UTC()
				startTime := now.Add(-150 * time.Second) // Beyond deadline (120s)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:             "test-backup",
						Status:           defs.StatusStarting,
						StartTS:          startTime.Unix(),
						LastTransitionTS: startTime.Unix(),
					}, nil)
				mockPBM.EXPECT().
					Node(gomock.Any()).
					Return("test-node", nil)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:     "test-backup",
						Replsets: []pbmBackup.BackupReplset{{Name: "rs0", Node: "node1"}},
					}, nil)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Spec:       api.PerconaServerMongoDBBackupSpec{Type: defs.LogicalBackup},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname: "test-backup",
				State:   api.BackupStateError,
				Error:   pbmStartingDeadlineErrMsg,
				Type:    defs.LogicalBackup,
				PBMPods: map[string]string{"rs0": "node1"},
			},
		},
		"backup in starting state beyond custom deadline": {
			setupMock: func(mockPBM *MockPBM) {
				now := time.Now().UTC()
				startTime := now.Add(-20 * time.Second) // Beyond deadline (10s)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:             "test-backup",
						Status:           defs.StatusStarting,
						StartTS:          startTime.Unix(),
						LastTransitionTS: startTime.Unix(),
					}, nil)
				mockPBM.EXPECT().
					Node(gomock.Any()).
					Return("test-node", nil)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:     "test-backup",
						Replsets: []pbmBackup.BackupReplset{{Name: "rs0", Node: "node1"}},
					}, nil)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Spec:       api.PerconaServerMongoDBBackupSpec{Type: defs.LogicalBackup},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			cluster: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					Backup: api.BackupSpec{
						StartingDeadlineSeconds: ptr.To(int64(10)),
					},
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname: "test-backup",
				State:   api.BackupStateError,
				Error:   pbmStartingDeadlineErrMsg,
				Type:    defs.LogicalBackup,
				PBMPods: map[string]string{"rs0": "node1"},
			},
		},
		"backup in running state": {
			setupMock: func(mockPBM *MockPBM) {
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:             "test-backup",
						Status:           defs.StatusRunning,
						StartTS:          1234567890,
						LastTransitionTS: 1234567900,
					}, nil)
				mockPBM.EXPECT().
					Node(gomock.Any()).
					Return("test-node", nil)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:     "test-backup",
						Replsets: []pbmBackup.BackupReplset{{Name: "rs0", Node: "node1"}},
					}, nil)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Spec:       api.PerconaServerMongoDBBackupSpec{Type: defs.LogicalBackup},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname:        "test-backup",
				State:          api.BackupStateRunning,
				StartAt:        &metav1.Time{Time: time.Unix(1234567890, 0)},
				LastTransition: &metav1.Time{Time: time.Unix(1234567900, 0)},
				Type:           defs.LogicalBackup,
				PBMPods:        map[string]string{"rs0": "node1"},
			},
		},
		"incremental backup with base not found error": {
			setupMock: func(mockPBM *MockPBM) {
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:             "test-backup",
						Status:           defs.StatusError,
						StartTS:          1234567890,
						LastTransitionTS: 1234567900,
						Err:              "define source backup: not found",
					}, nil)
				mockPBM.EXPECT().
					Node(gomock.Any()).
					Return("test-node", nil)
				mockPBM.EXPECT().
					GetBackupMeta(gomock.Any(), "test-backup").
					Return(&pbmBackup.BackupMeta{
						Name:     "test-backup",
						Replsets: []pbmBackup.BackupReplset{{Name: "rs0", Node: "node1"}},
					}, nil)
			},
			inputCR: &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
				Spec:       api.PerconaServerMongoDBBackupSpec{Type: defs.IncrementalBackup},
				Status: api.PerconaServerMongoDBBackupStatus{
					PBMname: "test-backup",
				},
			},
			expectedStatus: api.PerconaServerMongoDBBackupStatus{
				PBMname:        "test-backup",
				State:          api.BackupStateError,
				Error:          "incremental base backup not found",
				StartAt:        &metav1.Time{Time: time.Unix(1234567890, 0)},
				LastTransition: &metav1.Time{Time: time.Unix(1234567900, 0)},
				Type:           defs.IncrementalBackup,
				PBMPods:        map[string]string{"rs0": "node1"},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockPBM := NewMockPBM(ctrl)
			tt.setupMock(mockPBM)

			backup := &managedBackups{
				pbm: mockPBM,
			}

			cluster := tt.cluster
			if cluster == nil {
				cluster = new(api.PerconaServerMongoDB)
			}
			status, err := backup.Status(ctx, nil, cluster, tt.inputCR)
			assert.NoError(t, err)

			assert.Equal(t, tt.expectedStatus.State, status.State)
			assert.Equal(t, tt.expectedStatus.Error, status.Error)
			assert.Equal(t, tt.expectedStatus.Type, status.Type)
			assert.Equal(t, tt.expectedStatus.PBMPods, status.PBMPods)
			if name == "backup completed successfully" {
				assert.Equal(t, tt.expectedStatus.Size, status.Size)
			} else {
				assert.Empty(t, status.Size)
			}

			if tt.expectedStatus.StartAt != nil {
				assert.NotNil(t, status.StartAt)
			}
			if tt.expectedStatus.CompletedAt != nil {
				assert.NotNil(t, status.CompletedAt)
				assert.Equal(t, tt.expectedStatus.CompletedAt.Time, status.CompletedAt.Time)
			}
			if tt.expectedStatus.LastWriteAt != nil {
				assert.NotNil(t, status.LastWriteAt)
				assert.Equal(t, tt.expectedStatus.LastWriteAt.Time, status.LastWriteAt.Time)
			}

			if name != "backup in starting state within deadline" && name != "backup in starting state beyond deadline" {
				if tt.expectedStatus.LastTransition != nil {
					assert.NotNil(t, status.LastTransition)
					assert.Equal(t, tt.expectedStatus.LastTransition.Time, status.LastTransition.Time)
				}
			}
		})
	}
}
