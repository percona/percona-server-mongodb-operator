package perconaservermongodbbackup

import (
	"errors"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	pbmBackup "github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/v2/bson"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func TestCancellationRequested(t *testing.T) {
	tests := map[string]struct {
		annotations map[string]string
		state       api.BackupState
		deleting    bool
		want        bool
	}{
		"no annotation": {},
		"false annotation": {
			annotations: map[string]string{naming.AnnotationCancelBackup: "false"},
		},
		"true annotation": {
			annotations: map[string]string{naming.AnnotationCancelBackup: "true"},
			want:        true,
		},
		"annotation removed while canceling": {
			state: api.BackupStateCanceling,
			want:  true,
		},
		"deletion requests cancellation": {
			state: api.BackupStateRunning, deleting: true, want: true,
		},
		"deletion overrides false annotation": {
			annotations: map[string]string{naming.AnnotationCancelBackup: "false"},
			state:       api.BackupStateRunning, deleting: true, want: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDBBackup{
				Annotations: tt.annotations,
				Status:      api.PerconaServerMongoDBBackupStatus{State: tt.state},
			}
			if tt.deleting {
				cr.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			}
			assert.Equal(t, tt.want, cancellationRequested(cr))
		})
	}
}

func TestCancelBackupUnstarted(t *testing.T) {
	cr := &api.PerconaServerMongoDBBackup{}
	r := &ReconcilePerconaServerMongoDBBackup{}

	err := r.cancelBackup(t.Context(), cr)

	require.NoError(t, err)
	assert.Equal(t, api.BackupStateCanceled, cr.Status.State)
	assert.Nil(t, cr.Status.CompletedAt)
	require.NotNil(t, cr.Status.LastTransition)
	assert.False(t, cr.Status.LastTransition.IsZero())
}

func TestCheckCancellationTimeout(t *testing.T) {
	now := time.Now()
	stale := now.Add(-5 * time.Minute)
	meta := &pbmBackup.BackupMeta{
		OPID: "backup-op", Status: defs.StatusRunning,
		LastTransitionTS: stale.Unix(), Hb: bson.Timestamp{T: uint32(stale.Unix())},
	}
	heartbeat := *meta
	heartbeat.Hb.T = uint32(now.Unix())
	transition := *meta
	transition.LastTransitionTS = now.Unix()

	tests := map[string]struct {
		lastTransition *metav1.Time
		meta           *pbmBackup.BackupMeta
		deadline       *int64
		checkLock      bool
		activeLock     bool
		lockErr        error
		wantError      string
		wantStatusErr  string
	}{
		"initialize cancellation time": {},
		"recent cancellation": {
			lastTransition: &metav1.Time{Time: now}, meta: meta,
		},
		"recent heartbeat": {
			lastTransition: &metav1.Time{Time: stale}, meta: &heartbeat,
		},
		"recent PBM transition": {
			lastTransition: &metav1.Time{Time: stale}, meta: &transition,
		},
		"configured deadline": {
			lastTransition: &metav1.Time{Time: stale}, meta: meta, deadline: new(int64(600)),
		},
		"missing metadata past deadline": {
			lastTransition: &metav1.Time{Time: stale}, checkLock: true,
			wantStatusErr: "PBM backup metadata not found during cancellation within the allowed timeout",
		},
		"agent stopped responding": {
			lastTransition: &metav1.Time{Time: stale}, meta: meta, checkLock: true,
			wantStatusErr: "PBM backup agent stopped responding before cancellation could finish",
		},
		"active lock": {
			lastTransition: &metav1.Time{Time: stale}, meta: meta, checkLock: true, activeLock: true,
		},
		"lock lookup fails": {
			lastTransition: &metav1.Time{Time: stale}, meta: meta, checkLock: true,
			lockErr: errors.New("locks unavailable"), wantError: "check backup locks during cancellation recovery: locks unavailable",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			pbm := NewMockPBM(gomock.NewController(t))
			if tt.checkLock {
				opID := ""
				if tt.meta != nil {
					opID = tt.meta.OPID
				}
				pbm.EXPECT().HasBackupLock(gomock.Any(), opID).Return(tt.activeLock, tt.lockErr)
			}
			cluster := &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					Backup: api.BackupSpec{StartingDeadlineSeconds: tt.deadline},
				},
			}
			cr := &api.PerconaServerMongoDBBackup{
				Spec: api.PerconaServerMongoDBBackupSpec{Type: defs.LogicalBackup},
			}
			status := api.PerconaServerMongoDBBackupStatus{
				State: api.BackupStateCanceling, LastTransition: tt.lastTransition,
			}

			err := checkCancellationTimeout(t.Context(), nil, cluster, cr, pbm, &status, tt.meta)

			if tt.wantError != "" {
				require.EqualError(t, err, tt.wantError)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantStatusErr, status.Error)
			assert.Nil(t, status.CompletedAt)
			require.NotNil(t, status.LastTransition)
			if tt.wantStatusErr != "" {
				assert.Equal(t, api.BackupStateError, status.State)
				assert.False(t, status.LastTransition.IsZero())
			} else {
				assert.Equal(t, api.BackupStateCanceling, status.State)
				if tt.lastTransition != nil {
					assert.Equal(t, tt.lastTransition, status.LastTransition)
				}
			}
		})
	}
}

func TestCleanupCanceledSnapshots(t *testing.T) {
	cr := &api.PerconaServerMongoDBBackup{
		Name: "backup", Namespace: "ns",
		Status: api.PerconaServerMongoDBBackupStatus{
			Snapshots:    api.SnapshotInfos{{SnapshotName: "recorded-snapshot", ReplsetName: "old-rs"}},
			ReplsetNames: []string{"rs0"},
		},
	}
	recorded := &volumesnapshotv1.VolumeSnapshot{Name: "recorded-snapshot", Namespace: cr.Namespace}
	deterministic := &volumesnapshotv1.VolumeSnapshot{
		Name: naming.VolumeSnapshotName(cr, "rs0"), Namespace: cr.Namespace,
		Finalizers: []string{"test/delayed-deletion"},
	}
	unrelated := &volumesnapshotv1.VolumeSnapshot{Name: "other-backup", Namespace: cr.Namespace}
	cl := fake.NewClientBuilder().WithScheme(newSnapshotTestScheme(t)).
		WithObjects(recorded, deterministic, unrelated).Build()

	done, err := cleanupCanceledSnapshots(t.Context(), cl, cr)
	require.NoError(t, err)
	assert.False(t, done)
	err = cl.Get(t.Context(), client.ObjectKeyFromObject(recorded), recorded)
	assert.True(t, k8serrors.IsNotFound(err))
	require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(deterministic), deterministic))
	assert.NotNil(t, deterministic.DeletionTimestamp)

	done, err = cleanupCanceledSnapshots(t.Context(), cl, cr)
	require.NoError(t, err)
	assert.False(t, done, "wait for the snapshot finalizer")

	deterministic.Finalizers = nil
	require.NoError(t, cl.Update(t.Context(), deterministic))
	done, err = cleanupCanceledSnapshots(t.Context(), cl, cr)
	require.NoError(t, err)
	assert.True(t, done)
	require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(unrelated), unrelated))
}
