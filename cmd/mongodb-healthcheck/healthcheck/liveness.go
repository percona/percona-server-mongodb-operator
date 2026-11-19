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

func HealthCheckMongodLiveness(ctx context.Context, cnf *db.Config) (_ *mongo.MemberState, err error) {
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

	rsStatus, err := client.RSStatus(ctx)
	if err != nil {
		if err.Error() == ErrNoReplsetConfigStr {
			state := mongo.MemberStateUnknown
			log.V(1).Info("replSetGetStatus failed", "err", err.Error(), "state", state)
			return &state, nil
		}
		return nil, errors.Wrap(err, "get replSetGetStatus response")
	}

	log.V(1).Info("Checking state", "state", rsStatus.MyState)
	if err := CheckStateForLiveness(ctx, rsStatus); err != nil {
		return &rsStatus.MyState, err
	}

	return &rsStatus.MyState, nil
}

func CheckStateForLiveness(ctx context.Context, rs mongo.Status) error {
	self := rs.GetSelf()
	if self == nil {
		return errors.New("self member is not found")
	}

	log := logf.FromContext(ctx)

	switch rs.MyState {
	case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
		return nil
	case mongo.MemberStateStartup, mongo.MemberStateStartup2:
		log.Info("Member state is " + mongo.MemberStateStrings[rs.MyState])
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
