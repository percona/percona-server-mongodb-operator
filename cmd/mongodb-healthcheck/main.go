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
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	uzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/db"
	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/healthcheck"
	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/tool"
)

var (
	GitCommit string
	GitBranch string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	app := tool.New("Performs health and readiness checks for MongoDB", GitCommit, GitBranch)

	k8sCmd := app.Command("k8s", "Performs liveness check for MongoDB on Kubernetes")
	livenessCmd := k8sCmd.Command("liveness", "Run a liveness check of MongoDB").Default()
	readinessCmd := k8sCmd.Command("readiness", "Run a readiness check of MongoDB")
	startupDelaySeconds := livenessCmd.Flag("startupDelaySeconds", "").Default("7200").Uint64()
	component := k8sCmd.Flag("component", "").Default("mongod").String()

	opts := zap.Options{
		Encoder: getLogEncoder(),
		Level:   getLogLevel(),
	}
	log := zap.New(zap.UseFlagOptions(&opts))
	logf.SetLogger(log)

	restoreInProgress, err := fileExists("/opt/percona/restore-in-progress")
	if err != nil {
		log.Error(err, "check if restore in progress")
		os.Exit(1)
	}

	if restoreInProgress {
		os.Exit(0)
	}

	sleepForever, err := fileExists("/data/db/sleep-forever")
	if err != nil {
		log.Error(err, "check if sleep-forever file exists")
		os.Exit(1)
	}
	if sleepForever {
		os.Exit(0)
	}

	cnf, err := db.NewConfig(
		app,
		tool.EnvMongoDBClusterMonitorUser,
		tool.EnvMongoDBClusterMonitorPassword,
	)
	if err != nil {
		log.Error(err, "new cfg")
		os.Exit(1)
	}

	command, err := app.Parse(os.Args[1:])
	if err != nil {
		log.Error(err, "Cannot parse command line")
		os.Exit(1)
	}

	switch command {
	case livenessCmd.FullCommand():
		log.Info("Running Kubernetes liveness check for", "component", component)
		switch *component {

		case "mongod":
			memberState, err := healthcheck.HealthCheckMongodLiveness(ctx, cnf, int64(*startupDelaySeconds))
			if err != nil {
				log.Error(err, "Member failed Kubernetes liveness check")
				os.Exit(1)
			}
			log.Info("Member passed Kubernetes liveness check with replication state", "state", memberState)

		case "mongos":
			err := healthcheck.HealthCheckMongosLiveness(ctx, cnf)
			if err != nil {
				log.Error(err, "Member failed Kubernetes liveness check")
				os.Exit(1)
			}
			log.Info("Member passed Kubernetes liveness check")
		}

	case readinessCmd.FullCommand():
		log.Info("Running Kubernetes readiness check for component", "component", component)
		switch *component {

		case "mongod":
			err := healthcheck.MongodReadinessCheck(ctx, cnf.Hosts[0])
			if err != nil {
				log.Error(err, "Member failed Kubernetes readiness check")
				os.Exit(1)
			}
		case "mongos":
			err := healthcheck.MongosReadinessCheck(ctx, cnf)
			if err != nil {
				log.Error(err, "Member failed Kubernetes readiness check")
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

func getLogEncoder() zapcore.Encoder {
	consoleEnc := zapcore.NewConsoleEncoder(uzap.NewDevelopmentEncoderConfig())

	s, found := os.LookupEnv("LOG_STRUCTURED")
	if !found {
		return consoleEnc
	}

	useJSON, err := strconv.ParseBool(s)
	if err != nil {
		return consoleEnc
	}
	if !useJSON {
		return consoleEnc
	}

	return zapcore.NewJSONEncoder(uzap.NewProductionEncoderConfig())
}

func getLogLevel() zapcore.LevelEnabler {
	l, found := os.LookupEnv("LOG_LEVEL")
	if !found {
		return zapcore.InfoLevel
	}

	switch strings.ToUpper(l) {
	case "DEBUG":
		return zapcore.DebugLevel
	case "INFO":
		return zapcore.InfoLevel
	case "ERROR":
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}
