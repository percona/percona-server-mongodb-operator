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

package logger

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/alecthomas/kingpin"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	log "github.com/sirupsen/logrus"
)

// enableVerboseLogging enables verbose logging
func enableVerboseLogging(ctx *kingpin.ParseContext) error {
	log.SetLevel(log.DebugLevel)
	return nil
}

// getCallerInfo returns the file and file line-number of a caller
func getLogCallerInfo(e *log.Entry) (interface{}, error) {
	skip := 1
	skipMax := 12
	for skip <= skipMax {
		_, file, lineNo, _ := runtime.Caller(skip)
		if strings.Contains(file, "github.com/sirupsen/logrus") {
			skip++
			continue
		}
		return filepath.Base(file) + ":" + strconv.Itoa(lineNo), nil
	}
	return "", nil
}

// GetLogFormatter returns a configured logrus.Formatter for logging
func GetLogFormatter() log.Formatter {
	return &log.JSONFormatter{}
}

// SetupLogger configures github.com/srupsen/logrus for logging
func SetupLogger(app *kingpin.Application, formatter log.Formatter, out io.Writer) *bool {
	log.SetOutput(out)
	log.SetFormatter(formatter)
	log.SetLevel(log.InfoLevel)
	if app != nil {
		var verbose bool
		app.Flag("verbose", "enable verbose logging").Action(enableVerboseLogging).BoolVar(&verbose)

		// fix for kingpin .Envar() being ignored above
		if strings.TrimSpace(os.Getenv(pkg.EnvLogVerbose)) == "true" {
			_ = enableVerboseLogging(nil)
			verbose = true
		}

		return &verbose
	}
	return nil
}
