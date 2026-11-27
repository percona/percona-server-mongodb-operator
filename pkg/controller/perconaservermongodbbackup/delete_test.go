package perconaservermongodbbackup

import (
	"context"
	"testing"
	"time"

	"github.com/golang/mock/gomock"
	volumesnapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestReconcileDeleteCanceledBackup(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		backupType    defs.BackupType
		pause         bool
		clusterState  psmdbv1.AppState
		pbmErr        error
		deleteErr     error
		missingMeta   bool
		withSnapshot  bool
		waitForLock   bool
		wantFinalizer bool
	}{
		"retains the finalizer when file deletion fails": {
			backupType:    defs.LogicalBackup,
			deleteErr:     errors.New("storage unavailable"),
			wantFinalizer: true,
		},
		"waits for lock release and skips PITR cleanup": {
			backupType:  defs.LogicalBackup,
			waitForLock: true,
		},
		"retains the finalizer when PBM is unavailable": {
			backupType:    defs.LogicalBackup,
			clusterState:  psmdbv1.AppStateReady,
			pbmErr:        errors.New("PBM unavailable"),
			wantFinalizer: true,
		},
		"retains the finalizer while the cluster is stopping and PBM is unavailable": {
			backupType:    defs.LogicalBackup,
			pause:         true,
			clusterState:  psmdbv1.AppStateStopping,
			pbmErr:        errors.New("PBM unavailable"),
			wantFinalizer: true,
		},
		"retains the finalizer while the cluster is resuming and PBM is unavailable": {
			backupType:    defs.LogicalBackup,
			clusterState:  psmdbv1.AppStatePaused,
			pbmErr:        errors.New("PBM unavailable"),
			wantFinalizer: true,
		},
		"deletes snapshots when the cluster is paused and PBM is unavailable": {
			backupType:   defs.ExternalBackup,
			pause:        true,
			clusterState: psmdbv1.AppStatePaused,
			pbmErr:       errors.New("PBM unavailable"),
			withSnapshot: true,
		},
		"deletes remaining snapshot backup metadata": {
			backupType: defs.ExternalBackup,
		},
		"checks for snapshot backup locks when metadata is missing": {
			backupType:  defs.ExternalBackup,
			missingMeta: true,
			waitForLock: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := backupWithState("backup", "ns", "", "cluster", psmdbv1.BackupStateCanceled)
			cr.DeletionTimestamp = new(metav1.Now())
			cr.Finalizers = []string{naming.FinalizerDeleteBackup}
			cr.Spec.Type = tt.backupType
			cr.Status.PBMname = "pbm-backup"

			if tt.backupType == defs.ExternalBackup {
				cr.Spec.VolumeSnapshotClass = new("snapshot-class")
				cr.Status.ReplsetNames = []string{"rs0"}
			} else {
				cr.Spec.StorageName = "s3"
				cr.Status.StorageName = "s3"
				cr.Status.S3 = &psmdbv1.BackupStorageS3Spec{
					Bucket:            "bucket",
					CredentialsSecret: "backup-credentials",
				}
			}

			var snapshot *volumesnapshotv1.VolumeSnapshot
			if tt.withSnapshot {
				snapshot = &volumesnapshotv1.VolumeSnapshot{
					Name:      naming.VolumeSnapshotName(cr, "rs0"),
					Namespace: cr.Namespace,
				}
				cr.Status.Snapshots = psmdbv1.SnapshotInfos{{ReplsetName: "rs0", SnapshotName: snapshot.Name}}
			}

			cluster := &psmdbv1.PerconaServerMongoDB{
				Name:      cr.Spec.ClusterName,
				Namespace: cr.Namespace,
				Spec: psmdbv1.PerconaServerMongoDBSpec{
					Pause:     tt.pause,
					CRVersion: version.Version(),
					Backup: psmdbv1.BackupSpec{
						Storages: map[string]psmdbv1.BackupStorageSpec{
							"s3": {Type: psmdbv1.BackupStorageS3, Main: true},
						},
					},
				},
				Status: psmdbv1.PerconaServerMongoDBStatus{State: tt.clusterState},
			}
			scheme := backupScheme(t)
			require.NoError(t, volumesnapshotv1.AddToScheme(scheme))
			builder := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(cr.DeepCopy(), cluster).
				WithStatusSubresource(cr).
				WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						if _, ok := obj.(*corev1.Secret); ok {
							t.Error("cleanup reached the direct storage fallback")
						}
						return cl.Get(ctx, key, obj, opts...)
					},
				})
			if snapshot != nil {
				builder.WithObjects(snapshot)
			}
			cl := builder.Build()

			pbm := NewMockPBM(gomock.NewController(t))
			r := &ReconcilePerconaServerMongoDBBackup{
				client:    cl,
				apiReader: cl,
				newPBMFunc: func(context.Context, client.Client, *psmdbv1.PerconaServerMongoDB) (backup.PBM, error) {
					if tt.pbmErr != nil {
						return nil, tt.pbmErr
					}
					return pbm, nil
				},
			}
			backupName := cr.Status.PBMname
			meta := &backup.BackupMeta{
				Name:   backupName,
				OPID:   "backup-op",
				Status: defs.StatusCancelled,
			}
			opID := meta.OPID
			var metaErr error
			if tt.missingMeta {
				meta, opID, metaErr = nil, "", pbmErrors.ErrNotFound
			}
			request := reconcile.Request{NamespacedName: client.ObjectKeyFromObject(cr)}

			lockStates := []bool{false}
			if tt.waitForLock {
				lockStates = []bool{true, false}
			}
			for _, locked := range lockStates {
				if tt.pbmErr == nil {
					calls := []*gomock.Call{
						pbm.EXPECT().GetBackupMeta(gomock.Any(), backupName).Return(meta, metaErr),
						pbm.EXPECT().HasBackupLock(gomock.Any(), opID).Return(locked, nil),
					}
					switch {
					case !locked && tt.backupType == defs.LogicalBackup:
						calls = append(calls, pbm.EXPECT().DeleteBackup(gomock.Any(), backupName).Return(tt.deleteErr))
					case !locked && !tt.missingMeta:
						calls = append(calls, pbm.EXPECT().DeleteBackupMeta(gomock.Any(), backupName).Return(nil))
					}
					gomock.InOrder(append(calls, pbm.EXPECT().Close(gomock.Any()).Return(nil))...)
				}

				result, err := r.Reconcile(t.Context(), request)
				require.NoError(t, err)
				if locked {
					assert.Positive(t, result.RequeueAfter)
				}

				got := new(psmdbv1.PerconaServerMongoDBBackup)
				err = cl.Get(t.Context(), request.NamespacedName, got)
				if locked || tt.wantFinalizer {
					require.NoError(t, err)
					assert.Equal(t, cr.Finalizers, got.Finalizers)
				} else {
					assert.True(t, k8serrors.IsNotFound(err), "backup should be removed after cleanup: %v", err)
				}
				if snapshot != nil {
					err := cl.Get(t.Context(), client.ObjectKeyFromObject(snapshot), &volumesnapshotv1.VolumeSnapshot{})
					if locked || tt.wantFinalizer {
						assert.NoError(t, err)
					} else {
						assert.True(t, k8serrors.IsNotFound(err), "snapshot should be removed after cleanup: %v", err)
					}
				}
			}
		})
	}
}

type erroringGetClient struct {
	client.Client
	err error
}

func (e *erroringGetClient) Get(ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*psmdbv1.PerconaServerMongoDB); ok {
		return e.err
	}
	return e.Client.Get(ctx, key, obj, opts...)
}

func TestReconcileDeleteClearsFinalizersWhenPBMUnavailable(t *testing.T) {
	t.Parallel()

	const (
		ns          = "ns"
		clusterName = "restored-mongodb"
		backupName  = "backup-error"
	)

	now := metav1.NewTime(time.Now())
	cr := &psmdbv1.PerconaServerMongoDBBackup{
		Name:              backupName,
		Namespace:         ns,
		UID:               types.UID("cccccccc-cccc-cccc-cccc-cccccccccccc"),
		DeletionTimestamp: &now,
		Finalizers: []string{
			naming.FinalizerDeleteBackup,
			naming.FinalizerReleaseLock,
		},
		Spec: psmdbv1.PerconaServerMongoDBBackupSpec{
			ClusterName: clusterName,
			StorageName: "s3-storage",
		},
		Status: psmdbv1.PerconaServerMongoDBBackupStatus{
			State: psmdbv1.BackupStateError,
			Error: "create pbm object: create PBM connection: mongo: no documents in result",
		},
	}

	cluster := &psmdbv1.PerconaServerMongoDB{
		Name:      clusterName,
		Namespace: ns,
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			CRVersion: "1.21.0",
		},
	}

	lease := &coordv1.Lease{
		Name:      naming.BackupLeaseName(clusterName),
		Namespace: ns,
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: new(naming.BackupHolderId(cr)),
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(backupScheme(t)).
		WithRuntimeObjects(cr.DeepCopy(), cluster.DeepCopy(), lease.DeepCopy()).
		WithStatusSubresource(cr).
		Build()

	r := &ReconcilePerconaServerMongoDBBackup{
		client:    cl,
		apiReader: cl,
		newPBMFunc: func(ctx context.Context, c client.Client, cluster *psmdbv1.PerconaServerMongoDB) (backup.PBM, error) {
			return nil, errors.New("create PBM connection: mongo: no documents in result")
		},
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		Name: backupName, Namespace: ns,
	})
	require.NoError(t, err)

	got := &psmdbv1.PerconaServerMongoDBBackup{}
	err = cl.Get(context.Background(), types.NamespacedName{Name: backupName, Namespace: ns}, got)
	// Fake client removes the object once finalizers are cleared and DeletionTimestamp is set.
	// Either outcome proves finalizers no longer block deletion.
	if err == nil {
		assert.Empty(t, got.GetFinalizers(), "finalizers must be cleared even when PBM setup fails")
		return
	}
	require.NoError(t, client.IgnoreNotFound(err))
}

func TestReconcileDeleteRetriesOnTransientClusterFetchError(t *testing.T) {
	t.Parallel()

	const (
		ns          = "ns"
		clusterName = "restored-mongodb"
		backupName  = "backup-transient"
	)

	now := metav1.NewTime(time.Now())
	cr := &psmdbv1.PerconaServerMongoDBBackup{
		Name:              backupName,
		Namespace:         ns,
		UID:               types.UID("dddddddd-dddd-dddd-dddd-dddddddddddd"),
		DeletionTimestamp: &now,
		Finalizers: []string{
			naming.FinalizerDeleteBackup,
			naming.FinalizerReleaseLock,
		},
		Spec: psmdbv1.PerconaServerMongoDBBackupSpec{
			ClusterName: clusterName,
			StorageName: "s3-storage",
		},
		Status: psmdbv1.PerconaServerMongoDBBackupStatus{
			State: psmdbv1.BackupStateReady,
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(backupScheme(t)).
		WithRuntimeObjects(cr.DeepCopy()).
		WithStatusSubresource(cr).
		Build()

	transientErr := k8serrors.NewServiceUnavailable("etcd unavailable")
	wrapped := &erroringGetClient{Client: cl, err: transientErr}

	r := &ReconcilePerconaServerMongoDBBackup{
		client:    wrapped,
		apiReader: cl,
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		Name: backupName, Namespace: ns,
	})
	require.Error(t, err, "transient cluster fetch error must be propagated so the request is retried")

	got := &psmdbv1.PerconaServerMongoDBBackup{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: backupName, Namespace: ns}, got))
	assert.ElementsMatch(t, []string{naming.FinalizerDeleteBackup, naming.FinalizerReleaseLock}, got.GetFinalizers(),
		"finalizers must not be touched when the cluster could not be inspected")
}

func TestReconcileDeleteKeepsForeignFinalizers(t *testing.T) {
	t.Parallel()

	const (
		ns          = "ns"
		clusterName = "restored-mongodb"
		backupName  = "backup-foreign"
		foreign     = "example.com/backup-protection"
	)

	now := metav1.NewTime(time.Now())
	cr := &psmdbv1.PerconaServerMongoDBBackup{
		Name:              backupName,
		Namespace:         ns,
		UID:               types.UID("eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"),
		DeletionTimestamp: &now,
		Finalizers: []string{
			naming.FinalizerDeleteBackup,
			foreign,
		},
		Spec: psmdbv1.PerconaServerMongoDBBackupSpec{
			ClusterName: clusterName,
			StorageName: "s3-storage",
		},
		Status: psmdbv1.PerconaServerMongoDBBackupStatus{
			State: psmdbv1.BackupStateReady,
		},
	}

	cluster := &psmdbv1.PerconaServerMongoDB{
		Name:      clusterName,
		Namespace: ns,
		Spec: psmdbv1.PerconaServerMongoDBSpec{
			CRVersion: "1.21.0",
		},
	}

	cl := fake.NewClientBuilder().
		WithScheme(backupScheme(t)).
		WithRuntimeObjects(cr.DeepCopy(), cluster.DeepCopy()).
		WithStatusSubresource(cr).
		Build()

	r := &ReconcilePerconaServerMongoDBBackup{
		client:    cl,
		apiReader: cl,
		newPBMFunc: func(ctx context.Context, c client.Client, cluster *psmdbv1.PerconaServerMongoDB) (backup.PBM, error) {
			return nil, errors.New("create PBM connection: mongo: no documents in result")
		},
	}

	_, err := r.Reconcile(context.Background(), reconcile.Request{
		Name: backupName, Namespace: ns,
	})
	require.NoError(t, err)

	got := &psmdbv1.PerconaServerMongoDBBackup{}
	require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: backupName, Namespace: ns}, got),
		"the backup must outlive its own finalizers while %s is still set", foreign)
	assert.Equal(t, []string{foreign}, got.GetFinalizers(),
		"the operator must remove only the finalizers it owns")
}
