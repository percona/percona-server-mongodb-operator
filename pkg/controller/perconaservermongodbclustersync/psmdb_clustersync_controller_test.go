package perconaservermongodbclustersync

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync/client"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	coordv1 "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	k8sclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestApplyObservedStatus(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	boom := errors.New("pcsm refused")

	tests := map[string]struct {
		action    modeAction
		cr        *psmdbv1.PerconaServerMongoDBClusterSync
		state     psmdbv1.ClusterSyncState
		actionErr error
		assert    func(t *testing.T, f *fakePCSM)
		wantErr   error
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
			action:    actionPause,
			cr:        &psmdbv1.PerconaServerMongoDBClusterSync{},
			actionErr: boom,
			wantErr:   boom,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			f := &fakePCSM{actionErr: tc.actionErr}
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

func TestReconcileMode(t *testing.T) {
	backupHolder := "backup-uid"
	backupLease := &coordv1.Lease{
		Name: naming.BackupLeaseName("tgt"), Namespace: "ns",
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: &backupHolder,
		},
	}
	running := func(size int32) psmdbv1.PerconaServerMongoDBClusterSyncSpec {
		return psmdbv1.PerconaServerMongoDBClusterSyncSpec{ClusterName: "tgt", Mode: psmdbv1.ClusterSyncModeRunning, Size: size}
	}
	paused := psmdbv1.PerconaServerMongoDBClusterSyncSpec{ClusterName: "tgt", Mode: psmdbv1.ClusterSyncModePaused, Size: 3}
	startedStatus := func(state psmdbv1.ClusterSyncState, prevError string) psmdbv1.PerconaServerMongoDBClusterSyncStatus {
		return psmdbv1.PerconaServerMongoDBClusterSyncStatus{
			Mode:      psmdbv1.ClusterSyncModeRunning,
			State:     state,
			StartedAt: &metav1.Time{Time: time.Unix(0, 0)},
			Error:     prevError,
		}
	}

	tests := map[string]struct {
		spec        psmdbv1.PerconaServerMongoDBClusterSyncSpec
		status      psmdbv1.PerconaServerMongoDBClusterSyncStatus
		pcsm        fakePCSM
		ready       int32
		objects     []k8sclient.Object
		passes      int
		wantError   string
		wantActive  string
		wantMode    psmdbv1.ClusterSyncMode
		wantEvents  []string
		wantStarted bool
		wantPaused  int
	}{
		"new CR against target with inherited finalized state issues /start": {
			spec:        running(0),
			pcsm:        fakePCSM{statusResp: client.Status{State: "finalized", LagTimeSeconds: 297}},
			ready:       1,
			wantStarted: true,
		},
		"intentional finalize from running: normal flow, no /start": {
			spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{ClusterName: "tgt", Mode: psmdbv1.ClusterSyncModeFinalized},
			status: psmdbv1.PerconaServerMongoDBClusterSyncStatus{
				Mode:      psmdbv1.ClusterSyncModeRunning,
				StartedAt: &metav1.Time{Time: time.Now().Add(-time.Hour)},
			},
			pcsm:  fakePCSM{statusResp: client.Status{State: "finalized"}},
			ready: 1,
		},
		"first observation records the active instance without an event": {
			spec:       running(3),
			status:     psmdbv1.PerconaServerMongoDBClusterSyncStatus{Mode: psmdbv1.ClusterSyncModeRunning},
			pcsm:       fakePCSM{statusResp: client.Status{State: "running"}, active: "sync-pcsm-0"},
			ready:      3,
			wantActive: "sync-pcsm-0",
		},
		"unchanged active instance emits no event": {
			spec:       running(3),
			status:     psmdbv1.PerconaServerMongoDBClusterSyncStatus{Mode: psmdbv1.ClusterSyncModeRunning, ActiveInstance: "sync-pcsm-0"},
			pcsm:       fakePCSM{statusResp: client.Status{State: "running"}, active: "sync-pcsm-0"},
			ready:      3,
			wantActive: "sync-pcsm-0",
		},
		"handover to another instance is reported as a failover": {
			spec:       running(3),
			status:     psmdbv1.PerconaServerMongoDBClusterSyncStatus{Mode: psmdbv1.ClusterSyncModeRunning, ActiveInstance: "sync-pcsm-0"},
			pcsm:       fakePCSM{statusResp: client.Status{State: "running"}, active: "sync-pcsm-2"},
			ready:      3,
			wantActive: "sync-pcsm-2",
			wantEvents: []string{"PCSMFailover"},
		},
		"degraded group keeps the last known active instance": {
			spec:       running(3),
			status:     psmdbv1.PerconaServerMongoDBClusterSyncStatus{Mode: psmdbv1.ClusterSyncModeRunning, ActiveInstance: "sync-pcsm-1"},
			pcsm:       fakePCSM{statusResp: client.Status{State: "running"}},
			ready:      1,
			wantActive: "sync-pcsm-1",
		},
		"busy cluster is reported once across reconciles": {
			spec:       running(0),
			pcsm:       fakePCSM{statusResp: client.Status{State: "idle"}},
			ready:      1,
			objects:    []k8sclient.Object{backupLease},
			passes:     3,
			wantError:  `backup is in progress on cluster "tgt" (lease ` + naming.BackupLeaseName("tgt") + `)`,
			wantEvents: []string{"ClusterBusy"},
		},
		"mid-election group reports no active instance": {
			spec:       running(3),
			status:     startedStatus("", ""),
			pcsm:       fakePCSM{statusErr: client.ErrNoActivePCSM, active: "sync-pcsm-0"},
			ready:      3,
			wantError:  client.ErrNoActivePCSM.Error(),
			wantActive: "sync-pcsm-0",
			wantMode:   psmdbv1.ClusterSyncModeRunning,
		},
		"no ready pod yet": {
			spec:       running(3),
			status:     startedStatus("", ""),
			pcsm:       fakePCSM{statusErr: client.ErrPCSMNotReady, active: "sync-pcsm-0"},
			ready:      3,
			wantError:  client.ErrPCSMNotReady.Error(),
			wantActive: "sync-pcsm-0",
			wantMode:   psmdbv1.ClusterSyncModeRunning,
		},
		"an unexpected status failure is recorded, not returned": {
			spec:       running(3),
			status:     startedStatus("", ""),
			pcsm:       fakePCSM{statusErr: errors.New("exec failed"), active: "sync-pcsm-0"},
			ready:      3,
			wantError:  "exec failed",
			wantActive: "sync-pcsm-0",
			wantMode:   psmdbv1.ClusterSyncModeRunning,
		},
		"a repeated status failure stays in status": {
			spec:       running(3),
			status:     startedStatus("", "exec failed"),
			pcsm:       fakePCSM{statusErr: errors.New("exec failed"), active: "sync-pcsm-0"},
			ready:      3,
			wantError:  "exec failed",
			wantActive: "sync-pcsm-0",
			wantMode:   psmdbv1.ClusterSyncModeRunning,
		},
		"a failed transition is recorded, not returned": {
			spec:   paused,
			status: startedStatus(psmdbv1.ClusterSyncStateRunning, ""),
			pcsm: fakePCSM{
				statusResp: client.Status{State: string(psmdbv1.ClusterSyncStateRunning)},
				actionErr:  errors.New("pcsm pause refused"),
				active:     "sync-pcsm-0",
			},
			ready:      3,
			wantError:  "pcsm pause refused",
			wantActive: "sync-pcsm-0",
			wantPaused: 1,
		},
		"a repeated failed transition stays in status": {
			spec:   paused,
			status: startedStatus(psmdbv1.ClusterSyncStateRunning, "pcsm pause refused"),
			pcsm: fakePCSM{
				statusResp: client.Status{State: string(psmdbv1.ClusterSyncStateRunning)},
				actionErr:  errors.New("pcsm pause refused"),
				active:     "sync-pcsm-0",
			},
			ready:      3,
			wantError:  "pcsm pause refused",
			wantActive: "sync-pcsm-0",
			wantPaused: 1,
		},
		"a transition against an unreachable instance is not an error": {
			spec:   paused,
			status: startedStatus(psmdbv1.ClusterSyncStateRunning, ""),
			pcsm: fakePCSM{
				statusResp: client.Status{State: string(psmdbv1.ClusterSyncStateRunning)},
				actionErr:  client.ErrNoActivePCSM,
				active:     "sync-pcsm-0",
			},
			ready:      3,
			wantError:  client.ErrNoActivePCSM.Error(),
			wantActive: "sync-pcsm-0",
			wantPaused: 1,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				Name: "sync", Namespace: "ns", UID: "u",
				Spec:   tc.spec,
				Status: tc.status,
			}
			cl := fake.NewClientBuilder().
				WithScheme(clusterSyncScheme(t)).
				WithStatusSubresource(cr).
				WithObjects(append([]k8sclient.Object{cr}, tc.objects...)...).
				Build()
			rec := events.NewFakeRecorder(16)
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, recorder: rec}
			pcsm := tc.pcsm
			nn := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

			for range max(tc.passes, 1) {
				current := &psmdbv1.PerconaServerMongoDBClusterSync{}
				require.NoError(t, cl.Get(t.Context(), nn, current))
				require.NoError(t, r.reconcileMode(t.Context(), current, &pcsm, tc.ready),
					"a PCSM-side failure must not fail the reconcile")
			}

			stored := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(t.Context(), nn, stored))
			assert.Equal(t, tc.wantError, stored.Status.Error)
			assert.Equal(t, tc.wantActive, stored.Status.ActiveInstance)
			assert.Equal(t, tc.ready, stored.Status.ReadyReplicas)
			assert.Equal(t, clustersync.Size(cr), stored.Status.Size)
			assert.Equal(t, tc.wantStarted, pcsm.started != nil)
			assert.Equal(t, tc.wantPaused, pcsm.paused)
			if tc.wantMode != "" {
				assert.Equal(t, tc.wantMode, stored.Status.Mode, "mode must not mirror spec while PCSM state is unknown")
			}

			require.Len(t, rec.Events, len(tc.wantEvents), "events are reported once per change, not once per reconcile")
			for _, want := range tc.wantEvents {
				assert.Contains(t, <-rec.Events, want)
			}
		})
	}
}

func TestReadyReplicas(t *testing.T) {
	cr := &psmdbv1.PerconaServerMongoDBClusterSync{Name: "sync", Namespace: "ns"}

	boom := errors.New("apiserver down")

	tests := map[string]struct {
		deployment *appsv1.Deployment
		getErr     error
		want       int32
		wantErr    error
	}{
		"missing deployment is not ready": {
			want: 0,
		},
		"a non-NotFound read failure is returned": {
			getErr:  boom,
			wantErr: boom,
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
			if tc.getErr != nil {
				b = b.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(context.Context, k8sclient.WithWatch, k8sclient.ObjectKey, k8sclient.Object, ...k8sclient.GetOption) error {
						return tc.getErr
					},
				})
			}
			r := &ReconcilePerconaServerMongoDBClusterSync{client: b.Build()}

			got, err := r.readyReplicas(t.Context(), cr)
			assert.Equal(t, tc.want, got)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
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

func TestReconcilePDB(t *testing.T) {
	boom := errors.New("etcd unavailable")

	tests := map[string]struct {
		size        int32
		existing    bool
		foreign     bool
		createErr   error
		wantPDB     bool
		wantDeletes int
		wantErr     error
	}{
		"HA group creates the budget":                    {size: 3, wantPDB: true},
		"HA group is idempotent over an existing budget": {size: 3, existing: true, wantPDB: true},
		"single instance creates no budget":              {size: 1},
		"scale down to one deletes the existing budget":  {size: 1, existing: true, wantDeletes: 1},
		"a budget this CR does not own is left alone":    {size: 3, existing: true, foreign: true, wantPDB: true},
		"scale down does not delete a foreign budget":    {size: 1, existing: true, foreign: true, wantPDB: true},
		"apply failure is returned":                      {size: 3, createErr: boom, wantErr: boom},
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
					Create: func(ctx context.Context, c k8sclient.WithWatch, obj k8sclient.Object, opts ...k8sclient.CreateOption) error {
						if tc.createErr != nil {
							return tc.createErr
						}
						return c.Create(ctx, obj, opts...)
					},
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

			err := r.reconcilePDB(t.Context(), cr)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.ErrorContains(t, err, "apply clustersync pdb")
				return
			}
			require.NoError(t, err)
			require.NoError(t, r.reconcilePDB(t.Context(), cr), "reconcile must be idempotent")
			assert.Equal(t, tc.wantDeletes, deletes,
				"reconcile runs every few seconds; it must not issue a write call when there is nothing to remove")

			pdb := &policyv1.PodDisruptionBudget{}
			err = cl.Get(t.Context(), types.NamespacedName{
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

func TestWriteStatus(t *testing.T) {
	steady := psmdbv1.PerconaServerMongoDBClusterSyncStatus{
		State:         psmdbv1.ClusterSyncStateRunning,
		ReadyReplicas: 3,
	}

	tests := map[string]struct {
		stored    psmdbv1.PerconaServerMongoDBClusterSyncStatus
		deleted   bool
		write     psmdbv1.PerconaServerMongoDBClusterSyncStatus
		wantWrite bool
	}{
		"a changed status is persisted": {
			stored:    psmdbv1.PerconaServerMongoDBClusterSyncStatus{State: psmdbv1.ClusterSyncStateRunning},
			write:     steady,
			wantWrite: true,
		},
		"an identical status is not written again": {
			stored: steady,
			write:  steady,
		},
		"a CR deleted mid-reconcile is not an error": {
			deleted: true,
			write:   psmdbv1.PerconaServerMongoDBClusterSyncStatus{State: psmdbv1.ClusterSyncStateFinalized},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{Name: "sync", Namespace: "ns", Status: tc.stored}
			b := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithStatusSubresource(cr)
			if !tc.deleted {
				b = b.WithObjects(cr)
			}
			cl := b.Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}
			nn := types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}

			before := &psmdbv1.PerconaServerMongoDBClusterSync{}
			if !tc.deleted {
				require.NoError(t, cl.Get(t.Context(), nn, before))
			}

			require.NoError(t, r.writeStatus(t.Context(), cr, tc.write))
			if tc.deleted {
				return
			}

			after := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(t.Context(), nn, after))
			assert.Equal(t, tc.write, after.Status)
			if tc.wantWrite {
				assert.NotEqual(t, before.ResourceVersion, after.ResourceVersion)
				return
			}
			assert.Equal(t, before.ResourceVersion, after.ResourceVersion,
				"a steady-state reconcile must not write status, or it wakes itself up every requeue")
		})
	}
}

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

func TestLoadTargetTLS(t *testing.T) {
	target := func(crVersion string, tls *psmdbv1.TLSSpec) *psmdbv1.PerconaServerMongoDB {
		return &psmdbv1.PerconaServerMongoDB{
			Name: "tgt", Namespace: "ns",
			Spec: psmdbv1.PerconaServerMongoDBSpec{
				CRVersion: crVersion,
				TLS:       tls,
				Secrets:   &psmdbv1.SecretsSpec{SSL: "tgt-ssl"},
			},
		}
	}
	sslSecret := func(data map[string][]byte) *corev1.Secret {
		return &corev1.Secret{Name: "tgt-ssl", Namespace: "ns", Data: data}
	}
	complete := func() map[string][]byte {
		return map[string][]byte{
			"ca.crt":  []byte("CA"),
			"tls.crt": []byte("CERT"),
			"tls.key": []byte("KEY"),
		}
	}

	tests := map[string]struct {
		target      *psmdbv1.PerconaServerMongoDB
		secret      *corev1.Secret
		mutate      func(t *testing.T, cl k8sclient.Client, target *psmdbv1.PerconaServerMongoDB, secret *corev1.Secret)
		wantEnabled bool
		wantErrMsg  string
	}{
		"tls disabled target needs no certificates": {
			target: target(version.Version(), &psmdbv1.TLSSpec{Mode: psmdbv1.TLSModeDisabled}),
		},
		"complete ssl secret resolves": {
			target:      target(version.Version(), nil),
			secret:      sslSecret(complete()),
			wantEnabled: true,
		},
		"missing ca is reported": {
			target:     target(version.Version(), nil),
			secret:     sslSecret(map[string][]byte{"tls.crt": []byte("CERT"), "tls.key": []byte("KEY")}),
			wantErrMsg: "target ssl secret ns/tgt-ssl is missing ca.crt",
		},
		"missing key is reported": {
			target:     target(version.Version(), nil),
			secret:     sslSecret(map[string][]byte{"ca.crt": []byte("CA"), "tls.crt": []byte("CERT")}),
			wantErrMsg: "target ssl secret ns/tgt-ssl is missing tls.key",
		},
		"missing secret is reported": {
			target:     target(version.Version(), nil),
			wantErrMsg: `get target ssl secret ns/tgt-ssl: secrets "tgt-ssl" not found`,
		},
		"target pinned to an older crVersion keeps the 1.23 pod template": {
			target: target("1.23.0", nil),
			secret: sslSecret(complete()),
		},
		"rotated certificate changes the hash": {
			target: target(version.Version(), nil),
			secret: sslSecret(complete()),
			mutate: func(t *testing.T, cl k8sclient.Client, _ *psmdbv1.PerconaServerMongoDB, secret *corev1.Secret) {
				secret.Data["tls.crt"] = []byte("ROTATED")
				require.NoError(t, cl.Update(t.Context(), secret))
			},
			wantEnabled: true,
		},
		"certificate verification toggle changes the hash": {
			target: target(version.Version(), &psmdbv1.TLSSpec{Mode: psmdbv1.TLSModeRequire, AllowInvalidCertificates: new(true)}),
			secret: sslSecret(complete()),
			mutate: func(_ *testing.T, _ k8sclient.Client, target *psmdbv1.PerconaServerMongoDB, _ *corev1.Secret) {
				target.Spec.TLS.AllowInvalidCertificates = new(false)
			},
			wantEnabled: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := clusterSyncScheme(t)
			require.NoError(t, corev1.AddToScheme(s))
			b := fake.NewClientBuilder().WithScheme(s)
			if tc.secret != nil {
				b = b.WithObjects(tc.secret)
			}
			cl := b.Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, scheme: s}

			got, err := r.loadTargetTLS(t.Context(), tc.target)
			if tc.wantErrMsg != "" {
				require.EqualError(t, err, tc.wantErrMsg)
				assert.Zero(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantEnabled, got.Enabled())
			if tc.wantEnabled {
				assert.Equal(t, "tgt-ssl", got.SecretName)
				assert.NotEmpty(t, got.Hash)
			}
			if tc.mutate == nil {
				return
			}

			tc.mutate(t, cl, tc.target, tc.secret)
			after, err := r.loadTargetTLS(t.Context(), tc.target)
			require.NoError(t, err)
			assert.NotEqual(t, got.Hash, after.Hash, "the pod template must roll when the TLS inputs change")
		})
	}
}

func targetCluster(crVersion string) *psmdbv1.PerconaServerMongoDB {
	return &psmdbv1.PerconaServerMongoDB{
		Name: "target", Namespace: "ns",
		Spec: psmdbv1.PerconaServerMongoDBSpec{CRVersion: crVersion},
	}
}

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

	require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0"), clustersync.TLS{}))
	initial := get(t)

	require.NotNil(t, initial.Spec.Replicas)
	assert.Equal(t, int32(1), *initial.Spec.Replicas)
	assert.Equal(t, appsv1.RecreateDeploymentStrategyType, initial.Spec.Strategy.Type)
	require.Len(t, initial.OwnerReferences, 1, "deployment must be garbage-collected with the CR")
	assert.Equal(t, cr.Name, initial.OwnerReferences[0].Name)
	assert.Equal(t, clustersync.Labels(cr), initial.Labels)

	t.Run("reconcile is idempotent", func(t *testing.T) {
		require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0"), clustersync.TLS{}))
		assert.Equal(t, initial.Spec, get(t).Spec)
	})

	for _, size := range []int32{3, 5, 1} {
		t.Run(fmt.Sprintf("scaling to %d changes only replicas", size), func(t *testing.T) {
			cr.Spec.Size = size
			require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0"), clustersync.TLS{}))
			dep := get(t)

			require.NotNil(t, dep.Spec.Replicas)
			assert.Equal(t, size, *dep.Spec.Replicas)
			assert.Equal(t, initial.Spec.Template, dep.Spec.Template,
				"a template change rolls the whole group and forces a PCSM failover")
		})
	}

	t.Run("HA group rolls one at a time without surging", func(t *testing.T) {
		cr.Spec.Size = 3
		require.NoError(t, r.reconcileDeployment(t.Context(), cr, targetCluster("1.24.0"), clustersync.TLS{}))
		strategy := get(t).Spec.Strategy

		require.Equal(t, appsv1.RollingUpdateDeploymentStrategyType, strategy.Type)
		require.NotNil(t, strategy.RollingUpdate)
		assert.Equal(t, intstr.FromInt32(1), *strategy.RollingUpdate.MaxUnavailable)
		assert.Equal(t, intstr.FromInt32(0), *strategy.RollingUpdate.MaxSurge,
			"surging past spec.size would add an instance the PCSM group never agreed to")
	})
}

func TestReconcileDeployment(t *testing.T) {
	boom := errors.New("etcd unavailable")

	tests := map[string]struct {
		crVersion   string
		createErr   error
		wantDefault bool
		wantErr     error
	}{
		"older crVersion keeps the 1.23 pod template": {crVersion: "1.23.0"},
		"current crVersion gets the defaults":         {crVersion: "1.24.0", wantDefault: true},
		"apply failure is returned":                   {crVersion: "1.24.0", createErr: boom, wantErr: boom},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDBClusterSync{
				Name: "sync", Namespace: "ns", UID: "u",
				Spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{Size: 3, Image: "pcsm:1.0.0"},
			}
			scheme := clusterSyncScheme(t)
			b := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr)
			if tc.createErr != nil {
				b = b.WithInterceptorFuncs(interceptor.Funcs{
					Create: func(context.Context, k8sclient.WithWatch, k8sclient.Object, ...k8sclient.CreateOption) error {
						return tc.createErr
					},
				})
			}
			cl := b.Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, scheme: scheme}

			err := r.reconcileDeployment(t.Context(), cr, targetCluster(tc.crVersion), clustersync.TLS{})
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.ErrorContains(t, err, "apply clustersync deployment")
				return
			}
			require.NoError(t, err)

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

func clusterSyncScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	require.NoError(t, appsv1.AddToScheme(s))
	require.NoError(t, coordv1.AddToScheme(s))
	require.NoError(t, policyv1.AddToScheme(s))
	require.NoError(t, psmdbv1.SchemeBuilder.AddToScheme(s))
	return s
}

func newClusterSyncCR(name, ns, cluster string, finalizers ...string) *psmdbv1.PerconaServerMongoDBClusterSync {
	return &psmdbv1.PerconaServerMongoDBClusterSync{
		Name:       name,
		Namespace:  ns,
		UID:        types.UID(name + "-uid"),
		Finalizers: finalizers,
		Spec:       psmdbv1.PerconaServerMongoDBClusterSyncSpec{ClusterName: cluster},
	}
}

func leaseFor(cr *psmdbv1.PerconaServerMongoDBClusterSync, holder string) *coordv1.Lease {
	return &coordv1.Lease{
		Name:      naming.ClusterSyncLeaseName(cr.Spec.ClusterName),
		Namespace: cr.Namespace,
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: &holder,
		},
	}
}

func TestReleaseClusterSyncLease(t *testing.T) {
	t.Parallel()

	ours := naming.ClusterSyncHolderId(newClusterSyncCR("sync", "ns", "tgt"))

	tests := map[string]struct {
		holder    string
		wantLease bool
	}{
		"lease held by us: deleted":                             {holder: ours},
		"lease not found: no error, nothing to delete":          {},
		"lease held by another CR: swallowed (we don't own it)": {holder: "different-holder", wantLease: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newClusterSyncCR("sync", "ns", "tgt")
			b := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t))
			if tc.holder != "" {
				b = b.WithRuntimeObjects(leaseFor(cr, tc.holder))
			}
			cl := b.Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

			require.NoError(t, r.releaseClusterSyncLease(t.Context(), cr))

			err := cl.Get(t.Context(), types.NamespacedName{
				Name:      naming.ClusterSyncLeaseName(cr.Spec.ClusterName),
				Namespace: cr.Namespace,
			}, &coordv1.Lease{})
			if tc.wantLease {
				require.NoError(t, err, "foreign lease must not be touched")
				return
			}
			require.True(t, k8serrors.IsNotFound(err), "lease must be gone, got %v", err)
		})
	}
}

func TestAcquireClusterSyncLease(t *testing.T) {
	t.Parallel()

	ours := naming.ClusterSyncHolderId(newClusterSyncCR("sync", "ns", "tgt"))

	tests := map[string]struct {
		holder      string
		wantForeign string
		wantHolder  string
	}{
		"no lease: acquires, no foreign holder": {
			wantHolder: ours,
		},
		"lease held by us: no foreign holder": {
			holder:     ours,
			wantHolder: ours,
		},
		"lease held by another CR: returns foreign holder, does not overwrite": {
			holder:      "other-sync-uid",
			wantForeign: "other-sync-uid",
			wantHolder:  "other-sync-uid",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newClusterSyncCR("sync", "ns", "tgt")
			b := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t))
			if tc.holder != "" {
				b = b.WithRuntimeObjects(leaseFor(cr, tc.holder))
			}
			cl := b.Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

			foreign, err := r.acquireClusterSyncLease(t.Context(), cr)
			require.NoError(t, err)
			assert.Equal(t, tc.wantForeign, foreign)

			got := &coordv1.Lease{}
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{
				Name:      naming.ClusterSyncLeaseName(cr.Spec.ClusterName),
				Namespace: cr.Namespace,
			}, got))
			require.NotNil(t, got.Spec.HolderIdentity)
			assert.Equal(t, tc.wantHolder, *got.Spec.HolderIdentity)
		})
	}
}

func TestEnsureReleaseLockFinalizer(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		existing []string
		want     []string
	}{
		"no finalizers: adds release-lock": {
			want: []string{naming.FinalizerReleaseLock},
		},
		"unrelated finalizer present: appends": {
			existing: []string{"other.example/x"},
			want:     []string{"other.example/x", naming.FinalizerReleaseLock},
		},
		"already present: no patch needed, no error": {
			existing: []string{naming.FinalizerReleaseLock},
			want:     []string{naming.FinalizerReleaseLock},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newClusterSyncCR("sync", "ns", "tgt", tc.existing...)
			cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithObjects(cr).Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

			require.NoError(t, r.ensureReleaseLockFinalizer(t.Context(), cr))

			got := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, got))
			assert.Equal(t, tc.want, got.Finalizers)
		})
	}
}

func TestClusterBusyByBackupOrRestore(t *testing.T) {
	t.Parallel()

	const (
		ns          = "ns"
		clusterName = "tgt"
		otherClus   = "otherCluster"
	)
	backupLease := func() *coordv1.Lease {
		h := "backup-uid"
		return &coordv1.Lease{
			Name: naming.BackupLeaseName(clusterName), Namespace: ns,
			Spec: coordv1.LeaseSpec{
				AcquireTime:    &metav1.MicroTime{Time: time.Now()},
				HolderIdentity: &h,
			},
		}
	}
	restoreOn := func(name, cluster string, state psmdbv1.RestoreState) *psmdbv1.PerconaServerMongoDBRestore {
		return &psmdbv1.PerconaServerMongoDBRestore{
			Name: name, Namespace: ns,
			Spec:   psmdbv1.PerconaServerMongoDBRestoreSpec{ClusterName: cluster},
			Status: psmdbv1.PerconaServerMongoDBRestoreStatus{State: state},
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

			busy, reason, err := r.clusterBusyByBackupOrRestore(t.Context(), cr)
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
	t.Parallel()

	tests := map[string]struct {
		action modeAction
		want   bool
	}{
		"start writes":          {action: actionStart, want: true},
		"resume writes":         {action: actionResume, want: true},
		"pause is read-only":    {action: actionPause},
		"finalize is read-only": {action: actionFinalize},
		"none is read-only":     {action: actionNone},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, writeAction(tc.action))
		})
	}
}

func TestHandleDeletion(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		finalizers     []string
		wantLease      bool
		wantFinalizers []string
	}{
		"release-lock finalizer absent: noop, no lease touched": {
			finalizers:     []string{"other.example/keep"},
			wantLease:      true,
			wantFinalizers: []string{"other.example/keep"},
		},
		"finalizer present: releases lease and drops finalizer": {
			finalizers:     []string{naming.FinalizerReleaseLock, "other.example/keep"},
			wantFinalizers: []string{"other.example/keep"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newClusterSyncCR("sync", "ns", "tgt", tc.finalizers...)
			cr.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			lease := leaseFor(cr, naming.ClusterSyncHolderId(cr))
			cl := fake.NewClientBuilder().WithScheme(clusterSyncScheme(t)).WithObjects(cr).WithRuntimeObjects(lease).Build()
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl}

			_, err := r.handleDeletion(t.Context(), cr)
			require.NoError(t, err)

			err = cl.Get(t.Context(), types.NamespacedName{Name: lease.Name, Namespace: lease.Namespace}, &coordv1.Lease{})
			if tc.wantLease {
				require.NoError(t, err, "lease must survive when our finalizer was absent")
			} else {
				require.True(t, k8serrors.IsNotFound(err), "lease must be released, got %v", err)
			}

			gotCR := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(t.Context(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, gotCR))
			assert.Equal(t, tc.wantFinalizers, gotCR.Finalizers)
		})
	}
}
