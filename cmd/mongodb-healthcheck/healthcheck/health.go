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

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var ErrNoReplsetConfigStr = "(NotYetInitialized) no replset config has been received"

func HealthCheckMongosLiveness(ctx context.Context, cnf *db.Config) (err error) {
	log := logf.FromContext(ctx).WithName("HealthCheckMongosLiveness")
	ctx = logf.IntoContext(ctx, log)

	client, err := db.Dial(ctx, cnf)
	if err != nil {
		return errors.Wrap(err, "connection error")
	}
	defer func() {
		if derr := client.Disconnect(ctx); derr != nil && err == nil {
			err = errors.Wrap(derr, "failed to disconnect")
		}
	}()

	isMasterResp, err := client.IsMaster(ctx)
	if err != nil {
		return errors.Wrap(err, "get isMaster response")
	}

	if isMasterResp.Msg != "isdbgrid" {
		log.V(1).Info("Wrong isMaster msg", "msg", isMasterResp.Msg)
		return errors.New("wrong msg")
	}

	return nil
}

func HealthCheckMongodLiveness(ctx context.Context, cnf *db.Config, startupDelaySeconds int64) (_ *mongo.MemberState, err error) {
	log := logf.FromContext(ctx).WithName("HealthCheckMongodLiveness")
	ctx = logf.IntoContext(ctx, log)

	client, err := db.Dial(ctx, cnf)
	if err != nil {
		return nil, errors.Wrap(err, "connection error")
	}
	defer func() {
		if derr := client.Disconnect(ctx); derr != nil && err == nil {
			err = errors.Wrap(derr, "failed to disconnect")
		}
	}()

	isMasterResp, err := client.IsMaster(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "get isMaster response")
	}

	rsStatus, err := getStatus(ctx, client)
	if err != nil {
		if err.Error() == ErrNoReplsetConfigStr {
			state := mongo.MemberStateUnknown
			log.V(1).Info("replSetGetStatus failed", "err", err.Error(), "state", state)
			return &state, nil
		}
		return nil, errors.Wrap(err, "get replSetGetStatus response")
	}

	oplogRs := OplogRs{}
	if !isMasterResp.IsArbiter {
		log.V(1).Info("Getting \"oplog.rs\" info")
		res := client.Database("local").RunCommand(ctx, bson.D{
			{Key: "collStats", Value: "oplog.rs"},
			{Key: "scale", Value: 1024 * 1024 * 1024}, // scale size to gigabytes
		})
		if res.Err() != nil {
			return nil, errors.Wrap(res.Err(), "get oplog.rs info")
		}
		if err := res.Decode(&oplogRs); err != nil {
			return nil, errors.Wrap(err, "decode oplog.rs info")
		}
		if oplogRs.OK == 0 {
			return nil, errors.Wrap(errors.New("non-ok response from getting oplog.rs info"), oplogRs.Errmsg)
		}
	}

	var storageSize int64
	if oplogRs.StorageSize > 0 {
		storageSize = oplogRs.StorageSize
	}

	log.V(1).Info("Checking state", "state", rsStatus.MyState, "storage size", storageSize)
	if err := CheckState(rsStatus, startupDelaySeconds, storageSize); err != nil {
		return &rsStatus.MyState, err
	}

	return &rsStatus.MyState, nil
}

type OplogRs struct {
	mongo.OKResponse `bson:",inline"`
	StorageSize      int64 `bson:"storageSize" json:"storageSize"`
}

type ReplSetStatus struct {
	InitialSyncStatus InitialSyncStatus `bson:"initialSyncStatus" json:"initialSyncStatus"`
	mongo.Status      `bson:",inline"`
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
			return errors.Errorf("state is %d and uptime is %d", rs.MyState, uptime)
		}
	case mongo.MemberStateRecovering:
		if uptime > startupDelaySeconds {
			return errors.Errorf("state is %d and uptime is %d", rs.MyState, uptime)
		}
	case mongo.MemberStateUnknown, mongo.MemberStateDown, mongo.MemberStateRollback, mongo.MemberStateRemoved:
		return errors.Errorf("invalid state %d", rs.MyState)
	default:
		return errors.Errorf("state is unknown %d", rs.MyState)
	}

	return nil
}
