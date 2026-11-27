package perconaservermongodbclustersync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestNextAction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		statusMode api.ClusterSyncMode
		specMode   api.ClusterSyncMode
		state      api.ClusterSyncState
		hasStarted bool
		syncFailed bool
		wantAction modeAction
		wantMirror bool
	}{
		"empty to paused (explicit override) mirrors only": {
			statusMode: "",
			specMode:   api.ClusterSyncModePaused,
			wantAction: actionNone,
			wantMirror: true,
		},
		"empty to running issues /start (default on first create)": {
			statusMode: "",
			specMode:   api.ClusterSyncModeRunning,
			wantAction: actionStart,
			wantMirror: true,
		},
		"empty to finalized is rejected": {
			statusMode: "",
			specMode:   api.ClusterSyncModeFinalized,
			wantAction: actionNone,
			wantMirror: false,
		},

		"paused == paused noop": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModePaused,
			wantAction: actionNone,
			wantMirror: false,
		},
		"running == running noop": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModeRunning,
			wantAction: actionNone,
			wantMirror: false,
		},

		"paused to running on first start": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModeRunning,
			hasStarted: false,
			wantAction: actionStart,
			wantMirror: true,
		},
		"paused to running after prior run resumes": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModeRunning,
			hasStarted: true,
			wantAction: actionResume,
			wantMirror: true,
		},

		"running to paused issues /pause": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModePaused,
			wantAction: actionPause,
			wantMirror: true,
		},

		"running to finalized issues /finalize": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModeFinalized,
			wantAction: actionFinalize,
			wantMirror: true,
		},
		"paused to finalized issues /finalize when previously started": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModeFinalized,
			hasStarted: true,
			wantAction: actionFinalize,
			wantMirror: true,
		},
		"paused to finalized rejected when never started": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModeFinalized,
			hasStarted: false,
			wantAction: actionNone,
			wantMirror: false,
		},
		"finalized to running rejected": {
			statusMode: api.ClusterSyncModeFinalized,
			specMode:   api.ClusterSyncModeRunning,
			wantAction: actionNone,
			wantMirror: false,
		},
		"finalized to paused rejected": {
			statusMode: api.ClusterSyncModeFinalized,
			specMode:   api.ClusterSyncModePaused,
			wantAction: actionNone,
			wantMirror: false,
		},

		"failed with spec=running resumes from failure": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModeRunning,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			wantAction: actionResume,
			wantMirror: false,
		},
		"failed without prior start stays put": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModeRunning,
			state:      api.ClusterSyncStateFailed,
			hasStarted: false,
			wantAction: actionNone,
			wantMirror: false,
		},
		"failed without startedAt and spec=paused mirrors without calling pause": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModePaused,
			state:      api.ClusterSyncStateFailed,
			hasStarted: false,
			wantAction: actionNone,
			wantMirror: true,
		},
		"failed without startedAt and spec=finalized neither finalizes nor mirrors": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModeFinalized,
			state:      api.ClusterSyncStateFailed,
			hasStarted: false,
			wantAction: actionNone,
			wantMirror: false,
		},
		"failed with spec=paused mirrors without calling pause": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModePaused,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			wantAction: actionNone,
			wantMirror: true,
		},
		"failed and already paused stays put": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModePaused,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			wantAction: actionNone,
			wantMirror: false,
		},
		"failed with spec=finalized from running neither finalizes nor mirrors": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModeFinalized,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			wantAction: actionNone,
			wantMirror: false,
		},
		"failed with spec=finalized from paused neither finalizes nor mirrors": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModeFinalized,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			wantAction: actionNone,
			wantMirror: false,
		},
		"failed initial sync with spec=running does not resume": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModeRunning,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			syncFailed: true,
			wantAction: actionNone,
			wantMirror: false,
		},
		"failed initial sync from paused to running does not resume": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModeRunning,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			syncFailed: true,
			wantAction: actionNone,
			wantMirror: false,
		},
		"failed initial sync with spec=paused still mirrors": {
			statusMode: api.ClusterSyncModeRunning,
			specMode:   api.ClusterSyncModePaused,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			syncFailed: true,
			wantAction: actionNone,
			wantMirror: true,
		},
		"failed replication from paused to running resumes from failure": {
			statusMode: api.ClusterSyncModePaused,
			specMode:   api.ClusterSyncModeRunning,
			state:      api.ClusterSyncStateFailed,
			hasStarted: true,
			wantAction: actionResume,
			wantMirror: false,
		},
		"recreated CR on a failed checkpoint starts a new run": {
			statusMode: "",
			specMode:   api.ClusterSyncModeRunning,
			state:      api.ClusterSyncStateFailed,
			hasStarted: false,
			wantAction: actionStart,
			wantMirror: true,
		},
		"recreated CR on a failed checkpoint with spec=paused mirrors only": {
			statusMode: "",
			specMode:   api.ClusterSyncModePaused,
			state:      api.ClusterSyncStateFailed,
			hasStarted: false,
			wantAction: actionNone,
			wantMirror: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			gotAction, gotMirror := nextAction(tc.statusMode, tc.specMode, tc.state, tc.hasStarted, tc.syncFailed)
			assert.Equal(t, tc.wantAction, gotAction, "action")
			assert.Equal(t, tc.wantMirror, gotMirror, "mirror")
		})
	}
}

func TestSkipAction(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		action modeAction
		state  api.ClusterSyncState
		want   bool
	}{
		"none is never skipped":           {action: actionNone, state: api.ClusterSyncStateRunning, want: false},
		"start skipped on running":        {action: actionStart, state: api.ClusterSyncStateRunning, want: true},
		"start not skipped on paused":     {action: actionStart, state: api.ClusterSyncStatePaused, want: false},
		"start not skipped on idle":       {action: actionStart, state: api.ClusterSyncStateIdle, want: false},
		"resume skipped on running":       {action: actionResume, state: api.ClusterSyncStateRunning, want: true},
		"resume not skipped on paused":    {action: actionResume, state: api.ClusterSyncStatePaused, want: false},
		"pause skipped on paused":         {action: actionPause, state: api.ClusterSyncStatePaused, want: true},
		"pause not skipped on running":    {action: actionPause, state: api.ClusterSyncStateRunning, want: false},
		"finalize skipped on finalizing":  {action: actionFinalize, state: api.ClusterSyncStateFinalizing, want: true},
		"finalize skipped on finalized":   {action: actionFinalize, state: api.ClusterSyncStateFinalized, want: true},
		"finalize not skipped on running": {action: actionFinalize, state: api.ClusterSyncStateRunning, want: false},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, skipAction(tc.action, tc.state))
		})
	}
}

func TestInitialSyncFailed(t *testing.T) {
	t.Parallel()

	started := &metav1.Time{}
	notDone := &api.ClusterSyncInitialSync{ClonedSizeBytes: 47992004}
	done := &api.ClusterSyncInitialSync{Completed: true}

	tests := map[string]struct {
		status api.PerconaServerMongoDBClusterSyncStatus
		want   bool
	}{
		"failed during clone after start": {
			status: api.PerconaServerMongoDBClusterSyncStatus{State: api.ClusterSyncStateFailed, StartedAt: started, InitialSync: notDone},
			want:   true,
		},
		"failed after initial sync completed": {
			status: api.PerconaServerMongoDBClusterSyncStatus{State: api.ClusterSyncStateFailed, StartedAt: started, InitialSync: done},
		},
		"failed on pcsm without initialSync": {
			status: api.PerconaServerMongoDBClusterSyncStatus{State: api.ClusterSyncStateFailed, StartedAt: started},
		},
		"failed before this CR started pcsm": {
			status: api.PerconaServerMongoDBClusterSyncStatus{State: api.ClusterSyncStateFailed, InitialSync: notDone},
		},
		"running clone": {
			status: api.PerconaServerMongoDBClusterSyncStatus{State: api.ClusterSyncStateRunning, StartedAt: started, InitialSync: notDone},
		},
		"paused clone": {
			status: api.PerconaServerMongoDBClusterSyncStatus{State: api.ClusterSyncStatePaused, StartedAt: started, InitialSync: notDone},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, initialSyncFailed(&tc.status))
		})
	}
}
