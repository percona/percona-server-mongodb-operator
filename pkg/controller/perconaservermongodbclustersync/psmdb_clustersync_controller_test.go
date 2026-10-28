package perconaservermongodbclustersync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync/client"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/record"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync"
)

func TestApplyObservedStatus(t *testing.T) {
	tests := map[string]struct {
		initial                psmdbv1.PerconaServerMongoDBClusterSyncStatus
		observed               client.Status
		wantState              psmdbv1.ClusterSyncState
		wantLag                int64
		wantError              string
		wantStartedAtSet       bool
		wantStartedAtUnchanged bool
		wantConditions         map[string]metav1.ConditionStatus
	}{
		"idle does not set startedAt": {
			observed:       client.Status{State: "idle"},
			wantState:      psmdbv1.ClusterSyncStateIdle,
			wantConditions: map[string]metav1.ConditionStatus{psmdbv1.ConditionClusterSyncRunning: metav1.ConditionFalse},
		},
		"paused does not set startedAt": {
			observed:       client.Status{State: "paused"},
			wantState:      psmdbv1.ClusterSyncStatePaused,
			wantConditions: map[string]metav1.ConditionStatus{psmdbv1.ConditionClusterSyncRunning: metav1.ConditionFalse},
		},
		"running sets startedAt and emits Running condition": {
			observed:         client.Status{State: "running", LagTimeSeconds: 2},
			wantState:        psmdbv1.ClusterSyncStateRunning,
			wantLag:          2,
			wantStartedAtSet: true,
			wantConditions:   map[string]metav1.ConditionStatus{psmdbv1.ConditionClusterSyncRunning: metav1.ConditionTrue},
		},
		"existing startedAt is preserved when state returns to idle": {
			initial: psmdbv1.PerconaServerMongoDBClusterSyncStatus{
				StartedAt: &metav1.Time{Time: time.Now().Add(-time.Hour)},
			},
			observed:               client.Status{State: "idle"},
			wantState:              psmdbv1.ClusterSyncStateIdle,
			wantStartedAtUnchanged: true,
			wantConditions:         map[string]metav1.ConditionStatus{psmdbv1.ConditionClusterSyncRunning: metav1.ConditionFalse},
		},
		"finalizing does not emit Finalized condition": {
			observed:       client.Status{State: "finalizing"},
			wantState:      psmdbv1.ClusterSyncStateFinalizing,
			wantConditions: map[string]metav1.ConditionStatus{psmdbv1.ConditionClusterSyncRunning: metav1.ConditionFalse},
		},
		"finalized emits Finalized condition": {
			observed:  client.Status{State: "finalized"},
			wantState: psmdbv1.ClusterSyncStateFinalized,
			wantConditions: map[string]metav1.ConditionStatus{
				psmdbv1.ConditionClusterSyncRunning:   metav1.ConditionFalse,
				psmdbv1.ConditionClusterSyncFinalized: metav1.ConditionTrue,
			},
		},
		"failed propagates error message": {
			observed:       client.Status{State: "failed", Error: "source unreachable"},
			wantState:      psmdbv1.ClusterSyncStateFailed,
			wantError:      "source unreachable",
			wantConditions: map[string]metav1.ConditionStatus{psmdbv1.ConditionClusterSyncRunning: metav1.ConditionFalse},
		},
		"running to failed flips Running condition to False": {
			initial: psmdbv1.PerconaServerMongoDBClusterSyncStatus{
				State: psmdbv1.ClusterSyncStateRunning,
				Conditions: []metav1.Condition{{
					Type:   psmdbv1.ConditionClusterSyncRunning,
					Status: metav1.ConditionTrue,
					Reason: "PCSMRunning",
				}},
				StartedAt: &metav1.Time{Time: time.Now().Add(-time.Minute)},
			},
			observed:               client.Status{State: "failed", Error: "source unreachable"},
			wantState:              psmdbv1.ClusterSyncStateFailed,
			wantError:              "source unreachable",
			wantStartedAtUnchanged: true,
			wantConditions:         map[string]metav1.ConditionStatus{psmdbv1.ConditionClusterSyncRunning: metav1.ConditionFalse},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := tc.initial.DeepCopy()
			before := s.StartedAt

			const generation = 7
			applyObservedStatus(s, tc.observed, generation)

			assert.Equal(t, tc.wantState, s.State)
			assert.Equal(t, tc.wantLag, s.LagTimeSeconds)
			assert.Equal(t, tc.wantError, s.Error)

			switch {
			case tc.wantStartedAtUnchanged:
				assert.Equal(t, before, s.StartedAt)
			case tc.wantStartedAtSet:
				require.NotNil(t, s.StartedAt)
			default:
				assert.Nil(t, s.StartedAt)
			}

			for ct, want := range tc.wantConditions {
				got := meta.FindStatusCondition(s.Conditions, ct)
				require.NotNil(t, got, "expected condition %s to be set", ct)
				assert.Equal(t, want, got.Status, "condition %s status mismatch", ct)
				assert.Equal(t, int64(generation), got.ObservedGeneration,
					"condition %s must say which spec generation it describes", ct)
			}
		})
	}
}

// fakePCSM records every call so InvokeAction tests can assert exactly
// one verb fires per action with the expected arguments.
type fakePCSM struct {
	started    *client.StartOptions
	paused     int
	resumed    *bool
	finalized  int
	statusResp client.Status
	statusErr  error
	actionErr  error
	active     string
}

func (f *fakePCSM) ActiveInstance() string { return f.active }

func (f *fakePCSM) Status(_ context.Context) (client.Status, error) {
	return f.statusResp, f.statusErr
}
func (f *fakePCSM) Start(_ context.Context, opts client.StartOptions) error {
	f.started = &opts
	return f.actionErr
}
func (f *fakePCSM) Pause(_ context.Context) error {
	f.paused++
	return f.actionErr
}
func (f *fakePCSM) Resume(_ context.Context, fromFailure bool) error {
	f.resumed = &fromFailure
	return f.actionErr
}
func (f *fakePCSM) Finalize(_ context.Context) error {
	f.finalized++
	return f.actionErr
}

func TestInvokeAction(t *testing.T) {
	tests := map[string]struct {
		action  modeAction
		cr      *psmdbv1.PerconaServerMongoDBClusterSync
		state   psmdbv1.ClusterSyncState
		assert  func(t *testing.T, f *fakePCSM)
		wantErr error
	}{
		"start passes excludeNamespaces from spec": {
			action: actionStart,
			cr: &psmdbv1.PerconaServerMongoDBClusterSync{
				Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
					ExcludeNamespaces: []string{"db.*", "tmp.coll"},
				},
			},
			assert: func(t *testing.T, f *fakePCSM) {
				require.NotNil(t, f.started)
				assert.Equal(t, []string{"db.*", "tmp.coll"}, f.started.ExcludeNamespaces)
			},
		},
		"resume passes fromFailure=true when last state was failed": {
			action: actionResume,
			cr:     &psmdbv1.PerconaServerMongoDBClusterSync{},
			state:  psmdbv1.ClusterSyncStateFailed,
			assert: func(t *testing.T, f *fakePCSM) {
				require.NotNil(t, f.resumed)
				assert.True(t, *f.resumed)
			},
		},
		"resume passes fromFailure=false from clean paused": {
			action: actionResume,
			cr:     &psmdbv1.PerconaServerMongoDBClusterSync{},
			state:  psmdbv1.ClusterSyncStatePaused,
			assert: func(t *testing.T, f *fakePCSM) {
				require.NotNil(t, f.resumed)
				assert.False(t, *f.resumed)
			},
		},
		"pause calls pause": {
			action: actionPause,
			cr:     &psmdbv1.PerconaServerMongoDBClusterSync{},
			assert: func(t *testing.T, f *fakePCSM) { assert.Equal(t, 1, f.paused) },
		},
		"finalize calls finalize": {
			action: actionFinalize,
			cr:     &psmdbv1.PerconaServerMongoDBClusterSync{},
			assert: func(t *testing.T, f *fakePCSM) { assert.Equal(t, 1, f.finalized) },
		},
		"none is a noop": {
			action: actionNone,
			cr:     &psmdbv1.PerconaServerMongoDBClusterSync{},
			assert: func(t *testing.T, f *fakePCSM) {
				assert.Nil(t, f.started)
				assert.Nil(t, f.resumed)
				assert.Zero(t, f.paused+f.finalized)
			},
		},
		"propagates client error": {
			action:  actionPause,
			cr:      &psmdbv1.PerconaServerMongoDBClusterSync{},
			wantErr: errors.New("some error"),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := &fakePCSM{}
			if tc.wantErr != nil {
				f.actionErr = tc.wantErr
			}
			err := invokeAction(t.Context(), f, tc.action, tc.cr, tc.state)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			if tc.assert != nil {
				tc.assert(t, f)
			}
		})
	}
}

func TestReconcileModeInheritedFinalized(t *testing.T) {
	tests := map[string]struct {
		spec        psmdbv1.PerconaServerMongoDBClusterSyncSpec
		status      psmdbv1.PerconaServerMongoDBClusterSyncStatus
		observed    client.Status
		wantStarted bool
	}{
		"new CR against target with inherited finalized state issues /start": {
			spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
				ClusterName: "tgt",
				Mode:        psmdbv1.ClusterSyncModeRunning,
			},
			observed:    client.Status{State: "finalized", LagTimeSeconds: 297},
			wantStarted: true,
		},
		"intentional finalize from running: normal flow, no /start": {
			spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
				ClusterName: "tgt",
				Mode:        psmdbv1.ClusterSyncModeFinalized,
			},
			status: psmdbv1.PerconaServerMongoDBClusterSyncStatus{
				Mode:      psmdbv1.ClusterSyncModeRunning,
				StartedAt: &metav1.Time{Time: time.Now().Add(-time.Hour)},
			},
			observed: client.Status{State: "finalized"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				Name: "sync3", Namespace: "ns", UID: "u",
				Spec:   tc.spec,
				Status: tc.status,
			}
			cl := fake.NewClientBuilder().
				WithScheme(clusterSyncScheme(t)).
				WithStatusSubresource(cr).
				WithObjects(cr).
				Build()
			rec := record.NewFakeRecorder(4)
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, recorder: rec}

			f := &fakePCSM{statusResp: tc.observed}

			err := r.reconcileMode(context.Background(), cr, f, 1)
			require.NoError(t, err)

			if tc.wantStarted {
				assert.NotNil(t, f.started, "expected /start to be issued")
			} else {
				assert.Nil(t, f.started, "did not expect /start")
			}
		})
	}
}

// TestReadyReplicas pins the readiness gate for an HA group.
//
// The pre-HA gate required every replica to be Ready before the operator
// would talk to PCSM at all. Under HA that is actively harmful: with three
// instances, one standby stuck Pending or crash-looping would freeze mode
// transitions and status updates for a group that is replicating
// perfectly through its active instance. One Ready instance is enough to
// drive PCSM, so the gate drops to "at least one" and the shortfall is
// reported in status.readyReplicas instead of blocking.
//
// The observedGeneration check stays: a deployment whose controller has
// not yet seen the latest spec reports replica counts for the *previous*
// spec, and acting on those would race the rollout.
func TestReadyReplicas(t *testing.T) {
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{Name: "sync", Namespace: "ns"}

	tests := map[string]struct {
		deployment *appsv1.Deployment
		want       int32
	}{
		"missing deployment is not ready": {
			want: 0,
		},
		"stale observedGeneration is treated as not ready": {
			deployment: deploymentWith(cr, 2, 1, 3),
			want:       0,
		},
		"no ready replicas": {
			deployment: deploymentWith(cr, 1, 1, 0),
			want:       0,
		},
		"single instance ready": {
			deployment: deploymentWith(cr, 1, 1, 1),
			want:       1,
		},
		"degraded HA group still reports its ready instances": {
			deployment: deploymentWith(cr, 4, 4, 1),
			want:       1,
		},
		"fully ready HA group": {
			deployment: deploymentWith(cr, 4, 4, 3),
			want:       3,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			b := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t))
			if tc.deployment != nil {
				b = b.WithObjects(tc.deployment)
			}
			r := &ReconcilePerconaServerMongoDBClusterSync{client: b.Build()}

			got, err := r.readyReplicas(t.Context(), cr)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	// A missing deployment is a normal bootstrap state and reads as zero;
	// any other read failure must not be flattened into the same answer,
	// or an unreachable API server looks like a group that is still
	// starting and reconcile silently skips every PCSM command.
	t.Run("a non-NotFound read failure is returned", func(t *testing.T) {
		boom := errors.New("apiserver down")
		cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(context.Context, k8sclient.WithWatch, k8sclient.ObjectKey, k8sclient.Object, ...k8sclient.GetOption) error {
					return boom
				},
			}).Build()
		r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

		got, err := r.readyReplicas(t.Context(), cr)

		require.ErrorIs(t, err, boom)
		assert.Zero(t, got)
	})
}

func deploymentWith(cr *psmdbv1.PerconaServerMongoDBClusterSync, generation, observed, ready int64) *appsv1.Deployment {
	return &appsv1.Deployment{
		Name:       clustersync.DeploymentName(cr),
		Namespace:  cr.Namespace,
		Generation: generation,
		Status: appsv1.DeploymentStatus{
			ObservedGeneration: observed,
			ReadyReplicas:      int32(ready),
		},
	}
}

// TestReconcilePDB covers the full lifecycle of the disruption budget,
// including the part that is easy to get wrong: removal.
//
// A PDB is only meaningful above one instance. If the group is scaled back
// to a single instance and the budget is left behind, it keeps asserting
// "at most one pod may be unavailable" over a one-pod deployment, which
// blocks every node drain on that node indefinitely. So scaling down must
// delete it, and deleting something that is already gone must not fail the
// reconcile — a reconcile runs many times and only the first one finds
// anything to delete.
func TestReconcilePDB(t *testing.T) {
	tests := map[string]struct {
		size        int32
		existing    bool
		foreign     bool
		wantPDB     bool
		wantDeletes int
	}{
		"HA group creates the budget":                    {size: 3, wantPDB: true},
		"HA group is idempotent over an existing budget": {size: 3, existing: true, wantPDB: true},
		"single instance creates no budget":              {size: 1},
		"scale down to one deletes the existing budget":  {size: 1, existing: true, wantDeletes: 1},
		"a budget this CR does not own is left alone":    {size: 3, existing: true, foreign: true, wantPDB: true},
		"scale down does not delete a foreign budget":    {size: 1, existing: true, foreign: true, wantPDB: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				Name: "sync", Namespace: "ns", UID: "u",
				Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{Size: tc.size},
			}
			scheme := clusterSyncScheme(t)
			deletes := 0
			b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).
				WithInterceptorFuncs(interceptor.Funcs{
					Delete: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.DeleteOption) error {
						deletes++
						return c.Delete(ctx, obj, opts...)
					},
				})
			if tc.existing {
				haCR := cr.DeepCopy()
				haCR.Spec.Size = 3
				existing := clustersync.PodDisruptionBudget(haCR)
				if tc.foreign {
					existing.Spec.MaxUnavailable = nil
					existing.Spec.MinAvailable = new(intstr.FromInt32(2))
				} else {
					require.NoError(t, controllerutil.SetControllerReference(cr, existing, scheme))
				}
				b = b.WithObjects(existing)
			}
			cl := b.Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, scheme: scheme}

			require.NoError(t, r.reconcilePDB(t.Context(), cr))
			require.NoError(t, r.reconcilePDB(t.Context(), cr), "reconcile must be idempotent")
			assert.Equal(t, tc.wantDeletes, deletes,
				"reconcile runs every few seconds; it must not issue a write call when there is nothing to remove")

			pdb := &policyv1.PodDisruptionBudget{}
			err := cl.Get(t.Context(), types.NamespacedName{
				Name:      clustersync.DeploymentName(cr),
				Namespace: cr.Namespace,
			}, pdb)

			if !tc.wantPDB {
				assert.True(t, k8serrors.IsNotFound(err), "budget must not outlive a scale-down, got %v", err)
				return
			}
			require.NoError(t, err)
			if tc.foreign {
				assert.Empty(t, pdb.OwnerReferences)
				assert.Equal(t, new(intstr.FromInt32(2)), pdb.Spec.MinAvailable,
					"a budget owned by someone else must keep its own spec")
				return
			}
			require.Len(t, pdb.OwnerReferences, 1, "budget must be garbage-collected with the CR")
			assert.Equal(t, cr.Name, pdb.OwnerReferences[0].Name)
			assert.Equal(t, clustersync.Labels(cr), pdb.Spec.Selector.MatchLabels)
		})
	}
}

// TestReconcileModeReportsHAGroup covers what the CR tells a user about
// the PCSM group: how many instances are ready, and which one currently
// holds the lease.
//
// status.activeInstance is not just cosmetic — the next reconcile seeds
// the PCSM client with it so commands go straight to the writer instead
// of probing every standby. A handover between reconciles is the only
// externally visible sign that a failover happened, so it also emits an
// event; emitting one per reconcile instead of per handover would bury
// that signal in noise.
func TestReconcileModeReportsHAGroup(t *testing.T) {
	tests := map[string]struct {
		prevActive string
		active     string
		ready      int32
		wantActive string
		wantEvent  string
	}{
		"first observation records the active instance without an event": {
			active:     "sync-pcsm-0",
			ready:      3,
			wantActive: "sync-pcsm-0",
		},
		"unchanged active instance emits no event": {
			prevActive: "sync-pcsm-0",
			active:     "sync-pcsm-0",
			ready:      3,
			wantActive: "sync-pcsm-0",
		},
		"handover to another instance is reported as a failover": {
			prevActive: "sync-pcsm-0",
			active:     "sync-pcsm-2",
			ready:      3,
			wantActive: "sync-pcsm-2",
			wantEvent:  "PCSMFailover",
		},
		"degraded group keeps the last known active instance": {
			prevActive: "sync-pcsm-1",
			active:     "",
			ready:      1,
			wantActive: "sync-pcsm-1",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				Name: "sync", Namespace: "ns", UID: "u",
				Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
					ClusterName: "tgt",
					Mode:        psmdbv1.ClusterSyncModeRunning,
					Size:        3,
				},
				Status: psmdbv1.PerconaServerMongoDBClusterSyncStatus{
					Mode:           psmdbv1.ClusterSyncModeRunning,
					ActiveInstance: tc.prevActive,
				},
			}
			cl := fake.NewClientBuilder().
				WithScheme(clusterSyncScheme(t)).
				WithStatusSubresource(cr).
				WithObjects(cr).
				Build()
			rec := record.NewFakeRecorder(4)
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, recorder: rec}

			err := r.reconcileMode(t.Context(), cr, &fakePCSM{
				statusResp: client.Status{State: "running"},
				active:     tc.active,
			}, tc.ready)
			require.NoError(t, err)

			stored := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, stored))
			assert.Equal(t, tc.wantActive, stored.Status.ActiveInstance)
			assert.Equal(t, tc.ready, stored.Status.ReadyReplicas)
			assert.Equal(t, int32(3), stored.Status.Size)

			if tc.wantEvent == "" {
				assert.Empty(t, rec.Events, "no event expected")
				return
			}
			require.Len(t, rec.Events, 1)
			assert.Contains(t, <-rec.Events, tc.wantEvent)
		})
	}
}

// TestReconcileModeReportsClusterBusyOnce guards against event and log
// spam on a path that can stay true for hours.
//
// A backup holds the target cluster for as long as it runs, and reconcile
// re-runs every few seconds, so an ungated Eventf here would attach
// hundreds of ClusterBusy events to the CR and bury everything else in the
// operator log. The hold is reported once per distinct reason; the
// standing condition lives in status.error, which is where a user looks
// anyway and which is deduplicated by definition.
func TestReconcileModeReportsClusterBusyOnce(t *testing.T) {
	const ns, clusterName = "ns", "tgt"

	cr := &psmdbv1.PerconaServerMongoDBClusterSync{
		Name: "sync", Namespace: ns, UID: "u",
		Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
			ClusterName: clusterName,
			Mode:        psmdbv1.ClusterSyncModeRunning,
		},
	}
	holder := "backup-uid"
	lease := &coordv1.Lease{
		Name: naming.BackupLeaseName(clusterName), Namespace: ns,
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: &holder,
		},
	}
	cl := fake.NewClientBuilder().
		WithScheme(clusterSyncScheme(t)).
		WithStatusSubresource(cr).
		WithObjects(cr, lease).
		Build()
	rec := record.NewFakeRecorder(16)
	r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, recorder: rec}

	// Three passes of the same hold, re-reading the CR in between exactly
	// as the real reconcile loop does.
	for range 3 {
		stored := &psmdbv1.PerconaServerMongoDBClusterSync{}
		require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: cr.Name, Namespace: ns}, stored))
		require.NoError(t, r.reconcileMode(t.Context(), stored, &fakePCSM{
			statusResp: client.Status{State: "idle"},
		}, 1))
	}

	stored := &psmdbv1.PerconaServerMongoDBClusterSync{}
	require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: cr.Name, Namespace: ns}, stored))
	assert.Contains(t, stored.Status.Error, "backup is in progress")
	assert.Nil(t, stored.Status.StartedAt, "PCSM must not be started while the cluster is busy")

	require.Len(t, rec.Events, 1, "ClusterBusy must be reported once per distinct reason, not once per reconcile")
	assert.Contains(t, <-rec.Events, "ClusterBusy")
}

// TestWriteStatus covers the write path every reconcile ends in.
//
// Reconcile requeues every few seconds for the lifetime of the CR, so an
// unconditional status Update is one API write per CR per requeue forever,
// plus a watch event that wakes this controller up again. Identical status
// must therefore be a no-op. The missing-CR case matters because the user
// can delete the CR while a reconcile is mid-flight, and losing a race
// with deletion is not a reconcile failure.
func TestWriteStatus(t *testing.T) {
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{
		Name: "sync", Namespace: "ns",
		Status: psmdbv1.PerconaServerMongoDBClusterSyncStatus{State: psmdbv1.ClusterSyncStateRunning},
	}
	cl := fake.NewClientBuilder().
		WithScheme(clusterSyncScheme(t)).
		WithStatusSubresource(cr).
		WithObjects(cr).
		Build()
	r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}
	nn := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

	read := func(t *testing.T) *psmdbv1.PerconaServerMongoDBClusterSync {
		t.Helper()
		got := &psmdbv1.PerconaServerMongoDBClusterSync{}
		require.NoError(t, cl.Get(t.Context(), nn, got))
		return got
	}

	t.Run("a changed status is persisted", func(t *testing.T) {
		require.NoError(t, r.writeStatus(t.Context(), cr, psmdbv1.PerconaServerMongoDBClusterSyncStatus{
			State:         psmdbv1.ClusterSyncStateRunning,
			ReadyReplicas: 3,
		}))
		assert.Equal(t, int32(3), read(t).Status.ReadyReplicas)
	})

	t.Run("an identical status is not written again", func(t *testing.T) {
		before := read(t).ResourceVersion
		require.NoError(t, r.writeStatus(t.Context(), cr, psmdbv1.PerconaServerMongoDBClusterSyncStatus{
			State:         psmdbv1.ClusterSyncStateRunning,
			ReadyReplicas: 3,
		}))
		assert.Equal(t, before, read(t).ResourceVersion,
			"a steady-state reconcile must not write status, or it wakes itself up every requeue")
	})

	t.Run("a CR deleted mid-reconcile is not an error", func(t *testing.T) {
		require.NoError(t, cl.Delete(t.Context(), cr.DeepCopy()))
		assert.NoError(t, r.writeStatus(t.Context(), cr, psmdbv1.PerconaServerMongoDBClusterSyncStatus{
			State: psmdbv1.ClusterSyncStateFinalized,
		}))
	})
}

// TestIsPCSMUnreachable documents which failures are transient. Both
// sentinels mean "nobody to talk to right now" — no ready pod, or a group
// mid-election with no lease holder — and must requeue quietly rather than
// being logged as reconcile errors every 5 seconds.
func TestIsPCSMUnreachable(t *testing.T) {
	tests := map[string]struct {
		err  error
		want bool
	}{
		"no ready pod":                    {err: client.ErrPCSMNotReady, want: true},
		"no active instance":              {err: client.ErrNoActivePCSM, want: true},
		"wrapped no active instance":      {err: fmt.Errorf("reconcile mode: %w", client.ErrNoActivePCSM), want: true},
		"genuine command failure":         {err: errors.New("exit status 1"), want: false},
		"nil error is not unreachability": {err: nil, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, isPCSMUnreachable(tc.err))
		})
	}
}

type recordingMongoClient struct {
	mongo.Client

	getUserInfoResp *mongo.User
	getUserInfoErr  error

	createUserErr     error
	updateUserPassErr error
	updateRolesErr    error

	createUserCalls     int
	updateUserPassCalls int
	updateRolesCalls    int

	lastCreatePass  string
	lastUpdatePass  string
	lastUpdateRoles []mongo.Role
}

func (m *recordingMongoClient) GetUserInfo(_ context.Context, _, _ string) (*mongo.User, error) {
	return m.getUserInfoResp, m.getUserInfoErr
}

func (m *recordingMongoClient) CreateUser(_ context.Context, _, _, pwd string, _ ...mongo.Role) error {
	m.createUserCalls++
	m.lastCreatePass = pwd
	return m.createUserErr
}

func (m *recordingMongoClient) UpdateUserPass(_ context.Context, _, _, pwd string) error {
	m.updateUserPassCalls++
	m.lastUpdatePass = pwd
	return m.updateUserPassErr
}

func (m *recordingMongoClient) UpdateUserRoles(_ context.Context, _, _ string, roles []mongo.Role) error {
	m.updateRolesCalls++
	m.lastUpdateRoles = roles
	return m.updateRolesErr
}

func TestEnsureTargetMongoUser(t *testing.T) {
	const (
		username = "clustersync-cr"
		password = "new-password"
	)
	passwordMAC := passwordAnnotationMAC([]byte(password))

	secretWith := func(annotations map[string]string) *corev1.Secret {
		return &corev1.Secret{
			Annotations: annotations,
			Data: map[string][]byte{
				targetUserSecretUsernameKey: []byte(username),
				targetUserSecretPasswordKey: []byte(password),
			},
		}
	}
	existingWithDesiredRoles := &mongo.User{DB: "admin", Roles: append([]mongo.Role(nil), syncTargetUserRoles...)}

	tests := map[string]struct {
		mc          *recordingMongoClient
		sec         *corev1.Secret
		wantErr     string
		wantMutated bool
		assert      func(t *testing.T, m *recordingMongoClient, s *corev1.Secret)
	}{
		"missing user is created and password hash annotation is set": {
			mc:          &recordingMongoClient{getUserInfoResp: nil},
			sec:         secretWith(nil),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.createUserCalls)
				assert.Equal(t, password, m.lastCreatePass)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"steady state converged does no writes": {
			mc:          &recordingMongoClient{getUserInfoResp: existingWithDesiredRoles},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantMutated: false,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.createUserCalls)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"password hash mismatch triggers UpdateUserPass and annotation refresh": {
			mc:          &recordingMongoClient{getUserInfoResp: existingWithDesiredRoles},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: "stale"}),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, password, m.lastUpdatePass)
				assert.Zero(t, m.updateRolesCalls)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"recreated secret with existing mongo user triggers UpdateUserPass and creates annotation map": {
			mc:          &recordingMongoClient{getUserInfoResp: existingWithDesiredRoles},
			sec:         secretWith(nil),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, password, m.lastUpdatePass)
				assert.Zero(t, m.updateRolesCalls)
				require.NotNil(t, s.Annotations)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"new cr reconcile against stale mongo user with drifted roles applies both password and roles": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin", Roles: []mongo.Role{{Role: "clusterMonitor", DB: "admin"}}},
			},
			sec:         secretWith(nil),
			wantMutated: true,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, password, m.lastUpdatePass)
				assert.Equal(t, 1, m.updateRolesCalls)
				assert.Equal(t, syncTargetUserRoles, m.lastUpdateRoles)
				assert.Equal(t, passwordMAC, s.Annotations[targetUserPasswordMACAnnotation])
			},
		},
		"role drift triggers UpdateUserRoles without touching password": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin", Roles: []mongo.Role{{Role: "clusterMonitor", DB: "admin"}}},
			},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantMutated: false,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.updateUserPassCalls)
				assert.Equal(t, 1, m.updateRolesCalls)
				assert.Equal(t, syncTargetUserRoles, m.lastUpdateRoles)
			},
		},
		"roles in a different order are treated as equal": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin", Roles: []mongo.Role{
					{Role: "readWriteAnyDatabase", DB: "admin"},
					{Role: "clusterManager", DB: "admin"},
					{Role: "clusterMonitor", DB: "admin"},
					{Role: "restore", DB: "admin"},
				}},
			},
			sec:         secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantMutated: false,
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"GetUserInfo error short-circuits with no writes": {
			mc:      &recordingMongoClient{getUserInfoErr: errors.New("error")},
			sec:     secretWith(nil),
			wantErr: "look up target user",
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.createUserCalls)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"CreateUser error is wrapped": {
			mc:      &recordingMongoClient{getUserInfoResp: nil, createUserErr: errors.New("error")},
			sec:     secretWith(nil),
			wantErr: "create target user",
		},
		"UpdateUserPass error stops before UpdateUserRoles": {
			mc: &recordingMongoClient{
				getUserInfoResp:   &mongo.User{DB: "admin"},
				updateUserPassErr: errors.New("error"),
			},
			sec:     secretWith(nil),
			wantErr: "sync target user password",
			assert: func(t *testing.T, m *recordingMongoClient, s *corev1.Secret) {
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"UpdateUserRoles error is wrapped": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin"},
				updateRolesErr:  errors.New("error"),
			},
			sec:     secretWith(map[string]string{targetUserPasswordMACAnnotation: passwordMAC}),
			wantErr: "sync target user roles",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			mutated, err := ensureTargetMongoUser(t.Context(), tc.mc, tc.sec)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.wantMutated, mutated)
			}
			if tc.assert != nil {
				tc.assert(t, tc.mc, tc.sec)
			}
		})
	}
}

func targetCluster(crVersion string) *psmdbv1.PerconaServerMongoDB {
	return &psmdbv1.PerconaServerMongoDB{
		Name: "target", Namespace: "ns",
		Spec: psmdbv1.PerconaServerMongoDBSpec{CRVersion: crVersion},
	}
}

// TestReconcileDeploymentConvergesOnSize is the unit-level counterpart of
// the HA scale steps in e2e-tests/clustersync/run.
//
// The assertion that matters is the pod template: spec.size must change
// replicas and nothing else. When the template varies with size, the
// Deployment controller rolls the group through a new ReplicaSet instead
// of adding replicas, which terminates the ACTIVE instance and forces a
// failover on every scale change. Asserting replicas alone misses that
// entirely, which is how it reached a live cluster.
func TestReconcileDeploymentConvergesOnSize(t *testing.T) {
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{
		Name: "sync", Namespace: "ns", UID: "u",
		Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{Size: 1, Image: "pcsm:1.0.0"},
	}
	scheme := clusterSyncScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()
	r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, scheme: scheme}

	get := func(t *testing.T) *appsv1.Deployment {
		t.Helper()
		dep := &appsv1.Deployment{}
		require.NoError(t, cl.Get(t.Context(), types.NamespacedName{
			Name:      clustersync.DeploymentName(cr),
			Namespace: cr.Namespace,
		}, dep))
		return dep
	}

	require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0")))
	initial := get(t)

	require.NotNil(t, initial.Spec.Replicas)
	assert.Equal(t, int32(1), *initial.Spec.Replicas)
	assert.Equal(t, appsv1.RecreateDeploymentStrategyType, initial.Spec.Strategy.Type)
	require.Len(t, initial.OwnerReferences, 1, "deployment must be garbage-collected with the CR")
	assert.Equal(t, cr.Name, initial.OwnerReferences[0].Name)
	assert.Equal(t, clustersync.Labels(cr), initial.Labels)

	t.Run("reconcile is idempotent", func(t *testing.T) {
		require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0")))
		assert.Equal(t, initial.Spec, get(t).Spec)
	})

	for _, size := range []int32{3, 5, 1} {
		t.Run(fmt.Sprintf("scaling to %d changes only replicas", size), func(t *testing.T) {
			cr.Spec.Size = size
			require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0")))
			dep := get(t)

			require.NotNil(t, dep.Spec.Replicas)
			assert.Equal(t, size, *dep.Spec.Replicas)
			assert.Equal(t, initial.Spec.Template, dep.Spec.Template,
				"a template change rolls the whole group and forces a PCSM failover")
		})
	}

	t.Run("HA group rolls one at a time without surging", func(t *testing.T) {
		cr.Spec.Size = 3
		require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0")))
		strategy := get(t).Spec.Strategy

		require.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, strategy.Type)
		require.NotNil(t, strategy.RollingUpdate)
		assert.Equal(t, intstr.FromInt32(1), *strategy.RollingUpdate.MaxUnavailable)
		assert.Equal(t, intstr.FromInt32(0), *strategy.RollingUpdate.MaxSurge,
			"surging past spec.size would add an instance the PCSM group never agreed to")
	})
}

// TestReconcileDeploymentVersionGate checks the target cluster's
// crVersion actually reaches the pod-template builders: a cluster that has
// not been bumped to 1.24.0 must keep the 1.23 pod template, so upgrading
// the operator alone never rolls the PCSM pod.
func TestReconcileDeploymentVersionGate(t *testing.T) {
	tests := map[string]struct {
		crVersion   string
		wantDefault bool
	}{
		"older crVersion keeps the 1.23 pod template": {crVersion: "1.23.0"},
		"current crVersion gets the defaults":         {crVersion: "1.24.0", wantDefault: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				Name: "sync", Namespace: "ns", UID: "u",
				Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{Size: 3, Image: "pcsm:1.0.0"},
			}
			scheme := clusterSyncScheme(t)
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, scheme: scheme}

			require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster(tc.crVersion)))

			dep := &appsv1.Deployment{}
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{
				Name:      clustersync.DeploymentName(cr),
				Namespace: cr.Namespace,
			}, dep))

			tmpl := dep.Spec.Template
			require.Len(t, tmpl.Spec.Containers, 1)
			wantPath := clustersync.StatusPath
			if tc.wantDefault {
				wantPath = clustersync.MetricsPath
				assert.NotNil(t, tmpl.Spec.Affinity)
			} else {
				assert.Nil(t, tmpl.Spec.Affinity)
			}
			for name, probe := range map[string]*corev1.Probe{
				"liveness":  tmpl.Spec.Containers[0].LivenessProbe,
				"readiness": tmpl.Spec.Containers[0].ReadinessProbe,
			} {
				require.NotNil(t, probe, name)
				require.NotNil(t, probe.Exec, name)
				assert.Contains(t, strings.Join(probe.Exec.Command, " "), wantPath, name)
			}
		})
	}
}

// TestReconcileDeploymentApplyFailure checks the write error is wrapped
// rather than swallowed: a silent failure here leaves the group at the
// old size while status happily reports the new one.
func TestReconcileDeploymentApplyFailure(t *testing.T) {
	boom := errors.New("etcd unavailable")
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{Name: "sync", Namespace: "ns", UID: "u"}
	scheme := clusterSyncScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.CreateOption) error {
				return boom
			},
		}).Build()
	r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, scheme: scheme}

	err := r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0"))

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "apply clustersync deployment")
}

// TestReconcilePDBApplyFailure keeps the write error on the reconcile
// return path. Swallowing it would leave an HA group with no disruption
// budget while nothing anywhere says so.
func TestReconcilePDBApplyFailure(t *testing.T) {
	boom := errors.New("etcd unavailable")
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{
		Name: "sync", Namespace: "ns", UID: "u",
		Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{Size: 3},
	}
	scheme := clusterSyncScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.CreateOption) error {
				return boom
			},
		}).Build()
	r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, scheme: scheme}

	err := r.reconcilePDB(t.Context(), cr)

	require.ErrorIs(t, err, boom)
	assert.Contains(t, err.Error(), "apply clustersync pdb")
}

// TestReconcileModeSurvivesPCSMFailures covers the failure paths around
// the PCSM call itself.
//
// None of these may return an error to the controller. An HA group has no
// ACTIVE instance for a few seconds on every election, and a failed sync
// is retried on every requeue; returning an error in either case turns a
// normal transient into exponential backoff and a reconcile-failure
// metric. The condition belongs in status.error, which the requeue
// interval already refreshes.
func TestReconcileModeSurvivesPCSMFailures(t *testing.T) {
	tests := map[string]struct {
		prevError  string
		statusErr  error
		actionErr  error
		mode       psmdbv1.ClusterSyncMode
		state      psmdbv1.ClusterSyncState
		wantError  string
		wantPaused int
	}{
		"mid-election group reports no active instance": {
			statusErr: client.ErrNoActivePCSM,
			mode:      psmdbv1.ClusterSyncModeRunning,
			wantError: client.ErrNoActivePCSM.Error(),
		},
		"no ready pod yet": {
			statusErr: client.ErrPCSMNotReady,
			mode:      psmdbv1.ClusterSyncModeRunning,
			wantError: client.ErrPCSMNotReady.Error(),
		},
		"an unexpected status failure is recorded, not returned": {
			statusErr: errors.New("exec failed"),
			mode:      psmdbv1.ClusterSyncModeRunning,
			wantError: "exec failed",
		},
		"a repeated status failure stays in status": {
			prevError: "exec failed",
			statusErr: errors.New("exec failed"),
			mode:      psmdbv1.ClusterSyncModeRunning,
			wantError: "exec failed",
		},
		"a failed transition is recorded, not returned": {
			actionErr:  errors.New("pcsm pause refused"),
			mode:       psmdbv1.ClusterSyncModePaused,
			state:      psmdbv1.ClusterSyncStateRunning,
			wantError:  "pcsm pause refused",
			wantPaused: 1,
		},
		"a repeated failed transition stays in status": {
			prevError:  "pcsm pause refused",
			actionErr:  errors.New("pcsm pause refused"),
			mode:       psmdbv1.ClusterSyncModePaused,
			state:      psmdbv1.ClusterSyncStateRunning,
			wantError:  "pcsm pause refused",
			wantPaused: 1,
		},
		"a transition against an unreachable instance is not an error": {
			actionErr:  client.ErrNoActivePCSM,
			mode:       psmdbv1.ClusterSyncModePaused,
			state:      psmdbv1.ClusterSyncStateRunning,
			wantError:  client.ErrNoActivePCSM.Error(),
			wantPaused: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				Name: "sync", Namespace: "ns", UID: "u",
				Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
					ClusterName: "tgt", Mode: tc.mode, Size: 3,
				},
				Status: psmdbv1.PerconaServerMongoDBClusterSyncStatus{
					Mode:      psmdbv1.ClusterSyncModeRunning,
					State:     tc.state,
					StartedAt: &metav1.Time{Time: time.Unix(0, 0)},
					Error:     tc.prevError,
				},
			}
			cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).
				WithStatusSubresource(cr).WithObjects(cr).Build()
			rec := record.NewFakeRecorder(4)
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, recorder: rec}
			pcsm := &fakePCSM{
				statusResp: client.Status{State: string(tc.state)},
				statusErr:  tc.statusErr,
				actionErr:  tc.actionErr,
				active:     "sync-pcsm-0",
			}

			require.NoError(t, r.reconcileMode(t.Context(), cr, pcsm, 3),
				"a PCSM-side failure must not fail the reconcile")

			stored := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, stored))
			assert.Equal(t, tc.wantError, stored.Status.Error)
			assert.Equal(t, int32(3), stored.Status.ReadyReplicas)
			assert.Equal(t, tc.wantPaused, pcsm.paused)

			if tc.statusErr != nil {
				assert.Equal(t, psmdbv1.ClusterSyncModeRunning, stored.Status.Mode,
					"mode must not mirror spec while PCSM state is unknown")
			}
		})
	}
}
