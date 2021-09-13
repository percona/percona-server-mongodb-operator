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
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/db"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/dcos"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/tool"
	log "github.com/sirupsen/logrus"
)

var (
	GitCommit     string
	GitBranch     string
	enableSecrets bool
)

func main() {
	app, _ := tool.New("Performs health and readiness checks for MongoDB", GitCommit, GitBranch)

	dcosCmd := app.Command("dcos", "Performs health and readiness checks for MongoDB on DC/OS")
	dcosCmd.Flag(
		"enableSecrets",
		"enable secrets, this causes passwords to be loaded from files, overridden by env var "+dcos.EnvSecretsEnabled,
	).Envar(dcos.EnvSecretsEnabled).BoolVar(&enableSecrets)
	dcosCmd.Command("health", "Run MongoDB health check")
	dcosCmd.Command("readiness", "Run MongoDB readiness check").Default()

	k8sCmd := app.Command("k8s", "Performs liveness check for MongoDB on Kubernetes")
	livenessCmd := k8sCmd.Command("liveness", "Run a liveness check of MongoDB").Default()
	_ = k8sCmd.Command("readiness", "Run a readiness check of MongoDB")
	startupDelaySeconds := livenessCmd.Flag("startupDelaySeconds", "").Default("7200").Uint64()
	component := k8sCmd.Flag("component", "").Default("mongod").String()

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

	if enableSecrets {
		cnf.Password = tools.PasswordFromFile(
			os.Getenv(dcos.EnvMesosSandbox),
			cnf.Password,
			"password",
		)
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

	case "dcos health":
		log.Debug("Running DC/OS health check")
		state, memberState, err := healthcheck.HealthCheck(client, healthcheck.OkMemberStates)
		if err != nil {
			log.Debug(err.Error())
			if err := client.Disconnect(context.TODO()); err != nil {
				log.Errorf("failed to disconnect: %v", err)
			}
			os.Exit(state.ExitCode())
		}
		log.Debugf("Member passed DC/OS health check with replication state: %d", *memberState)

	case "dcos readiness":
		log.Debug("Running DC/OS readiness check")
		state, err := healthcheck.ReadinessCheck(client)
		if err != nil {
			log.Debug(err.Error())
			if err := client.Disconnect(context.TODO()); err != nil {
				log.Errorf("failed to disconnect: %v", err)
			}
			os.Exit(state.ExitCode())
		}
		log.Debug("Member passed DC/OS readiness check")

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
