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
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/readpref"
)

// ReadinessCheck runs a ping on a pmgo.SessionManager to check server readiness
func ReadinessCheck(client *mongo.Client) (State, error) {
	if err := client.Ping(context.TODO(), readpref.Primary()); err != nil {
		return StateFailed, errors.Wrap(err, "ping")
	}

	return StateOk, nil
}

func MongosReadinessCheck(client *mongo.Client) error {
	ss := ServerStatus{}
	cur := client.Database("admin").RunCommand(context.TODO(), bson.D{
		{Key: "listDatabases", Value: 1},
		{Key: "filter", Value: bson.D{{Key: "name", Value: "admin"}}},
		{Key: "nameOnly", Value: true}})
	if cur.Err() != nil {
		return errors.Wrap(cur.Err(), "run listDatabases")
	}

	if err := cur.Decode(&ss); err != nil {
		return errors.Wrap(err, "decode listDatabases response")
	}

	if ss.Ok == 0 {
		return errors.New(ss.Errmsg)
	}

	return nil
}
