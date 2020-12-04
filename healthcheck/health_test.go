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
	"testing"

	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/timvaillancourt/go-mongodb-replset/status"
)

var (
	testMember = &status.Member{
		Id:       0,
		Name:     "localhost:27017",
		Health:   status.MemberHealthUp,
		State:    status.MemberStateRecovering,
		StateStr: "RECOVERING",
		Self:     true,
	}
	testStatus = &status.Status{
		Set:     "test",
		MyState: status.MemberStatePrimary,
		Ok:      1,
		Members: []*status.Member{
			testMember,
		},
	}
)

func TestHealthcheckGetSelfMemberState(t *testing.T) {
	state := getSelfMemberState(testStatus)
	assert.Equalf(t, *state, testMember.State, "healthcheck.getSelfMemberState() returned wrong result")

	testStatus.Members[0].Health = status.MemberHealthDown
	state = getSelfMemberState(testStatus)
	assert.Nil(t, state, "healthcheck.getSelfMemberState() should return nil")
	testStatus.Members[0].Health = status.MemberHealthUp
}

func TestHealthcheckIsMemberStateOk(t *testing.T) {
	state := getSelfMemberState(testStatus)
	assert.Truef(t, isStateOk(state, OkMemberStates), "healthcheck.isStateOk(\"%s\") returned false", *state)

	testStatusFail := testStatus
	testStatusFail.Members[0].State = status.MemberStateRemoved
	stateFail := getSelfMemberState(testStatusFail)
	assert.Falsef(t, isStateOk(stateFail, OkMemberStates), "healthcheck.isStateOk(\"%s\") returned true", *stateFail)
}

func TestHealthcheckHealthCheck(t *testing.T) {
	testutils.DoSkipTest(t)

	state, memberState, err := HealthCheck(testDBSession, OkMemberStates)
	assert.NoError(t, err, "healthcheck.HealthCheck() returned an error")
	assert.Equal(t, state, StateOk, "healthcheck.HealthCheck() returned non-ok state")
	assert.Equal(t, *memberState, status.MemberStatePrimary, "healthcheck.HealthCheck() returned non-primary member state")

	state, _, err = HealthCheck(testDBSession, []status.MemberState{status.MemberStateRemoved})
	assert.EqualError(t, err,
		"member has unhealthy replication state: "+status.MemberStatePrimary.String(),
		"healthcheck.HealthCheck() returned an expected error",
	)
	assert.NotEqual(t, state, StateOk, "healthcheck.HealthCheck() returned an unexpected ok state")
}
