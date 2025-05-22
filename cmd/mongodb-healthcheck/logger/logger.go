package logger

import (
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	uzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

type Logger struct {
	logRotateWriter *lumberjack.Logger
	logr.Logger
}

func New() *Logger {
	logPath := filepath.Join(config.MongodDataVolClaimName, "logs", "mongodb-healthcheck.log")

	return newLogger(logPath)
}

func newLogger(logPath string) *Logger {
	logRotateWriter := &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    100,
		MaxAge:     1,
		MaxBackups: 0,
		Compress:   true,
	}

	zapOpts := zap.Options{
		Encoder:    getLogEncoder(),
		Level:      getLogLevel(),
		DestWriter: io.MultiWriter(os.Stderr, logRotateWriter),
	}

	return &Logger{
		logRotateWriter: logRotateWriter,
		Logger:          zap.New(zap.UseFlagOptions(&zapOpts)),
	}
}

func (l *Logger) Rotate() error {
	return l.logRotateWriter.Rotate()
}

func (l *Logger) Close() error {
	return l.logRotateWriter.Close()
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
