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
	"strconv"

	"github.com/alecthomas/kingpin"
	"github.com/percona/mongodb-orchestration-tools/controller"
	"github.com/percona/mongodb-orchestration-tools/controller/replset"
	"github.com/percona/mongodb-orchestration-tools/internal/db"
	"github.com/percona/mongodb-orchestration-tools/internal/tool"
	"github.com/percona/mongodb-orchestration-tools/pkg"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod/k8s"
	log "github.com/sirupsen/logrus"
)

var (
	GitCommit string
	GitBranch string
	cmdInit   *kingpin.CmdClause
)

func handleInitCmd(app *kingpin.Application, cnf *controller.Config) {
	cmdInit = app.Command("init", "Initiate a MongoDB replica set").Default()

	// replset init
	cmdInit.Flag(
		"delay",
		"amount of time to delay the init process, overridden by env var "+pkg.EnvInitInitiateDelay,
	).Default(k8s.DefaultInitDelay).Envar(pkg.EnvInitInitiateDelay).DurationVar(&cnf.ReplsetInit.Delay)

	cmdInit.Flag(
		"maxConnectTries",
		"number of times to retry connect to mongodb, overridden by env var "+pkg.EnvInitMaxConnectTries,
	).Default(controller.DefaultMaxConnectTries).Envar(pkg.EnvInitMaxConnectTries).UintVar(&cnf.ReplsetInit.MaxConnectTries)

	cmdInit.Flag(
		"maxReplTries",
		"number of times to retry initiating mongodb replica set, overridden by env var "+pkg.EnvInitMaxInitReplsetTries,
	).Default(controller.DefaultInitMaxReplTries).Envar(pkg.EnvInitMaxInitReplsetTries).UintVar(&cnf.ReplsetInit.MaxReplTries)

	cmdInit.Flag(
		"retrySleep",
		"amount of time to wait between retries, overridden by env var "+pkg.EnvInitRetrySleep,
	).Default(controller.DefaultRetrySleep).Envar(pkg.EnvInitRetrySleep).DurationVar(&cnf.ReplsetInit.RetrySleep)
}

func main() {
	app, _ := tool.New("Performs replset initiation for percona-server-mongodb-operator", GitCommit, GitBranch)

	var namespace, ip string
	var port int
	hostname, err := os.Hostname()
	if err != nil {
		log.Fatalf("Could not load hostname: %v", err)
	}

	cnf := &controller.Config{
		ReplsetInit: &controller.ConfigReplsetInit{},
	}

	app.Flag(
		"service",
		"Kubernetes service name, overridden by env var "+pkg.EnvServiceName,
	).Default(pkg.DefaultServiceName).Envar(pkg.EnvServiceName).StringVar(&cnf.ServiceName)

	app.Flag(
		"namespace",
		"Kubernetes namespace, overridden by env var "+k8s.EnvNamespace,
	).Default(k8s.DefaultNamespace).Envar(k8s.EnvNamespace).StringVar(&namespace)

	app.Flag(
		"replset",
		"mongodb replica set name, this flag or env var "+pkg.EnvMongoDBReplset+" is required",
	).Envar(pkg.EnvMongoDBReplset).Required().StringVar(&cnf.Replset)

	app.Flag(
		"ip",
		"Kubernetes service IP address, overridden by env var"+pkg.EnvMongoDBIp,
	).Envar(pkg.EnvMongoDBIp).StringVar(&ip)

	app.Flag(
		"port",
		"mongodb port number, this flag or env var "+pkg.EnvMongoDBPort+" is required",
	).Envar(pkg.EnvMongoDBPort).Required().IntVar(&port)

	app.Flag(
		"userAdminUser",
		"mongodb userAdmin username, overridden by env var "+pkg.EnvMongoDBUserAdminUser,
	).Envar(pkg.EnvMongoDBUserAdminUser).Required().StringVar(&cnf.UserAdminUser)

	app.Flag(
		"userAdminPassword",
		"mongodb userAdmin password or path to file containing it, overridden by env var "+pkg.EnvMongoDBUserAdminPassword,
	).Envar(pkg.EnvMongoDBUserAdminPassword).Required().StringVar(&cnf.UserAdminPassword)

	cnf.SSL = db.NewSSLConfig(app)

	handleInitCmd(app, cnf)

	command, err := app.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("Cannot parse command line: %s", err)
	}
	if port <= 1024 {
		log.Fatalf("Port must be > 1024")
	}

	switch command {
	case cmdInit.FullCommand():
		var host string
		if ip != "" {
			host = ip
		} else {
			host = k8s.GetMongoHost(hostname, cnf.ServiceName, cnf.Replset, namespace)
		}

		cnf.ReplsetInit.PrimaryAddr = host + ":" + strconv.Itoa(port)

		err := replset.NewInitiator(cnf).Run()
		if err != nil {
			log.Fatalf("Error initiating replset: %v", err)
		}
	}
}
