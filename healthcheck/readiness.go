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
	"errors"
	"fmt"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

// ReadinessCheck runs a ping on a pmgo.SessionManager to check server readiness
func ReadinessCheck(session *mgo.Session) (State, error) {
	err := session.Ping()

	if err != nil {
		return StateFailed, fmt.Errorf("failed to get successful ping: %s", err)
	}

	return StateOk, nil
}

func MongosReadinessCheck(session *mgo.Session) error {
	ss := ServerStatus{}

	if err := session.Run(bson.D{{Name: "listDatabases", Value: 1}}, &ss); err != nil {
		return fmt.Errorf("listDatabases returned error %v", err)
	}

	if ss.Ok == 0 {
		return errors.New(ss.Errmsg)
	}

	return nil
}
