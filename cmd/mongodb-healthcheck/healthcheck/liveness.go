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
	"strings"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var ErrNoReplsetConfigStr = "(NotYetInitialized) no replset config has been received"

const notYetInitializedCode = 94

// isNotYetInitialized matches the NotYetInitialized error also when it is
// wrapped, e.g. "replSetGetStatus: (NotYetInitialized) ...".
func isNotYetInitialized(err error) bool {
	var cmdErr mongodriver.CommandError
	if errors.As(err, &cmdErr) && cmdErr.Code == notYetInitializedCode {
		return true
	}
	return strings.Contains(err.Error(), ErrNoReplsetConfigStr)
}

// awaitingReplsetInit reports whether mongod runs with --replSet but the
// replset has not been initiated yet. It connects without credentials: such a
// member has no users, so authenticating as the monitor user always fails and
// the probe would otherwise kill a healthy member that the operator has not
// initiated yet. hello does not require authentication.
func awaitingReplsetInit(ctx context.Context, cnf *db.Config) bool {
	log := logf.FromContext(ctx)

	anonymous := *cnf
	anonymous.Username = ""
	anonymous.Password = ""
	anonymous.Direct = true
	anonymous.ReplSetName = ""

	client, err := dial(ctx, &anonymous)
	if err != nil {
		log.V(1).Info("Unauthenticated connection failed, skipping replset initiation check", "err", err.Error())
		return false
	}
	defer func() { _ = client.Disconnect(ctx) }()

	var hello struct {
		IsReplicaSet bool   `bson:"isreplicaset"`
		SetName      string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		log.V(1).Info("hello failed, skipping replset initiation check", "err", err.Error())
		return false
	}
	return hello.IsReplicaSet && hello.SetName == ""
}

// dial is replaced in tests.
var dial = db.Dial

// A rejected login means the server answered, so it is alive. The probe reads
// its credentials from the mounted users secret, which the kubelet refreshes
// up to a minute after the operator changes a system user in MongoDB, and a
// restart would not fix them.
const authFailedMsg = "Authentication failed, skipping liveness check"

func HealthCheckMongosLiveness(ctx context.Context, cnf *db.Config) (err error) {
	log := logf.FromContext(ctx).WithName("HealthCheckMongosLiveness")
	ctx = logf.IntoContext(ctx, log)

	client, err := dial(ctx, cnf)
	if err != nil {
		if mongo.IsAuthenticationFailed(err) {
			log.Info(authFailedMsg, "error", err.Error())
			return nil
		}
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

func HealthCheckMongodLiveness(ctx context.Context, cnf *db.Config) (_ *mongo.MemberState, err error) {
	log := logf.FromContext(ctx).WithName("HealthCheckMongodLiveness")
	ctx = logf.IntoContext(ctx, log)

	if awaitingReplsetInit(ctx, cnf) {
		state := mongo.MemberStateUnknown
		log.V(1).Info("Member is waiting for replset initiation", "state", state)
		return &state, nil
	}

	client, err := dial(ctx, cnf)
	if err != nil {
		if mongo.IsAuthenticationFailed(err) {
			log.Info(authFailedMsg, "error", err.Error())
			return nil, nil
		}
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

	rsStatus, err := client.RSStatus(ctx)
	if err != nil {
		if isNotYetInitialized(err) {
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
	if err := CheckStateForLiveness(ctx, rsStatus, storageSize); err != nil {
		return &rsStatus.MyState, err
	}

	return &rsStatus.MyState, nil
}

type OplogRs struct {
	mongo.OKResponse `bson:",inline"`
	StorageSize      int64 `bson:"storageSize" json:"storageSize"`
}

func CheckStateForLiveness(ctx context.Context, rs mongo.Status, oplogSize int64) error {
	self := rs.GetSelf()
	if self == nil {
		return errors.New("self member is not found")
	}

	log := logf.FromContext(ctx)

	switch rs.MyState {
	case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
		return nil
	case mongo.MemberStateStartup, mongo.MemberStateStartup2:
		// give 60 seconds to each 1Gb of oplog + 30 seconds
		if rs.InitialSyncStatus == nil && self.Uptime > 30+oplogSize*60 {
			return errors.Errorf("state is %s and uptime is %d", mongo.MemberStateStrings[rs.MyState], self.Uptime)
		}
		if rs.InitialSyncStatus != nil {
			log.Info("Initial sync is in progress")
		}
	case mongo.MemberStateRollback, mongo.MemberStateRecovering:
		log.Info("Member state is " + mongo.MemberStateStrings[rs.MyState])
	case mongo.MemberStateUnknown, mongo.MemberStateDown, mongo.MemberStateRemoved:
		return errors.Errorf("unhealthy state %s", mongo.MemberStateStrings[rs.MyState])
	default:
		return errors.Errorf("state is unknown %d", rs.MyState)
	}

	return nil
}
