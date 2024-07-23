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
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	uzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/tool"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

var (
	GitCommit string
	GitBranch string
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	logPath := filepath.Join(psmdb.MongodDataVolClaimName, "logs", "mongodb-healthcheck.log")
	logRotateWriter := lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    100,
		MaxAge:     1,
		MaxBackups: 0,
		Compress:   true,
	}
	logOpts := zap.Options{
		Encoder:    getLogEncoder(),
		Level:      getLogLevel(),
		DestWriter: io.MultiWriter(os.Stderr, &logRotateWriter),
	}

	log := zap.New(zap.UseFlagOptions(&logOpts))
	logf.SetLogger(log)
	ctx = logf.IntoContext(ctx, log)

	log.Info("Running mongodb-healthcheck", "commit", GitCommit, "branch", GitBranch)

	app := tool.New("Performs health and readiness checks for MongoDB", GitCommit, GitBranch)
	if err := app.Run(ctx); err != nil {
		log.Error(err, "Failed to perform check")
		if err := logRotateWriter.Rotate(); err != nil {
			log.Error(err, "failed to rotate logs")
		}
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
	defaultLogLevel := zapcore.DebugLevel

	l, found := os.LookupEnv("LOG_LEVEL")
	if !found {
		return defaultLogLevel
	}

	switch strings.ToUpper(l) {
	case "DEBUG":
		return zapcore.DebugLevel
	case "INFO":
		return zapcore.InfoLevel
	case "ERROR":
		return zapcore.ErrorLevel
	default:
		return defaultLogLevel
	}
}
