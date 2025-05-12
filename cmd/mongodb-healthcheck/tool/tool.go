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

package tool

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/alecthomas/kingpin"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/errors"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/healthcheck"
)

// Author is the author used by kingpin
const Author = "Percona LLC."

// New sets up a new kingpin.Application
func New(help, commit, branch string) *App {
	app := kingpin.New(filepath.Base(os.Args[0]), help)
	app.Author(Author)
	app.Version(fmt.Sprintf(
		"%s version %s\ngit commit %s, branch %s\ngo version %s",
		app.Name, Version, commit, branch, runtime.Version(),
	))
	return &App{app}
}

type App struct {
	*kingpin.Application
}

func (app *App) Run(ctx context.Context) error {
	log := logf.FromContext(ctx)

	k8sCmd := app.Command("k8s", "Performs liveness check for MongoDB on Kubernetes")
	livenessCmd := k8sCmd.Command("liveness", "Run a liveness check of MongoDB").Default()
	readinessCmd := k8sCmd.Command("readiness", "Run a readiness check of MongoDB")
	startupDelaySeconds := livenessCmd.Flag("startupDelaySeconds", "").Default("7200").Uint64()
	component := k8sCmd.Flag("component", "").Default("mongod").String()

	_, err := os.Stat("/opt/percona/restore-in-progress")
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "check if restore in progress file exists")
	}
	if err == nil {
		return nil
	}

	_, err = os.Stat("/data/db/sleep-forever")
	if err != nil && !os.IsNotExist(err) {
		return errors.Wrap(err, "check if sleep-forever file exists")
	}
	if err == nil {
		return nil
	}

	cnf, err := NewConfig(
		app.Application,
		EnvMongoDBClusterMonitorUser,
		EnvMongoDBClusterMonitorPassword,
	)
	if err != nil {
		return errors.Wrap(err, "new cfg")
	}

	command, err := app.Parse(os.Args[1:])
	if err != nil {
		return errors.Wrap(err, "cannot parse command line")
	}

	switch command {
	case livenessCmd.FullCommand():
		log.Info("Running Kubernetes liveness check for", "component", component)
		switch *component {

		case "mongod":
			memberState, err := healthcheck.HealthCheckMongodLiveness(ctx, cnf, int64(*startupDelaySeconds))
			if err != nil {
				return errors.Wrap(err, "member failed Kubernetes liveness check")
			}
			log.Info("Member passed Kubernetes liveness check with replication state", "state", memberState)

		case "mongos":
			err := healthcheck.HealthCheckMongosLiveness(ctx, cnf)
			if err != nil {
				return errors.Wrap(err, "member failed Kubernetes liveness check")
			}
			log.Info("Member passed Kubernetes liveness check")
		}

	case readinessCmd.FullCommand():
		log.Info("Running Kubernetes readiness check for component", "component", component)
		switch *component {

		case "mongod":
			err := healthcheck.MongodReadinessCheck(ctx, cnf)
			if err != nil {
				return errors.Wrap(err, "member failed Kubernetes readiness check")
			}
		case "mongos":
			err := healthcheck.MongosReadinessCheck(ctx, cnf)
			if err != nil {
				return errors.Wrap(err, "member failed Kubernetes readiness check")
			}
		}
	}
	return nil
}
