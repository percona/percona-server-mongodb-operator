package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"

	pbmBackup "github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func newSnapshotTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, api.SchemeBuilder.AddToScheme(s))
	require.NoError(t, volumesnapshotv1.AddToScheme(s))
	return s
}

func TestSnapshotBackups_Start(t *testing.T) {
	ctx := context.Background()

	tests := map[string]struct {
		setupMock    func(*MockPBM)
		cluster      *api.PerconaServerMongoDB
		wantState    api.BackupState
		wantErr      bool
		wantReplsets []string
	}{
		"success - no replsets": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().SendCmd(gomock.Any(), gomock.Any()).Return(nil)
			},
			cluster:   &api.PerconaServerMongoDB{},
			wantState: api.BackupStateRequested,
		},
		"success - with replsets": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().SendCmd(gomock.Any(), gomock.Any()).Return(nil)
			},
			cluster: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{Name: "rs0"},
						{Name: "rs1"},
					},
				},
			},
			wantState:    api.BackupStateRequested,
			wantReplsets: []string{"rs0", "rs1"},
		},
		"success - with sharding enabled": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().SendCmd(gomock.Any(), gomock.Any()).Return(nil)
			},
			cluster: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					Sharding: api.Sharding{
						Enabled: true,
						ConfigsvrReplSet: &api.ReplsetSpec{
							Name: "cfg",
						},
					},
					Replsets: []*api.ReplsetSpec{
						{Name: "rs0"},
					},
				},
			},
			wantState:    api.BackupStateRequested,
			wantReplsets: []string{"cfg", "rs0"},
		},
		"SendCmd error": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().SendCmd(gomock.Any(), gomock.Any()).Return(fmt.Errorf("connection refused"))
			},
			cluster: &api.PerconaServerMongoDB{},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockPBM := NewMockPBM(ctrl)
			tt.setupMock(mockPBM)

			b := &snapshotBackups{pbm: mockPBM}
			cr := &api.PerconaServerMongoDBBackup{
				ObjectMeta: metav1.ObjectMeta{Name: "test-backup"},
			}

			status, err := b.Start(ctx, nil, tt.cluster, cr)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantState, status.State)
			assert.NotEmpty(t, status.PBMname)
			assert.NotNil(t, status.LastTransition)
			assert.Len(t, status.ReplsetNames, len(tt.wantReplsets))
			for _, rs := range tt.wantReplsets {
				assert.Contains(t, status.ReplsetNames, rs)
			}
		})
	}
}

func TestSnapshotBackups_ReconcileSnapshot(t *testing.T) {
	ctx := context.Background()
	scheme := newSnapshotTestScheme(t)

	const namespace = "default"

	bcp := &api.PerconaServerMongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backup",
			Namespace: namespace,
		},
		Spec: api.PerconaServerMongoDBBackupSpec{
			VolumeSnapshotClass: ptr.To("csi-snapclass"),
		},
	}

	existingSnapshot := &volumesnapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.VolumeSnapshotName(bcp, "rs0"),
			Namespace: namespace,
		},
		Status: &volumesnapshotv1.VolumeSnapshotStatus{
			ReadyToUse: ptr.To(true),
		},
	}

	tests := map[string]struct {
		existingObjects []client.Object
		rsName          string
		pvc             string
		wantErr         bool
		check           func(t *testing.T, snapshot *volumesnapshotv1.VolumeSnapshot)
	}{
		"creates new snapshot when none exists": {
			rsName: "rs0",
			pvc:    "mongod-data-pod-0",
			check: func(t *testing.T, snapshot *volumesnapshotv1.VolumeSnapshot) {
				assert.Equal(t, naming.VolumeSnapshotName(bcp, "rs0"), snapshot.GetName())
				assert.Equal(t, namespace, snapshot.GetNamespace())
				assert.Equal(t, ptr.To("csi-snapclass"), snapshot.Spec.VolumeSnapshotClassName)
				require.NotNil(t, snapshot.Spec.Source.PersistentVolumeClaimName)
				assert.Equal(t, "mongod-data-pod-0", *snapshot.Spec.Source.PersistentVolumeClaimName)
			},
		},
		"returns existing snapshot without recreating": {
			existingObjects: []client.Object{existingSnapshot},
			rsName:          "rs0",
			pvc:             "mongod-data-pod-0",
			check: func(t *testing.T, snapshot *volumesnapshotv1.VolumeSnapshot) {
				assert.Equal(t, naming.VolumeSnapshotName(bcp, "rs0"), snapshot.GetName())
				require.NotNil(t, snapshot.Status)
				assert.Equal(t, ptr.To(true), snapshot.Status.ReadyToUse)
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.existingObjects...).
				Build()

			b := &snapshotBackups{}
			snapshot, err := b.reconcileSnapshot(ctx, cl, tt.rsName, tt.pvc, bcp)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, snapshot)
			if tt.check != nil {
				tt.check(t, snapshot)
			}
		})
	}
}

func TestSnapshotBackups_ReconcileSnapshots(t *testing.T) {
	ctx := context.Background()
	scheme := newSnapshotTestScheme(t)

	const namespace = "default"

	bcp := &api.PerconaServerMongoDBBackup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-backup",
			Namespace: namespace,
		},
		Spec: api.PerconaServerMongoDBBackupSpec{
			VolumeSnapshotClass: ptr.To("csi-snapclass"),
		},
	}

	snapshotName := func(rsName string) string {
		return naming.VolumeSnapshotName(bcp, rsName)
	}

	readySnapshot := func(rsName string) *volumesnapshotv1.VolumeSnapshot {
		return &volumesnapshotv1.VolumeSnapshot{
			ObjectMeta: metav1.ObjectMeta{
				Name:      snapshotName(rsName),
				Namespace: namespace,
			},
			Status: &volumesnapshotv1.VolumeSnapshotStatus{
				ReadyToUse: ptr.To(true),
			},
		}
	}

	tests := map[string]struct {
		existingObjects []client.Object
		meta            *pbmBackup.BackupMeta
		wantDone        bool
		wantErr         bool
		wantSnapshots   []api.SnapshotInfo
	}{
		"empty replsets returns done immediately": {
			meta: &pbmBackup.BackupMeta{
				Replsets: []pbmBackup.BackupReplset{},
			},
			wantDone:      true,
			wantSnapshots: []api.SnapshotInfo{},
		},
		"replset not copy-ready skips snapshot creation": {
			meta: &pbmBackup.BackupMeta{
				Replsets: []pbmBackup.BackupReplset{
					{Name: "rs0", Node: "pod-0.rs0.svc", Status: defs.StatusRunning},
				},
			},
			wantDone:      false,
			wantSnapshots: []api.SnapshotInfo{},
		},
		"all copy-ready with ready snapshots returns done": {
			existingObjects: []client.Object{readySnapshot("rs0")},
			meta: &pbmBackup.BackupMeta{
				Replsets: []pbmBackup.BackupReplset{
					{Name: "rs0", Node: "pod-0.rs0.svc", Status: defs.StatusCopyReady},
				},
			},
			wantDone: true,
			wantSnapshots: []api.SnapshotInfo{
				{ReplsetName: "rs0", SnapshotName: snapshotName("rs0")},
			},
		},
		"copy-ready but snapshot not yet ready": {
			existingObjects: []client.Object{
				&volumesnapshotv1.VolumeSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name:      snapshotName("rs0"),
						Namespace: namespace,
					},
					Status: &volumesnapshotv1.VolumeSnapshotStatus{
						ReadyToUse: ptr.To(false),
					},
				},
			},
			meta: &pbmBackup.BackupMeta{
				Replsets: []pbmBackup.BackupReplset{
					{Name: "rs0", Node: "pod-0.rs0.svc", Status: defs.StatusCopyReady},
				},
			},
			wantDone: false,
			wantSnapshots: []api.SnapshotInfo{
				{ReplsetName: "rs0", SnapshotName: snapshotName("rs0")},
			},
		},
		"snapshot with error returns error": {
			existingObjects: []client.Object{
				&volumesnapshotv1.VolumeSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name:      snapshotName("rs0"),
						Namespace: namespace,
					},
					Status: &volumesnapshotv1.VolumeSnapshotStatus{
						Error: &volumesnapshotv1.VolumeSnapshotError{
							Message: ptr.To("snapshot creation failed: insufficient storage"),
						},
					},
				},
			},
			meta: &pbmBackup.BackupMeta{
				Replsets: []pbmBackup.BackupReplset{
					{Name: "rs0", Node: "pod-0.rs0.svc", Status: defs.StatusCopyReady},
				},
			},
			wantErr: true,
		},
		"invalid node name format returns error": {
			meta: &pbmBackup.BackupMeta{
				Replsets: []pbmBackup.BackupReplset{
					{Name: "rs0", Node: ".invalid-node", Status: defs.StatusCopyReady},
				},
			},
			wantErr: true,
		},
		"multiple replsets with one not copy-ready": {
			existingObjects: []client.Object{readySnapshot("rs0")},
			meta: &pbmBackup.BackupMeta{
				Replsets: []pbmBackup.BackupReplset{
					{Name: "rs0", Node: "pod-0.rs0.svc", Status: defs.StatusCopyReady},
					{Name: "rs1", Node: "pod-1.rs1.svc", Status: defs.StatusRunning},
				},
			},
			wantDone: false,
			wantSnapshots: []api.SnapshotInfo{
				{ReplsetName: "rs0", SnapshotName: snapshotName("rs0")},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.existingObjects...).
				Build()

			b := &snapshotBackups{}
			done, snapshots, err := b.reconcileSnapshots(ctx, cl, bcp, tt.meta)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantDone, done)
			assert.Equal(t, tt.wantSnapshots, snapshots)
		})
	}
}

func TestSnapshotBackups_Status(t *testing.T) {
	ctx := context.Background()
	scheme := newSnapshotTestScheme(t)

	const (
		namespace = "default"
		pbmName   = "test-backup"
	)

	newCR := func() *api.PerconaServerMongoDBBackup {
		return &api.PerconaServerMongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pbmName,
				Namespace: namespace,
			},
			Spec: api.PerconaServerMongoDBBackupSpec{
				Type:                defs.ExternalBackup,
				VolumeSnapshotClass: ptr.To("csi-snapclass"),
			},
			Status: api.PerconaServerMongoDBBackupStatus{
				PBMname: pbmName,
			},
		}
	}

	// Snapshot name for rs0 given backup named "test-backup".
	rs0SnapshotName := naming.VolumeSnapshotName(newCR(), "rs0")

	readyRS0Snapshot := &volumesnapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs0SnapshotName,
			Namespace: namespace,
		},
		Status: &volumesnapshotv1.VolumeSnapshotStatus{
			ReadyToUse: ptr.To(true),
		},
	}

	pendingRS0Snapshot := &volumesnapshotv1.VolumeSnapshot{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rs0SnapshotName,
			Namespace: namespace,
		},
		Status: &volumesnapshotv1.VolumeSnapshotStatus{
			ReadyToUse: ptr.To(false),
		},
	}

	rs0CopyReadyReplset := pbmBackup.BackupReplset{
		Name:   "rs0",
		Node:   "pod-0.rs0.svc",
		Status: defs.StatusCopyReady,
	}

	tests := map[string]struct {
		setupMock       func(*MockPBM)
		existingObjects []client.Object
		cr              *api.PerconaServerMongoDBBackup
		cluster         *api.PerconaServerMongoDB
		wantState       api.BackupState
		wantErr         bool
		check           func(t *testing.T, status api.PerconaServerMongoDBBackupStatus)
	}{
		"metadata not found leaves status unchanged": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(nil, pbmErrors.ErrNotFound)
			},
			cr:        newCR(),
			cluster:   &api.PerconaServerMongoDB{},
			wantState: api.BackupStateNew,
		},
		"get backup meta error returns error": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(nil, fmt.Errorf("mongodb connection error"))
			},
			cr:      newCR(),
			cluster: &api.PerconaServerMongoDB{},
			wantErr: true,
		},
		"backup in error state": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusError,
					StartTS:          1234567890,
					LastTransitionTS: 1234567900,
					Err:              "backup failed",
				}, nil)
			},
			cr:        newCR(),
			cluster:   &api.PerconaServerMongoDB{},
			wantState: api.BackupStateError,
			check: func(t *testing.T, status api.PerconaServerMongoDBBackupStatus) {
				assert.Equal(t, "backup failed", status.Error)
				assert.Equal(t, defs.ExternalBackup, status.Type)
				require.NotNil(t, status.StartAt)
				assert.Equal(t, time.Unix(1234567890, 0), status.StartAt.Time)
				require.NotNil(t, status.LastTransition)
				assert.Equal(t, time.Unix(1234567900, 0), status.LastTransition.Time)
			},
		},
		"backup in starting state within deadline": {
			setupMock: func(m *MockPBM) {
				startTime := time.Now().UTC().Add(-30 * time.Second)
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusStarting,
					StartTS:          startTime.Unix(),
					LastTransitionTS: startTime.Unix(),
				}, nil)
			},
			cr:        newCR(),
			cluster:   &api.PerconaServerMongoDB{},
			wantState: api.BackupStateRequested,
		},
		"backup in starting state beyond default deadline": {
			setupMock: func(m *MockPBM) {
				startTime := time.Now().UTC().Add(-150 * time.Second)
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusStarting,
					StartTS:          startTime.Unix(),
					LastTransitionTS: startTime.Unix(),
				}, nil)
			},
			cr:        newCR(),
			cluster:   &api.PerconaServerMongoDB{},
			wantState: api.BackupStateError,
			check: func(t *testing.T, status api.PerconaServerMongoDBBackupStatus) {
				assert.Equal(t, pbmStartingDeadlineErrMsg, status.Error)
			},
		},
		"backup in starting state beyond custom deadline": {
			setupMock: func(m *MockPBM) {
				startTime := time.Now().UTC().Add(-20 * time.Second)
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusStarting,
					StartTS:          startTime.Unix(),
					LastTransitionTS: startTime.Unix(),
				}, nil)
			},
			cr:      newCR(),
			cluster: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					Backup: api.BackupSpec{
						StartingDeadlineSeconds: ptr.To(int64(10)),
					},
				},
			},
			wantState: api.BackupStateError,
			check: func(t *testing.T, status api.PerconaServerMongoDBBackupStatus) {
				assert.Equal(t, pbmStartingDeadlineErrMsg, status.Error)
			},
		},
		"backup completed successfully": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusDone,
					StartTS:          1234567890,
					LastTransitionTS: 1234567900,
					LastWriteTS:      primitive.Timestamp{T: 1234567950},
				}, nil)
			},
			cr:        newCR(),
			cluster:   &api.PerconaServerMongoDB{},
			wantState: api.BackupStateReady,
			check: func(t *testing.T, status api.PerconaServerMongoDBBackupStatus) {
				assert.Equal(t, defs.ExternalBackup, status.Type)
				require.NotNil(t, status.StartAt)
				assert.Equal(t, time.Unix(1234567890, 0), status.StartAt.Time)
				require.NotNil(t, status.CompletedAt)
				assert.Equal(t, time.Unix(1234567900, 0), status.CompletedAt.Time)
				require.NotNil(t, status.LastWriteAt)
				assert.Equal(t, time.Unix(1234567950, 0), status.LastWriteAt.Time)
				require.NotNil(t, status.LastTransition)
				assert.Equal(t, time.Unix(1234567900, 0), status.LastTransition.Time)
			},
		},
		"copy-ready with snapshots not yet ready does not finish backup": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusCopyReady,
					StartTS:          1234567890,
					LastTransitionTS: 1234567900,
					Replsets:         []pbmBackup.BackupReplset{rs0CopyReadyReplset},
				}, nil)
				// FinishBackup must NOT be called when snapshots are not ready.
			},
			existingObjects: []client.Object{pendingRS0Snapshot},
			cr:              newCR(),
			cluster:         &api.PerconaServerMongoDB{},
			wantState:       api.BackupStateRunning,
			check: func(t *testing.T, status api.PerconaServerMongoDBBackupStatus) {
				require.Len(t, status.Snapshots, 1)
				assert.Equal(t, "rs0", status.Snapshots[0].ReplsetName)
				assert.Equal(t, rs0SnapshotName, status.Snapshots[0].SnapshotName)
			},
		},
		"copy-ready with all snapshots ready calls FinishBackup": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusCopyReady,
					StartTS:          1234567890,
					LastTransitionTS: 1234567900,
					Replsets:         []pbmBackup.BackupReplset{rs0CopyReadyReplset},
				}, nil)
				m.EXPECT().FinishBackup(gomock.Any(), pbmName).Return(nil)
			},
			existingObjects: []client.Object{readyRS0Snapshot},
			cr:              newCR(),
			cluster:         &api.PerconaServerMongoDB{},
			wantState:       api.BackupStateRunning,
			check: func(t *testing.T, status api.PerconaServerMongoDBBackupStatus) {
				require.Len(t, status.Snapshots, 1)
				assert.Equal(t, "rs0", status.Snapshots[0].ReplsetName)
				assert.Equal(t, rs0SnapshotName, status.Snapshots[0].SnapshotName)
			},
		},
		"copy-ready with FinishBackup error propagates error": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusCopyReady,
					StartTS:          1234567890,
					LastTransitionTS: 1234567900,
					Replsets:         []pbmBackup.BackupReplset{rs0CopyReadyReplset},
				}, nil)
				m.EXPECT().FinishBackup(gomock.Any(), pbmName).Return(fmt.Errorf("finish backup failed"))
			},
			existingObjects: []client.Object{readyRS0Snapshot},
			cr:              newCR(),
			cluster:         &api.PerconaServerMongoDB{},
			wantErr:         true,
		},
		"copy-ready with snapshot error propagates error": {
			setupMock: func(m *MockPBM) {
				m.EXPECT().GetBackupMeta(gomock.Any(), pbmName).Return(&pbmBackup.BackupMeta{
					Name:             pbmName,
					Status:           defs.StatusCopyReady,
					StartTS:          1234567890,
					LastTransitionTS: 1234567900,
					Replsets:         []pbmBackup.BackupReplset{rs0CopyReadyReplset},
				}, nil)
			},
			existingObjects: []client.Object{
				&volumesnapshotv1.VolumeSnapshot{
					ObjectMeta: metav1.ObjectMeta{
						Name:      rs0SnapshotName,
						Namespace: namespace,
					},
					Status: &volumesnapshotv1.VolumeSnapshotStatus{
						Error: &volumesnapshotv1.VolumeSnapshotError{
							Message: ptr.To("csi driver error: disk unavailable"),
						},
					},
				},
			},
			cr:      newCR(),
			cluster: &api.PerconaServerMongoDB{},
			wantErr: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockPBM := NewMockPBM(ctrl)
			tt.setupMock(mockPBM)

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(tt.existingObjects...).
				Build()

			b := &snapshotBackups{pbm: mockPBM}
			status, err := b.Status(ctx, cl, tt.cluster, tt.cr)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.wantState, status.State)
			if tt.check != nil {
				tt.check(t, status)
			}
		})
	}
}
