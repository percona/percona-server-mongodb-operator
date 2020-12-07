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
	"os"

	"github.com/percona/percona-server-mongodb-operator/healthcheck"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/db"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/dcos"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/tool"
	"github.com/percona/pmgo"
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
	startupDelaySeconds := livenessCmd.Flag("startupDelaySeconds", "").Default("7200").Uint64()

	cnf := db.NewConfig(
		app,
		pkg.EnvMongoDBClusterMonitorUser,
		pkg.EnvMongoDBClusterMonitorPassword,
	)

	command, err := app.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("Cannot parse command line: %s", err)
	}
	if enableSecrets {
		cnf.DialInfo.Password = tools.PasswordFromFile(
			os.Getenv(dcos.EnvMesosSandbox),
			cnf.DialInfo.Password,
			"password",
		)
	}
	sslConf := db.SSLConfig{}
	cnf.SSL = &sslConf
	cnf.SSL.Insecure = true

	session, err := db.GetSession(cnf)
	if err != nil {
		log.Info("ssl connection error: " + err.Error())
	}

	if session == nil {
		cnf.SSL = nil
		session, err = db.GetSession(cnf)
		if err != nil {
			log.Fatalf("Error connecting to mongodb: %s", err)
			return
		}

	}

	defer session.Close()

	switch command {
	case "dcos health":
		log.Debug("Running DC/OS health check")
		state, memberState, err := healthcheck.HealthCheck(session, healthcheck.OkMemberStates)
		if err != nil {
			log.Debug(err.Error())
			session.Close()
			os.Exit(state.ExitCode())
		}
		log.Debugf("Member passed DC/OS health check with replication state: %s", memberState)
	case "dcos readiness":
		log.Debug("Running DC/OS readiness check")
		state, err := healthcheck.ReadinessCheck(pmgo.NewSessionManager(session))
		if err != nil {
			log.Debug(err.Error())
			session.Close()
			os.Exit(state.ExitCode())
		}
		log.Debug("Member passed DC/OS readiness check")
	case "k8s liveness":
		log.Info("Running Kubernetes liveness check")
		memberState, err := healthcheck.HealthCheckLiveness(session, int64(*startupDelaySeconds))
		if err != nil {
			log.Error(err.Error())
			session.Close()
			os.Exit(1)
		}
		log.Infof("Member passed Kubernetes liveness check with replication state: %s", memberState)
	}
}
