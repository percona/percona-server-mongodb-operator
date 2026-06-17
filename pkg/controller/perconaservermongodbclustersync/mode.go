package perconaservermongodbclustersync

import (
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type modeAction string

const (
	actionNone     modeAction = ""
	actionStart    modeAction = "start"
	actionResume   modeAction = "resume"
	actionPause    modeAction = "pause"
	actionFinalize modeAction = "finalize"
)

// skipAction returns true when the requested action is a no-op for the
// observed PCSM state, so we don't waste a CLI call.
func skipAction(action modeAction, state api.ClusterSyncState) bool {
	switch action {
	case actionStart, actionResume:
		return state == api.ClusterSyncStateRunning
	case actionPause:
		return state == api.ClusterSyncStatePaused
	case actionFinalize:
		return state == api.ClusterSyncStateFinalizing || state == api.ClusterSyncStateFinalized
	}
	return false
}

func nextAction(statusMode, specMode api.ClusterSyncMode, state api.ClusterSyncState, hasStarted bool) (modeAction, bool) {
	if statusMode == api.ClusterSyncModeFinalized {
		return actionNone, false
	}
	if state == api.ClusterSyncStateFailed && specMode == api.ClusterSyncModeRunning && hasStarted {
		return actionResume, false
	}
	if statusMode == specMode {
		return actionNone, false
	}

	from := statusMode
	if from == "" {
		from = api.ClusterSyncModePaused
	}

	switch specMode {
	case api.ClusterSyncModePaused:
		if from == api.ClusterSyncModeRunning {
			return actionPause, true
		}
		return actionNone, true
	case api.ClusterSyncModeRunning:
		if from == api.ClusterSyncModePaused {
			if hasStarted {
				return actionResume, true
			}
			return actionStart, true
		}
	case api.ClusterSyncModeFinalized:
		switch from {
		case api.ClusterSyncModeRunning:
			return actionFinalize, true
		case api.ClusterSyncModePaused:
			if hasStarted {
				return actionFinalize, true
			}
		}
	}
	return actionNone, false
}
