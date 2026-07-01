package perconaservermongodbclustersync

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/clustersync/client"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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

			applyObservedStatus(s, tc.observed)

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
}

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

func TestReconcileModeTargetAlreadyFinalized(t *testing.T) {
	tests := map[string]struct {
		spec        psmdbv1.PerconaServerMongoDBClusterSyncSpec
		status      psmdbv1.PerconaServerMongoDBClusterSyncStatus
		observed    client.Status
		wantErrSubs string
		wantEvent   bool
	}{
		"new CR inherits finalized from a previous sync on this target: surfaces conflict, no action": {
			spec: psmdbv1.PerconaServerMongoDBClusterSyncSpec{
				ClusterName: "tgt",
				Mode:        psmdbv1.ClusterSyncModeRunning,
			},
			observed:    client.Status{State: "finalized", LagTimeSeconds: 297},
			wantErrSubs: "inherited from a previous ClusterSync",
			wantEvent:   true,
		},
		"intentional finalize from running: no inherited-state error, normal flow": {
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
				ObjectMeta: metav1.ObjectMeta{Name: "sync3", Namespace: "ns", UID: "u"},
				Spec:       tc.spec,
				Status:     tc.status,
			}
			cl := fake.NewClientBuilder().
				WithScheme(clusterSyncScheme(t)).
				WithStatusSubresource(cr).
				WithObjects(cr).
				Build()
			rec := record.NewFakeRecorder(4)
			r := &ReconcilePerconaServerMongoDBClusterSync{client: cl, recorder: rec}

			f := &fakePCSM{statusResp: tc.observed}

			err := r.reconcileMode(context.Background(), cr, f)
			require.NoError(t, err)

			got := &psmdbv1.PerconaServerMongoDBClusterSync{}
			require.NoError(t, cl.Get(context.Background(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, got))

			if tc.wantErrSubs != "" {
				assert.Contains(t, got.Status.Error, tc.wantErrSubs)
				assert.Nil(t, f.started, "should not have issued /start while target already finalized")
				assert.Zero(t, f.finalized, "should not have issued /finalize either")
			} else {
				assert.NotContains(t, got.Status.Error, "inherited from a previous ClusterSync")
			}

			select {
			case ev := <-rec.Events:
				assert.True(t, tc.wantEvent, "did not expect a recorded event, got %q", ev)
			default:
				assert.False(t, tc.wantEvent, "expected a recorded event")
			}
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
	creds := psmdb.Credentials{Username: "clustersync-cr", Password: "new-password"}

	tests := map[string]struct {
		mc      *recordingMongoClient
		wantErr string
		assert  func(t *testing.T, m *recordingMongoClient)
	}{
		"missing user is created with secret password": {
			mc: &recordingMongoClient{getUserInfoResp: nil},
			assert: func(t *testing.T, m *recordingMongoClient) {
				assert.Equal(t, 1, m.createUserCalls)
				assert.Equal(t, "new-password", m.lastCreatePass)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"existing user has password and roles re-applied": {
			mc: &recordingMongoClient{getUserInfoResp: &mongo.User{DB: "admin"}},
			assert: func(t *testing.T, m *recordingMongoClient) {
				assert.Zero(t, m.createUserCalls)
				assert.Equal(t, 1, m.updateUserPassCalls)
				assert.Equal(t, "new-password", m.lastUpdatePass)
				assert.Equal(t, 1, m.updateRolesCalls)
				assert.Equal(t, syncTargetUserRoles, m.lastUpdateRoles)
			},
		},
		"GetUserInfo error short-circuits with no writes": {
			mc:      &recordingMongoClient{getUserInfoErr: errors.New("error")},
			wantErr: "look up target user",
			assert: func(t *testing.T, m *recordingMongoClient) {
				assert.Zero(t, m.createUserCalls)
				assert.Zero(t, m.updateUserPassCalls)
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"CreateUser error is wrapped": {
			mc:      &recordingMongoClient{getUserInfoResp: nil, createUserErr: errors.New("error")},
			wantErr: "create target user",
		},
		"UpdateUserPass error stops before UpdateUserRoles": {
			mc: &recordingMongoClient{
				getUserInfoResp:   &mongo.User{DB: "admin"},
				updateUserPassErr: errors.New("error"),
			},
			wantErr: "sync target user password",
			assert: func(t *testing.T, m *recordingMongoClient) {
				assert.Zero(t, m.updateRolesCalls)
			},
		},
		"UpdateUserRoles error is wrapped": {
			mc: &recordingMongoClient{
				getUserInfoResp: &mongo.User{DB: "admin"},
				updateRolesErr:  errors.New("error"),
			},
			wantErr: "sync target user roles",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := ensureTargetMongoUser(t.Context(), tc.mc, creds)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			if tc.assert != nil {
				tc.assert(t, tc.mc)
			}
		})
	}
}
