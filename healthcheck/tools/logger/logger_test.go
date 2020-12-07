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
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/alecthomas/kingpin"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func TestInternalLoggerSetupLogger(t *testing.T) {
	assert.Equal(t, log.InfoLevel, log.GetLevel(), "logrus.GetLevel() should return info level")
	formatter := GetLogFormatter()

	// nil *kingpin.Application
	assert.Nil(t, SetupLogger(nil, formatter, os.Stdout))

	assert.False(t, *SetupLogger(kingpin.New(t.Name(), "test"), formatter, os.Stdout))
	assert.Equal(t, formatter, formatter, "logrus.StandardLogger().Formatter is incorrect")
}

func TestInternalLoggerLogDebug(t *testing.T) {
	os.Unsetenv(pkg.EnvLogVerbose)
	defer os.Unsetenv(pkg.EnvLogVerbose)

	formatter := GetLogFormatter()
	debugStr := strings.ToUpper(log.DebugLevel.String())

	// test --verbose flag
	buf1 := new(bytes.Buffer)
	app1 := kingpin.New(t.Name(), t.Name())
	verbose1 := SetupLogger(app1, formatter, buf1)
	assert.False(t, *verbose1)
	_, err1 := app1.Parse([]string{"--verbose"})
	assert.NoError(t, err1)
	assert.True(t, *verbose1)

	log.Debug("test123")
	logged1 := buf1.String()
	assert.Contains(t, strings.TrimSpace(logged1), "logger_test.go:56 "+debugStr+"  test123", ".Debug() log output unexpected")

	// test verbose env var
	buf2 := new(bytes.Buffer)
	app2 := kingpin.New(t.Name(), t.Name())
	os.Setenv(pkg.EnvLogVerbose, "true")
	verbose2 := SetupLogger(app2, formatter, buf2)
	_, err2 := app2.Parse([]string{})
	assert.NoError(t, err2)
	assert.True(t, *verbose2)
}

func TestInternalLoggerLogInfo(t *testing.T) {
	buf := new(bytes.Buffer)
	formatter := GetLogFormatter()
	assert.Nil(t, SetupLogger(nil, formatter, buf))
	log.Info("test123")

	infoStr := strings.ToUpper(log.InfoLevel.String())
	logged := buf.String()
	assert.Contains(t, strings.TrimSpace(logged), "logger_test.go:74 "+infoStr+"   test123", ".Info() log output unexpected")
}
