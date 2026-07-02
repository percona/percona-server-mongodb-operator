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
	"net"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// MongodReadinessCheck runs a ping on a pmgo.SessionManager to check server readiness
func MongodReadinessCheck(ctx context.Context, cnf *db.Config) error {
	log := logf.FromContext(ctx).WithName("MongodReadinessCheck")
	ctx = logf.IntoContext(ctx, log)

	var d net.Dialer

	if len(cnf.Hosts) == 0 {
		return errors.New("no hosts found")
	}
	addr := cnf.Hosts[0]
	log.V(1).Info("Connecting to " + addr)
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return errors.Wrap(err, "dial")
	}
	if err := conn.Close(); err != nil {
		return err
	}

	s, err := func() (status *mongo.Status, err error) {
		cnf.Timeout = time.Second
		client, err := db.Dial(ctx, cnf)
		if err != nil {
			// The operator waits for the StatefulSet to be ready before initializing the database.
			// Until then, it is not possible to connect to it.
			// We should ignore any errors from Dial to allow the cluster to be deployed.
			return nil, nil
		}
		defer func() {
			if derr := client.Disconnect(ctx); derr != nil && err == nil {
				err = errors.Wrap(derr, "failed to disconnect")
			}
		}()
		var rs mongo.Status
		rs, err = client.RSStatus(ctx)
		if err != nil {
			// We should ignore this error to allow operator to fix the config. This pod should be ready
			if errors.Is(err, mongo.ErrInvalidReplsetConfig) {
				log.Info("Couldn't connect to mongo due to invalid replset config. Ignoring", "error", err)
				return nil, nil
			}
			return nil, err
		}
		return &rs, nil
	}()
	if err != nil || s == nil {
		return errors.Wrap(err, "failed to get rs status")
	}

	if err := CheckStateForReadiness(*s); err != nil {
		return errors.Wrap(err, "check state")
	}

	return nil
}

func CheckStateForReadiness(rs mongo.Status) error {
	self := rs.GetSelf()
	if self == nil {
		return errors.New("self member is not found")
	}

	switch rs.MyState {
	case mongo.MemberStatePrimary, mongo.MemberStateSecondary, mongo.MemberStateArbiter:
		return nil
	case mongo.MemberStateStartup, mongo.MemberStateStartup2, mongo.MemberStateRollback, mongo.MemberStateRecovering:
		return errors.Errorf("member state is %s", mongo.MemberStateStrings[rs.MyState])
	case mongo.MemberStateUnknown, mongo.MemberStateDown, mongo.MemberStateRemoved:
		return errors.Errorf("unhealthy state %s", mongo.MemberStateStrings[rs.MyState])
	default:
		return errors.Errorf("state is unknown %d", rs.MyState)
	}
}

func MongosReadinessCheck(ctx context.Context, cnf *db.Config) (err error) {
	log := logf.FromContext(ctx).WithName("MongosReadinessCheck")
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

	log.V(1).Info("Running listDatabases")
	resp := mongo.OKResponse{}
	cur := client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "listDatabases", Value: 1},
		{Key: "filter", Value: bson.D{{Key: "name", Value: "admin"}}},
		{Key: "nameOnly", Value: true},
	})
	if cur.Err() != nil {
		return errors.Wrap(cur.Err(), "run listDatabases")
	}

	if err := cur.Decode(&resp); err != nil {
		return errors.Wrap(err, "decode listDatabases response")
	}

	if resp.OK == 0 {
		return errors.Wrap(errors.New("non-ok response from listDatabases"), resp.Errmsg)
	}

	return nil
}
