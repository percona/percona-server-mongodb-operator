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

	"github.com/alecthomas/kingpin"
	"github.com/percona/mongodb-orchestration-tools/controller"
	"github.com/percona/mongodb-orchestration-tools/controller/replset"
	"github.com/percona/mongodb-orchestration-tools/controller/user"
	"github.com/percona/mongodb-orchestration-tools/internal"
	"github.com/percona/mongodb-orchestration-tools/internal/db"
	"github.com/percona/mongodb-orchestration-tools/internal/dcos"
	"github.com/percona/mongodb-orchestration-tools/internal/dcos/api"
	"github.com/percona/mongodb-orchestration-tools/internal/tool"
	"github.com/percona/mongodb-orchestration-tools/pkg"
	log "github.com/sirupsen/logrus"
)

var (
	GitCommit        string
	GitBranch        string
	enableSecrets    bool
	cmdInit          *kingpin.CmdClause
	cmdReplset       *kingpin.CmdClause
	cmdUser          *kingpin.CmdClause
	cmdUserUpdate    *kingpin.CmdClause
	cmdUserRemove    *kingpin.CmdClause
	cmdUserReloadSys *kingpin.CmdClause
)

func handleReplsetCmd(app *kingpin.Application, cnf *controller.Config) {
	cmdReplset = app.Command("replset", "Control MongoDB replsets")
	cmdInit = cmdReplset.Command("init", "Initiate a MongoDB replica set")

	// replset init
	cmdInit.Flag(
		"primaryAddr",
		"mongodb primary (host:port) to use to initiate the replset, overridden by env var "+dcos.EnvMongoDBPrimaryAddr,
	).Envar(dcos.EnvMongoDBPrimaryAddr).Required().StringVar(&cnf.ReplsetInit.PrimaryAddr)
	cmdInit.Flag(
		"delay",
		"amount of time to delay the init process, overridden by env var "+pkg.EnvInitInitiateDelay,
	).Default(controller.DefaultInitDelay).Envar(pkg.EnvInitInitiateDelay).DurationVar(&cnf.ReplsetInit.Delay)
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

func handleUserCmd(app *kingpin.Application, cnf *controller.Config) {
	cmdUser = app.Command("user", "Control MongoDB users")
	cmdUserRemove = cmdUser.Command("remove", "Remove a MongoDB user")
	cmdUserUpdate = cmdUser.Command("update", "Add/update a MongoDB user")
	cmdUserReloadSys = cmdUser.Command("reload-system", "Reload the DCOS Framework MongoDB system users")

	// user
	cmdUser.Flag(
		"endpoint",
		"DC/OS SDK service mongod endpoint name, overridden by env var "+dcos.EnvMongoDBMongodEndpointName,
	).Default(dcos.DefaultMongoDBMongodEndpointName).Envar(dcos.EnvMongoDBMongodEndpointName).StringVar(&cnf.User.EndpointName)
	cmdUser.Flag(
		"apiHost",
		"DC/OS SDK API hostname, overridden by env var "+dcos.EnvSchedulerAPIHost,
	).Default(api.DefaultSchedulerHost).Envar(dcos.EnvSchedulerAPIHost).StringVar(&cnf.User.API.Host)
	cmdUser.Flag(
		"apiTimeout",
		"DC/OS SDK API timeout",
	).Default(api.DefaultHTTPTimeout).DurationVar(&cnf.User.API.Timeout)
	app.Flag(
		"apiSecure",
		"Use secure connections to DC/OS SDK API",
	).BoolVar(&cnf.User.API.Secure)
	cmdUser.Flag(
		"maxConnectTries",
		"number of times to retry connecting to mongodb",
	).Default(controller.DefaultMaxConnectTries).Envar("MONGODB_USER_CHANGE_CONNECT_TRIES").UintVar(&cnf.User.MaxConnectTries)
	cmdUser.Flag(
		"retrySleep",
		"number of times to retry connecting to mongodb",
	).Default(controller.DefaultRetrySleep).Envar("MONGODB_USER_CHANGE_RETRY_SLEEP").DurationVar(&cnf.User.RetrySleep)

	// user remove
	cmdUserRemove.Flag(
		"user",
		"the MongoDB user to be removed, system users will be skipped. this flag or env var "+dcos.EnvMongoDBChangeUserUsername+" is required",
	).Envar(dcos.EnvMongoDBChangeUserUsername).Required().StringVar(&cnf.User.Username)
	cmdUserRemove.Flag(
		"db",
		"the MongoDB database of the user, this flag or env var "+dcos.EnvMongoDBChangeUserDb+" is required",
	).Envar(dcos.EnvMongoDBChangeUserDb).Required().StringVar(&cnf.User.Database)

	// user update
	cmdUserUpdate.Arg(
		"file",
		"the required base64-encoded BSON file describing the MongoDB user to be updated",
	).Required().ExistingFileVar(&cnf.User.File)
	cmdUserUpdate.Flag(
		"db",
		"the MongoDB database of the user, this flag or env var "+dcos.EnvMongoDBChangeUserDb+" is required",
	).Envar(dcos.EnvMongoDBChangeUserDb).Required().StringVar(&cnf.User.Database)
}

func handleFailed(err error) {
	log.Fatalf("Failed with error: %s", err)
	os.Exit(1)
}

func main() {
	app, _ := tool.New("Performs administrative tasks for MongoDB on behalf of DC/OS", GitCommit, GitBranch)

	cnf := &controller.Config{
		ReplsetInit: &controller.ConfigReplsetInit{},
		User: &controller.ConfigUser{
			API: &api.Config{},
		},
	}

	app.Flag(
		"service",
		"DC/OS SDK service/framework name, overridden by env var "+pkg.EnvServiceName,
	).Default(pkg.DefaultServiceName).Envar(pkg.EnvServiceName).StringVar(&cnf.ServiceName)
	app.Flag(
		"replset",
		"mongodb replica set name, this flag or env var "+pkg.EnvMongoDBReplset+" is required",
	).Envar(pkg.EnvMongoDBReplset).Required().StringVar(&cnf.Replset)
	app.Flag(
		"userAdminUser",
		"mongodb userAdmin username, overridden by env var "+pkg.EnvMongoDBUserAdminUser,
	).Envar(pkg.EnvMongoDBUserAdminUser).Required().StringVar(&cnf.UserAdminUser)
	app.Flag(
		"userAdminPassword",
		"mongodb userAdmin password or path to file containing it, overridden by env var "+pkg.EnvMongoDBUserAdminPassword,
	).Envar(pkg.EnvMongoDBUserAdminPassword).Required().StringVar(&cnf.UserAdminPassword)
	app.Flag(
		"enableSecrets",
		"enable DC/OS Secrets, this causes passwords to be loaded from files, overridden by env var "+dcos.EnvSecretsEnabled,
	).Envar(dcos.EnvSecretsEnabled).BoolVar(&enableSecrets)

	cnf.SSL = db.NewSSLConfig(app)

	handleReplsetCmd(app, cnf)
	handleUserCmd(app, cnf)

	command, err := app.Parse(os.Args[1:])
	if err != nil {
		log.Fatalf("Cannot parse command line: %s", err)
	}
	if enableSecrets {
		cnf.UserAdminUser = internal.PasswordFromFile(
			os.Getenv(dcos.EnvMesosSandbox),
			cnf.UserAdminUser,
			"userAdmin",
		)
	}

	switch command {
	case cmdInit.FullCommand():
		err := replset.NewInitiator(cnf).Run()
		if err != nil {
			handleFailed(err)
		}
	case cmdUserUpdate.FullCommand():
		uc, err := user.NewController(cnf, api.New(cnf.ServiceName, cnf.User.API))
		if err != nil {
			handleFailed(err)
		}
		defer uc.Close()

		err = uc.UpdateUsers()
		if err != nil {
			handleFailed(err)
		}
	case cmdUserRemove.FullCommand():
		uc, err := user.NewController(cnf, api.New(cnf.ServiceName, cnf.User.API))
		if err != nil {
			handleFailed(err)
		}
		defer uc.Close()

		err = uc.RemoveUser()
		if err != nil {
			handleFailed(err)
		}
	case cmdUserReloadSys.FullCommand():
		uc, err := user.NewController(cnf, api.New(cnf.ServiceName, cnf.User.API))
		if err != nil {
			handleFailed(err)
		}
		defer uc.Close()

		err = uc.ReloadSystemUsers()
		if err != nil {
			handleFailed(err)
		}
	}
}
