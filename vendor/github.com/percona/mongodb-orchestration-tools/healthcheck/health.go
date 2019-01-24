// Copyright 2018 Percona LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package healthcheck

import (
	"fmt"

	"github.com/timvaillancourt/go-mongodb-replset/status"
	"gopkg.in/mgo.v2"
)

// OkMemberStates is a slice of acceptable replication member states
var OkMemberStates = []status.MemberState{
	status.MemberStatePrimary,
	status.MemberStateSecondary,
	status.MemberStateArbiter,
	status.MemberStateRecovering,
	status.MemberStateStartup2,
}

// getSelfMemberState returns the replication state of the local MongoDB member
func getSelfMemberState(rsStatus *status.Status) *status.MemberState {
	member := rsStatus.GetSelf()
	if member == nil || member.Health != status.MemberHealthUp {
		return nil
	}
	return &member.State
}

// isStateOk checks if a replication member state matches one of the acceptable member states in 'OkMemberStates'
func isStateOk(memberState *status.MemberState, okMemberStates []status.MemberState) bool {
	for _, state := range okMemberStates {
		if *memberState == state {
			return true
		}
	}
	return false
}

// HealthCheck checks the replication member state of the local MongoDB member
func HealthCheck(session *mgo.Session, okMemberStates []status.MemberState) (State, *status.MemberState, error) {
	rsStatus, err := status.New(session)
	if err != nil {
		return StateFailed, nil, fmt.Errorf("error getting replica set status: %s", err)
	}

	state := getSelfMemberState(rsStatus)
	if state == nil {
		return StateFailed, state, fmt.Errorf("found no member state for self in replica set status")
	}
	if isStateOk(state, okMemberStates) {
		return StateOk, state, nil
	}

	return StateFailed, state, fmt.Errorf("member has unhealthy replication state: %s", state)
}
