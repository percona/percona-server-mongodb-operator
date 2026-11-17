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

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/stretchr/testify/require"
)

func statusWithSelf(state mongo.MemberState, uptime int64, initialSync any) mongo.Status {
	return mongo.Status{
		MyState:           state,
		InitialSyncStatus: initialSync,
		Members: []*mongo.Member{
			{Id: 0, Name: "other:27017", State: state, Uptime: uptime},
			{Id: 1, Name: "self:27017", State: state, Uptime: uptime, Self: true},
		},
	}
}

func TestCheckStateForLiveness(t *testing.T) {
	tests := []struct {
		name      string
		rs        mongo.Status
		oplogSize int64
		wantErr   bool
	}{
		{
			name:    "no self member",
			rs:      mongo.Status{MyState: mongo.MemberStatePrimary},
			wantErr: true,
		},
		{
			name: "primary",
			rs:   statusWithSelf(mongo.MemberStatePrimary, 1000, nil),
		},
		{
			name: "secondary",
			rs:   statusWithSelf(mongo.MemberStateSecondary, 1000, nil),
		},
		{
			name: "arbiter",
			rs:   statusWithSelf(mongo.MemberStateArbiter, 1000, nil),
		},
		{
			name: "startup, no initial sync, uptime within budget",
			rs:   statusWithSelf(mongo.MemberStateStartup, 30, nil),
		},
		{
			name:    "startup, no initial sync, uptime exceeds budget",
			rs:      statusWithSelf(mongo.MemberStateStartup, 31, nil),
			wantErr: true,
		},
		{
			name:      "startup2, no initial sync, uptime within oplog-scaled budget",
			rs:        statusWithSelf(mongo.MemberStateStartup2, 150, nil),
			oplogSize: 2, // budget = 30 + 2*60 = 150
		},
		{
			name:      "startup2, no initial sync, uptime exceeds oplog-scaled budget",
			rs:        statusWithSelf(mongo.MemberStateStartup2, 151, nil),
			oplogSize: 2,
			wantErr:   true,
		},
		{
			name: "startup with initial sync ignores uptime",
			rs:   statusWithSelf(mongo.MemberStateStartup, 99999, map[string]any{"syncSourceHost": "src:27017"}),
		},
		{
			name: "startup2 with initial sync ignores uptime",
			rs:   statusWithSelf(mongo.MemberStateStartup2, 99999, map[string]any{"syncSourceHost": "src:27017"}),
		},
		{
			name: "recovering does not error",
			rs:   statusWithSelf(mongo.MemberStateRecovering, 99999, nil),
		},
		{
			name: "rollback does not error",
			rs:   statusWithSelf(mongo.MemberStateRollback, 99999, nil),
		},
		{
			name:    "unknown state",
			rs:      statusWithSelf(mongo.MemberStateUnknown, 1, nil),
			wantErr: true,
		},
		{
			name:    "down state",
			rs:      statusWithSelf(mongo.MemberStateDown, 1, nil),
			wantErr: true,
		},
		{
			name:    "removed state",
			rs:      statusWithSelf(mongo.MemberStateRemoved, 1, nil),
			wantErr: true,
		},
		{
			name:    "out-of-range state",
			rs:      statusWithSelf(mongo.MemberState(99), 1, nil),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckStateForLiveness(t.Context(), tt.rs, tt.oplogSize)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestCheckStateForReadiness(t *testing.T) {
	tests := []struct {
		name    string
		rs      mongo.Status
		wantErr bool
	}{
		{
			name:    "no self member",
			rs:      mongo.Status{MyState: mongo.MemberStatePrimary},
			wantErr: true,
		},
		{
			name: "primary is ready",
			rs:   statusWithSelf(mongo.MemberStatePrimary, 100, nil),
		},
		{
			name: "secondary is ready",
			rs:   statusWithSelf(mongo.MemberStateSecondary, 100, nil),
		},
		{
			name: "arbiter is ready",
			rs:   statusWithSelf(mongo.MemberStateArbiter, 100, nil),
		},
		{
			name:    "startup is not ready",
			rs:      statusWithSelf(mongo.MemberStateStartup, 1, nil),
			wantErr: true,
		},
		{
			name:    "startup2 is not ready",
			rs:      statusWithSelf(mongo.MemberStateStartup2, 1, nil),
			wantErr: true,
		},
		{
			name:    "recovering is not ready",
			rs:      statusWithSelf(mongo.MemberStateRecovering, 1, nil),
			wantErr: true,
		},
		{
			name:    "rollback is not ready",
			rs:      statusWithSelf(mongo.MemberStateRollback, 1, nil),
			wantErr: true,
		},
		{
			name:    "unknown state",
			rs:      statusWithSelf(mongo.MemberStateUnknown, 1, nil),
			wantErr: true,
		},
		{
			name:    "down state",
			rs:      statusWithSelf(mongo.MemberStateDown, 1, nil),
			wantErr: true,
		},
		{
			name:    "removed state",
			rs:      statusWithSelf(mongo.MemberStateRemoved, 1, nil),
			wantErr: true,
		},
		{
			name:    "out-of-range state",
			rs:      statusWithSelf(mongo.MemberState(99), 1, nil),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckStateForReadiness(tt.rs)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
