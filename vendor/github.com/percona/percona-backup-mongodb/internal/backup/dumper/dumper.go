package dumper

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mongodb/mongo-tools-common/log"
	"github.com/mongodb/mongo-tools/common/options"
	"github.com/mongodb/mongo-tools/common/progress"
	"github.com/mongodb/mongo-tools/mongodump"
	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"
)

const (
	progressBarLength   = 24
	progressBarWaitTime = time.Second * 3
)

type MongodumpInput struct {
	Archive  string
	Host     string
	Port     string
	Username string
	Password string
	AuthDB   string
	Gzip     bool
	Oplog    bool
	Threads  int
	Writer   io.WriteCloser
}

type Mongodump struct {
	*MongodumpInput
	mongodump *mongodump.MongoDump

	lastError error
	waitChan  chan error
	//
	lock    *sync.Mutex
	running bool

	mdumpLogger []byte
}

type log2LogrusWriter struct {
	entry *logrus.Entry
}

func (w *log2LogrusWriter) Write(b []byte) (int, error) {
	n := len(b)
	if n > 0 && b[n-1] == '\n' {
		b = b[:n-1]
	}
	w.entry.Info(string(b))
	return n, nil
}

func NewMongodump(i *MongodumpInput) (*Mongodump, error) {
	if i.Writer == nil {
		return nil, fmt.Errorf("Writer cannot be null")
	}

	// TODO: SSL?
	connOpts := &options.Connection{
		Host: i.Host,
		Port: i.Port,
	}

	toolOpts := &options.ToolOptions{
		AppName:    "mongodump",
		VersionStr: "0.0.1",
		Connection: connOpts,
		Auth:       &options.Auth{},
		Namespace:  &options.Namespace{},
	}
	if i.Username != "" && i.Password != "" {
		toolOpts.Auth.Username = i.Username
		toolOpts.Auth.Password = i.Password
		if i.AuthDB != "" {
			toolOpts.Auth.Source = i.AuthDB
		}
	}

	outputOpts := &mongodump.OutputOptions{
		// Archive = "-" means, for mongodump, use the provider Writer
		// instead of creating a file. This is not clear at plain sight,
		// you nee to look the code to discover it.
		Archive:                "-",
		Gzip:                   i.Gzip,
		Oplog:                  i.Oplog,
		NumParallelCollections: i.Threads,
	}

	dump := &mongodump.MongoDump{
		ToolOptions:   toolOpts,
		OutputOptions: outputOpts,
		InputOptions:  &mongodump.InputOptions{},
	}

	if err := dump.ValidateOptions(); err != nil {
		return nil, err
	}

	dump.OutputWriter = i.Writer

	mongoDump := &Mongodump{
		MongodumpInput: i,
		mongodump:      dump,
		lock:           &sync.Mutex{},
		running:        false,
	}
	//mdumpLogger.SetWriter(logrus.StandardLogger().Writer())

	return mongoDump, nil
}

func (md *Mongodump) LastError() error {
	return md.lastError
}

func (md *Mongodump) Start() error {
	if md.isRunning() {
		return fmt.Errorf("Dumper already running")
	}
	md.waitChan = make(chan error, 1)
	if err := md.mongodump.Init(); err != nil {
		return errors.Wrapf(err, "cannot start mongodump")
	}
	md.setRunning(true)
	go md.dump()

	return nil
}

func (md *Mongodump) Stop() error {
	if !md.isRunning() {
		return fmt.Errorf("The dumper is not running")
	}
	md.mongodump.HandleInterrupt()
	return md.Wait()
}

func (md *Mongodump) Wait() error {
	if !md.isRunning() {
		return fmt.Errorf("The dumper is not running")
	}
	md.setRunning(false)
	defer close(md.waitChan)
	md.lastError = <-md.waitChan
	return md.lastError
}

func (md *Mongodump) dump() {
	progressManager := progress.NewBarWriter(log.Writer(0), progressBarWaitTime, progressBarLength, false)
	md.mongodump.ProgressManager = progressManager
	progressManager.Start()
	defer progressManager.Stop()

	err := md.mongodump.Dump()
	md.waitChan <- err
}

func (md *Mongodump) isRunning() bool {
	md.lock.Lock()
	defer md.lock.Unlock()
	return md.running
}

func (md *Mongodump) setRunning(status bool) {
	md.lock.Lock()
	defer md.lock.Unlock()
	md.running = status
}
