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
	"context"
	"fmt"

	v "github.com/hashicorp/go-version"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	mgo "go.mongodb.org/mongo-driver/mongo"
)

// OkMemberStates is a slice of acceptable replication member states
var OkMemberStates = []mongo.MemberState{
	mongo.MemberStatePrimary,
	mongo.MemberStateSecondary,
	mongo.MemberStateRecovering,
	mongo.MemberStateArbiter,
	mongo.MemberStateStartup2,
	mongo.MemberStateRollback,
}

var ErrNoReplsetConfigStr string = "no replset config has been received"

// getSelfMemberState returns the replication state of the local MongoDB member
func getSelfMemberState(rsStatus *mongo.Status) *mongo.MemberState {
	member := rsStatus.GetSelf()
	if member == nil || member.Health != mongo.MemberHealthUp {
		return nil
	}
	return &member.State
}

// isStateOk checks if a replication member state matches one of the acceptable member states in 'OkMemberStates'
func isStateOk(memberState *mongo.MemberState, okMemberStates []mongo.MemberState) bool {
	for _, state := range okMemberStates {
		if *memberState == state {
			return true
		}
	}
	return false
}

// HealthCheck checks the replication member state of the local MongoDB member
func HealthCheck(client *mgo.Client, okMemberStates []mongo.MemberState) (State, *mongo.MemberState, error) {
	rsStatus, err := mongo.RSStatus(context.TODO(), client)
	if err != nil {
		return StateFailed, nil, fmt.Errorf("error getting replica set status: %s", err)
	}

	state := getSelfMemberState(&rsStatus)
	if state == nil {
		return StateFailed, state, fmt.Errorf("found no member state for self in replica set status")
	}
	if isStateOk(state, okMemberStates) {
		return StateOk, state, nil
	}

	return StateFailed, state, fmt.Errorf("member has unhealthy replication state: %d", state)
}

func HealthCheckMongosLiveness(client *mgo.Client) error {
	isMasterResp, err := mongo.IsMaster(context.TODO(), client)
	if err != nil {
		return errors.Wrap(err, "get isMaster response")
	}

	if isMasterResp.Msg != "isdbgrid" {
		return errors.New("wrong msg")
	}

	return nil
}

func HealthCheckMongodLiveness(client *mgo.Client, startupDelaySeconds int64) (*mongo.MemberState, error) {
	isMasterResp, err := mongo.IsMaster(context.TODO(), client)
	if err != nil {
		return nil, errors.Wrap(err, "get isMaster response")
	}

	buildInfo, err := mongo.RSBuildInfo(context.TODO(), client)
	if err != nil {
		return nil, errors.Wrap(err, "get buildInfo response")
	}

	replSetStatusCommand := bson.D{{Key: "replSetGetStatus", Value: 1}}
	mongoVersion := v.Must(v.NewVersion(buildInfo.Version))
	if mongoVersion.Compare(v.Must(v.NewVersion("4.2.1"))) < 0 {
		// https://docs.mongodb.com/manual/reference/command/replSetGetStatus/#syntax
		replSetStatusCommand = append(replSetStatusCommand, primitive.E{Key: "initialSync", Value: 1})
	}

	res := client.Database("admin").RunCommand(context.TODO(), replSetStatusCommand)
	if res.Err() != nil {
		return nil, errors.Wrap(err, "get replsetGetStatus response")
	}

	rsStatus := ReplSetStatus{}
	if err := res.Decode(&rsStatus); err != nil {
		// if we come this far, it means db connection was successful
		// standalone mongod nodes in an unmanaged cluster doesn't need
		// to die before they added to a replset
		if err.Error() == ErrNoReplsetConfigStr {
			return nil, nil
		}
		return nil, errors.Wrap(err, "get replsetGetStatus response")
	}

	oplogRs := OplogRs{}
	if !isMasterResp.IsArbiter {
		res := client.Database("local").RunCommand(context.TODO(), bson.D{
			{Key: "collStats", Value: "oplog.rs"},
			{Key: "scale", Value: 1024 * 1024 * 1024}, // scale size to gigabytes
		})
		if res.Err() != nil {
			return nil, errors.Wrap(res.Err(), "get oplog.rs info")
		}
		if err := res.Decode(&oplogRs); err != nil {
			return nil, errors.Wrap(err, "decode oplog.rs info")
		}
		if oplogRs.Ok == 0 {
			return nil, errors.New(oplogRs.Errmsg)
		}
	}

	var storageSize int64 = 0
	if oplogRs.StorageSize > 0 {
		storageSize = oplogRs.StorageSize
	}

	if err := CheckState(rsStatus, startupDelaySeconds, storageSize); err != nil {
		return &rsStatus.MyState, err
	}

	return &rsStatus.MyState, nil
}

type ServerStatus struct {
	Ok     int    `bson:"ok" json:"ok"`
	Errmsg string `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
}

type OplogRs struct {
	StorageSize int64 `bson:"storageSize" json:"storageSize"`

	Ok     int    `bson:"ok" json:"ok"`
	Errmsg string `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
}

type ReplSetStatus struct {
	mongo.Status      `bson:",inline"`
	InitialSyncStatus InitialSyncStatus `bson:"initialSyncStatus" json:"initialSyncStatus"`
}

type InitialSyncStatus interface{}

func CheckState(rs ReplSetStatus, startupDelaySeconds int64, oplogSize int64) error {
	uptime := rs.GetSelf().Uptime

	switch rs.MyState {
	case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
		return nil
	case mongo.MemberStateStartup, mongo.MemberStateStartup2:
		if (rs.InitialSyncStatus == nil && uptime > 30+oplogSize*60) || // give 60 seconds to each 1Gb of oplog
			(rs.InitialSyncStatus != nil && uptime > startupDelaySeconds) {
			return fmt.Errorf("state is %d and uptime is %d", rs.MyState, uptime)
		}
	case mongo.MemberStateRecovering:
		if uptime > startupDelaySeconds {
			return fmt.Errorf("state is %d and uptime is %d", rs.MyState, uptime)
		}
	case mongo.MemberStateUnknown, mongo.MemberStateDown, mongo.MemberStateRollback, mongo.MemberStateRemoved:
		return fmt.Errorf("invalid state %d", rs.MyState)
	default:
		return fmt.Errorf("state is unknown %d", rs.MyState)
	}

	return nil
}
