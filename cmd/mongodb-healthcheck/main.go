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

package main

import (
	"context"
	"os"

	"github.com/percona/percona-server-mongodb-operator/healthcheck"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/db"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/tool"
	log "github.com/sirupsen/logrus"
)

var (
	GitCommit string
	GitBranch string
)

func main() {
	app, _ := tool.New("Performs health and readiness checks for MongoDB", GitCommit, GitBranch)

	k8sCmd := app.Command("k8s", "Performs liveness check for MongoDB on Kubernetes")
	livenessCmd := k8sCmd.Command("liveness", "Run a liveness check of MongoDB").Default()
	_ = k8sCmd.Command("readiness", "Run a readiness check of MongoDB")
	startupDelaySeconds := livenessCmd.Flag("startupDelaySeconds", "").Default("7200").Uint64()
	component := k8sCmd.Flag("component", "").Default("mongod").String()

	restoreInProgress, err := fileExists("/opt/percona/restore-in-progress")
	if err != nil {
		log.Fatalf("check if restore in progress: %v", err)
	}

	if restoreInProgress {
		os.Exit(0)
	}

	cnf, err := db.NewConfig(
		app,
		pkg.EnvMongoDBClusterMonitorUser,
		pkg.EnvMongoDBClusterMonitorPassword,
	)
	if err != nil {
		log.Fatalf("new cfg: %s", err)
	}

	command, err := app.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("Cannot parse command line: %s", err)
	}

	client, err := db.Dial(cnf)
	if err != nil {
		log.Fatalf("connection error: %v", err)
	}

	defer func() {
		if err := client.Disconnect(context.TODO()); err != nil {
			log.Fatalf("failed to disconnect: %v", err)
		}
	}()

	switch command {

	case "k8s liveness":
		log.Infof("Running Kubernetes liveness check for %s", *component)
		switch *component {

		case "mongod":
			memberState, err := healthcheck.HealthCheckMongodLiveness(client, int64(*startupDelaySeconds))
			if err != nil {
				client.Disconnect(context.TODO()) // nolint:golint,errcheck
				log.Errorf("Member failed Kubernetes liveness check: %s", err.Error())
				os.Exit(1)
			}
			log.Infof("Member passed Kubernetes liveness check with replication state: %d", *memberState)

		case "mongos":
			err := healthcheck.HealthCheckMongosLiveness(client)
			if err != nil {
				client.Disconnect(context.TODO()) // nolint:golint,errcheck
				log.Errorf("Member failed Kubernetes liveness check: %s", err.Error())
				os.Exit(1)
			}
			log.Infof("Member passed Kubernetes liveness check")
		}

	case "k8s readiness":
		log.Infof("Running Kubernetes readiness check for %s", *component)
		switch *component {

		case "mongod":
			client.Disconnect(context.TODO()) // nolint:golint,errcheck
			log.Error("readiness check for mongod is not implemented")
			os.Exit(1)

		case "mongos":
			err := healthcheck.MongosReadinessCheck(client)
			if err != nil {
				client.Disconnect(context.TODO()) // nolint:golint,errcheck
				log.Errorf("Member failed Kubernetes readiness check: %s", err.Error())
				os.Exit(1)
			}
		}
	}
}

func fileExists(name string) (bool, error) {
	_, err := os.Stat(name)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
