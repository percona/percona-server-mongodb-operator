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

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

// MongodReadinessCheck runs a ping on a pmgo.SessionManager to check server readiness
func MongodReadinessCheck(ctx context.Context, addr string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return errors.Wrap(err, "dial")
	}
	return conn.Close()
}

func MongosReadinessCheck(ctx context.Context, cnf *db.Config) (err error) {
	client, err := db.Dial(ctx, cnf)
	if err != nil {
		return errors.Wrap(err, "connection error")
	}
	defer func() {
		if derr := client.Disconnect(ctx); derr != nil && err == nil {
			err = errors.Wrap(derr, "failed to disconnect")
		}
	}()

	ss := mongo.OKResponse{}
	cur := client.Database("admin").RunCommand(ctx, bson.D{
		{Key: "listDatabases", Value: 1},
		{Key: "filter", Value: bson.D{{Key: "name", Value: "admin"}}},
		{Key: "nameOnly", Value: true},
	})
	if cur.Err() != nil {
		return errors.Wrap(cur.Err(), "run listDatabases")
	}

	if err := cur.Decode(&ss); err != nil {
		return errors.Wrap(err, "decode listDatabases response")
	}

	if ss.OK == 0 {
		return errors.New(ss.Errmsg)
	}

	return nil
}
