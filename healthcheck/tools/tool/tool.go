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
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/alecthomas/kingpin"
	tools "github.com/percona/percona-server-mongodb-operator/healthcheck"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/logger"
)

// Author is the author used by kingpin
const Author = "Percona LLC."

// New sets up a new kingpin.Application
func New(help, commit, branch string) (*kingpin.Application, *bool) {
	app := kingpin.New(filepath.Base(os.Args[0]), help)
	app.Author(Author)
	app.Version(fmt.Sprintf(
		"%s version %s\ngit commit %s, branch %s\ngo version %s",
		app.Name, tools.Version, commit, branch, runtime.Version(),
	))
	return app, logger.SetupLogger(
		app,
		logger.GetLogFormatter(),
		os.Stdout,
	)
}
