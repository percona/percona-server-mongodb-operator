package logger

import (
	"fmt"
	"io"
	"log/syslog"
	"os"

	"github.com/sirupsen/logrus"
	lSyslog "github.com/sirupsen/logrus/hooks/syslog"
)

func NewDefaultLogger(filename string) *logrus.Logger {
	if filename == "" {
		return newDefaultLogger(os.Stderr)
	}

	fh, err := os.Create(filename)
	if err != nil {
		fmt.Printf("Cannot create lof file %s: %s", filename, err)
		fmt.Println("Using Stderr for error output")
		return newDefaultLogger(os.Stderr)
	}

	return newDefaultLogger(fh)
}

func NewSyslogLogger() *logrus.Logger {
	w, err := os.Create(os.DevNull)
	if err != nil {
		w = os.Stderr
	}

	log := newDefaultLogger(w)
	if hook, err := lSyslog.NewSyslogHook("", "", syslog.LOG_INFO, ""); err != nil {
		fmt.Printf("Cannot hook into local syslog: %s", err)
	} else {
		log.AddHook(hook)
	}
	return log
}

func newDefaultLogger(w io.Writer) *logrus.Logger {
	logger := &logrus.Logger{
		Out: w,
		Formatter: &logrus.TextFormatter{
			FullTimestamp:          true,
			DisableLevelTruncation: true,
		},
		Hooks: make(logrus.LevelHooks),
		Level: logrus.InfoLevel,
	}
	logger.SetLevel(logrus.StandardLogger().Level)
	logger.Out = logrus.StandardLogger().Out

	return logger
}

// MgoLogger matches the mgo.logLogger interface
// https://github.com/globalsign/mgo/blob/master/log.go#L40
type MgoLogger struct{}

// Output logs an Mgo debug message using logrus
func (ml *MgoLogger) Output(calldepth int, s string) error {
	logrus.WithFields(logrus.Fields{
		"calldepth": calldepth,
	}).Debugf("mgo: %s", s)
	return nil
}
