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
	"syscall"

	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/logger"
	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/tool"
)

var (
	GitCommit string
	GitBranch string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	log := logger.New()
	logf.SetLogger(log.Logger)
	ctx = logf.IntoContext(ctx, log.Logger)

	log.Info("Running mongodb-healthcheck", "commit", GitCommit, "branch", GitBranch)

	app := tool.New("Performs health and readiness checks for MongoDB", GitCommit, GitBranch)
	if err := app.Run(ctx); err != nil {
		log.Error(err, "Failed to perform check")
		if err := log.Rotate(); err != nil {
			log.Error(err, "failed to rotate logs")
		}
		_ = log.Close()
		os.Exit(1)
	}
	_ = log.Close()
}
