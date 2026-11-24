// Copyright 2026 Percona LLC
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
	"time"

	"github.com/pkg/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
)

// MongodStartupCheck succeeds as soon as mongod accepts connections.
func MongodStartupCheck(ctx context.Context, cnf *db.Config) (err error) {
	log := logf.FromContext(ctx).WithName("MongodStartupCheck")
	ctx = logf.IntoContext(ctx, log)

	// Users may not exist yet on a new cluster, and the operator only creates
	// them after pods are ready, so this check must not authenticate.
	c := *cnf
	c.Username = ""
	c.Password = ""
	c.ReplSetName = ""
	c.Direct = true
	c.Timeout = 2 * time.Second

	client, err := db.Dial(ctx, &c)
	if err != nil {
		return errors.Wrap(err, "connection error")
	}
	defer func() {
		if derr := client.Disconnect(ctx); derr != nil && err == nil {
			err = errors.Wrap(derr, "failed to disconnect")
		}
	}()

	return nil
}
