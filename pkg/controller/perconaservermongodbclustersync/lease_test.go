package perconaservermongodbclustersync

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordv1 "k8s.io/api/coordination/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func clusterSyncScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, coordv1.AddToScheme(s))
	require.NoError(t, psmdbv1.SchemeBuilder.AddToScheme(s))
	return s
}

func newClusterSyncCR(name, ns, cluster string, finalizers ...string) *psmdbv1.PerconaServerMongoDBClusterSync {
	return &psmdbv1.PerconaServerMongoDBClusterSync{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  ns,
			UID:        types.UID(name + "-uid"),
			Finalizers: finalizers,
		},
		Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{ClusterName: cluster},
	}
}

func leaseFor(cr *psmdbv1.PerconaServerMongoDBClusterSync, holder string) *coordv1.Lease {
	return &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.ClusterSyncLeaseName(cr.Spec.ClusterName),
			Namespace: cr.Namespace,
		},
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: &holder,
		},
	}
}

func TestReleaseClusterSyncLease(t *testing.T) {
	tests := map[string]struct {
		cr           *psmdbv1.PerconaServerMongoDBClusterSync
		seedHolder   string
		seedLease    bool
		wantDeleted  bool
		wantErrMatch string
	}{
		"lease held by us: deleted": {
			cr:          newClusterSyncCR("sync", "ns", "tgt"),
			seedLease:   true,
			wantDeleted: true,
		},
		"lease not found: no error, nothing to delete": {
			cr:        newClusterSyncCR("sync", "ns", "tgt"),
			seedLease: false,
		},
		"lease held by another CR: swallowed (we don't own it)": {
			cr:         newClusterSyncCR("sync", "ns", "tgt"),
			seedLease:  true,
			seedHolder: "different-holder",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			objs := []runtime.Object{}
			if tc.seedLease {
				holder := tc.seedHolder
				if holder == "" {
					holder = naming.ClusterSyncHolderId(tc.cr)
				}
				objs = append(objs, leaseFor(tc.cr, holder))
			}
			cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithRuntimeObjects(objs...).Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

			err := r.releaseClusterSyncLease(context.Background(), tc.cr)
			require.NoError(t, err)

			gotLease := &coordv1.Lease{}
			getErr := cl.Get(context.Background(), types.NamespacedName{
				Name:      naming.ClusterSyncLeaseName(tc.cr.Spec.ClusterName),
				Namespace: tc.cr.Namespace,
			}, gotLease)
			if tc.wantDeleted {
				require.True(t, k8serrors.IsNotFound(getErr), "lease should have been deleted")
				return
			}
			if !tc.seedLease {
				require.True(t, k8serrors.IsNotFound(getErr), "lease was never seeded")
				return
			}
			require.NoError(t, getErr, "foreign lease must not be touched")
		})
	}
}

func TestEnsureReleaseLockFinalizer(t *testing.T) {
	tests := map[string]struct {
		existing []string
		wantAdd  bool
	}{
		"no finalizers: adds release-lock":            {existing: nil, wantAdd: true},
		"unrelated finalizer present: appends":        {existing: []string{"other.example/x"}, wantAdd: true},
		"already present: no patch needed, no error":  {existing: []string{naming.FinalizerReleaseLock}, wantAdd: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newClusterSyncCR("sync", "ns", "tgt", tc.existing...)
			cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithObjects(cr).Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

			require.NoError(t, r.ensureReleaseLockFinalizer(context.Background(), cr))

			got := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, got))

			has := false
			for _, f := range got.Finalizers {
				if f == naming.FinalizerReleaseLock {
					has = true
					break
				}
			}
			assert.True(t, has, "release-lock finalizer must be present")
			if tc.wantAdd {
				assert.Greater(t, len(got.Finalizers), len(tc.existing))
			} else {
				assert.Equal(t, len(tc.existing), len(got.Finalizers))
			}
		})
	}
}

func TestClusterBusyByBackupOrRestore(t *testing.T) {
	const (
		ns          = "ns"
		clusterName = "tgt"
		otherClus   = "otherCluster"
	)
	backupLease := func() *coordv1.Lease {
		h := "backup-uid"
		return &coordv1.Lease{
			ObjectMeta: metav1.ObjectMeta{Name: naming.BackupLeaseName(clusterName), Namespace: ns},
			Spec: coordv1.LeaseSpec{
				AcquireTime:    &metav1.MicroTime{Time: time.Now()},
				HolderIdentity: &h,
			},
		}
	}
	restoreOn := func(name, cluster string, state psmdbv1.RestoreState) *psmdbv1.PerconaServerMongoDBRestore {
		return &psmdbv1.PerconaServerMongoDBRestore{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       psmdbv1.PerconaServerMongoDBRestoreSpec{ClusterName: cluster},
			Status:     psmdbv1.PerconaServerMongoDBRestoreStatus{State: state},
		}
	}

	tests := map[string]struct {
		objs           []runtime.Object
		wantBusy       bool
		wantReasonHint string
	}{
		"no lease, no restores: free": {
			objs:     nil,
			wantBusy: false,
		},
		"backup lease active: busy": {
			objs:           []runtime.Object{backupLease()},
			wantBusy:       true,
			wantReasonHint: "backup is in progress",
		},
		"restore Running for our cluster: busy": {
			objs:           []runtime.Object{restoreOn("rst1", clusterName, psmdbv1.RestoreStateRunning)},
			wantBusy:       true,
			wantReasonHint: "restore",
		},
		"restore Requested for our cluster: busy": {
			objs:           []runtime.Object{restoreOn("rst1", clusterName, psmdbv1.RestoreStateRequested)},
			wantBusy:       true,
			wantReasonHint: "restore",
		},
		"restore Waiting for our cluster: busy": {
			objs:           []runtime.Object{restoreOn("rst1", clusterName, psmdbv1.RestoreStateWaiting)},
			wantBusy:       true,
			wantReasonHint: "restore",
		},
		"restore Ready: terminal, not busy": {
			objs:     []runtime.Object{restoreOn("rst1", clusterName, psmdbv1.RestoreStateReady)},
			wantBusy: false,
		},
		"restore Error: terminal, not busy": {
			objs:     []runtime.Object{restoreOn("rst1", clusterName, psmdbv1.RestoreStateError)},
			wantBusy: false,
		},
		"restore Rejected: terminal, not busy": {
			objs:     []runtime.Object{restoreOn("rst1", clusterName, psmdbv1.RestoreStateRejected)},
			wantBusy: false,
		},
		"restore Running but targets a different cluster: not busy": {
			objs:     []runtime.Object{restoreOn("rst1", otherClus, psmdbv1.RestoreStateRunning)},
			wantBusy: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newClusterSyncCR("sync", ns, clusterName)
			cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithRuntimeObjects(tc.objs...).Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

			busy, reason, err := r.clusterBusyByBackupOrRestore(context.Background(), cr)
			require.NoError(t, err)
			assert.Equal(t, tc.wantBusy, busy)
			if tc.wantReasonHint != "" {
				assert.Contains(t, reason, tc.wantReasonHint)
			} else {
				assert.Empty(t, reason)
			}
		})
	}
}

func TestWriteAction(t *testing.T) {
	assert.True(t, writeAction(actionStart))
	assert.True(t, writeAction(actionResume))
	assert.False(t, writeAction(actionPause))
	assert.False(t, writeAction(actionFinalize))
	assert.False(t, writeAction(actionNone))
}

func TestHandleDeletion(t *testing.T) {
	t.Run("release-lock finalizer absent: noop, no lease touched", func(t *testing.T) {
		// "other.example/keep" is just a stand-in: the API server
		// refuses a deletionTimestamp without any finalizers, but
		// handleDeletion only acts on FinalizerReleaseLock so an
		// unrelated finalizer exercises the "no-op" branch.
		cr := newClusterSyncCR("sync", "ns", "tgt", "other.example/keep")
		cr.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		lease := leaseFor(cr, naming.ClusterSyncHolderId(cr))
		cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithObjects(cr).WithRuntimeObjects(lease).Build()
		r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

		_, err := r.handleDeletion(context.Background(), cr)
		require.NoError(t, err)

		got := &coordv1.Lease{}
		require.NoError(t, cl.Get(context.Background(), types.NamespacedName{
			Name: lease.Name, Namespace: lease.Namespace,
		}, got), "lease must survive when our finalizer was absent")
	})

	t.Run("finalizer present: releases lease and drops finalizer", func(t *testing.T) {
		cr := newClusterSyncCR("sync", "ns", "tgt", naming.FinalizerReleaseLock, "other.example/keep")
		cr.DeletionTimestamp = &metav1.Time{Time: time.Now()}
		lease := leaseFor(cr, naming.ClusterSyncHolderId(cr))
		cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithObjects(cr).WithRuntimeObjects(lease).Build()
		r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

		_, err := r.handleDeletion(context.Background(), cr)
		require.NoError(t, err)

		gotLease := &coordv1.Lease{}
		err = cl.Get(context.Background(), types.NamespacedName{
			Name: lease.Name, Namespace: lease.Namespace,
		}, gotLease)
		require.True(t, k8serrors.IsNotFound(err), "lease must be released")

		gotCR := &psmdbv1.PerconaServerMongoDBClusterSync{}
		require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, gotCR))
		assert.Equal(t, []string{"other.example/keep"}, gotCR.Finalizers,
			"release-lock finalizer dropped, unrelated finalizer preserved")
	})
}
