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
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	uzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/tool"
)

var (
	GitCommit string
	GitBranch string
)

func main() {
	opts := zap.Options{
		Encoder:    getLogEncoder(),
		Level:      getLogLevel(),
		DestWriter: getLogWriter(),
	}

	log := zap.New(zap.UseFlagOptions(&opts))
	logf.SetLogger(log)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	logf.IntoContext(ctx, log)

	app := tool.New("Performs health and readiness checks for MongoDB", GitCommit, GitBranch)
	if err := app.Run(ctx); err != nil {
		msg := strings.TrimSuffix(err.Error(), errors.Unwrap(err).Error())
		msg = strings.ToTitle(msg)
		log.Error(errors.Unwrap(err), msg)

		stop()
		os.Exit(1)
	}
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

func getLogWriter() io.Writer {
	var lw io.Writer
	lw = os.Stderr
	lf := createLogFile()
	if lf != nil {
		lw = io.MultiWriter(lw, lf)
	}
	return lw
}

func createLogFile() *os.File {
	log := logf.Log

	d, found := os.LookupEnv("LOG_DIR")
	if !found {
		return nil
	}

	if err := os.MkdirAll(d, 0755); err != nil {
		log.Error(err, "Failed to create $LOG_DIR directory")
		os.Exit(1)
	}

	timestamp := strconv.FormatInt(time.Now().UTC().UnixMilli(), 10)
	fp := filepath.Join(d, "healthcheck-"+timestamp+".log")

	f, err := os.Create(fp)
	if err != nil {
		log.Error(err, "Failed to create log file", "file path", fp)
		os.Exit(1)
	}

	return f
}
