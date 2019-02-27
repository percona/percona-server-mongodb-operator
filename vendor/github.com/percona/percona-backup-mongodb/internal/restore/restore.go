package restore

import (
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mongodb/mongo-tools-common/log"
	"github.com/mongodb/mongo-tools/common/db"
	"github.com/mongodb/mongo-tools/common/options"
	"github.com/mongodb/mongo-tools/common/progress"
	"github.com/mongodb/mongo-tools/mongorestore"
	"github.com/pkg/errors"
)

const (
	progressBarLength   = 24
	progressBarWaitTime = time.Second * 3
)

type MongoRestoreInput struct {
	Archive           string
	DryRun            bool // Used only for testing
	Host              string
	Port              string
	Username          string
	Password          string
	AuthDB            string
	Gzip              bool
	Oplog             bool
	Threads           int
	SkipUsersAndRoles bool

	Reader io.ReadCloser
}

type MongoRestore struct {
	*MongoRestoreInput
	mongorestore *mongorestore.MongoRestore

	lastError error
	waitChan  chan error
	//
	lock    *sync.Mutex
	running bool
}

func NewMongoRestore(i *MongoRestoreInput) (*MongoRestore, error) {
	if i.Reader == nil && i.Archive == "" {
		return nil, fmt.Errorf("You need to specify an archive or a reader")
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
		URI:        &options.URI{},
		Direct:     true,
	}
	if i.Username != "" && i.Password != "" {
		toolOpts.Auth.Username = i.Username
		toolOpts.Auth.Password = i.Password
		if i.AuthDB != "" {
			toolOpts.Auth.Source = i.AuthDB
		}
	}

	inputOpts := &mongorestore.InputOptions{
		Gzip:                   i.Gzip,
		Archive:                i.Archive,
		Objcheck:               false,
		RestoreDBUsersAndRoles: false,
		OplogReplay:            false,
	}

	outputOpts := &mongorestore.OutputOptions{
		BulkBufferSize:           2000,
		BypassDocumentValidation: true,
		Drop:                     true,
		DryRun:                   false,
		KeepIndexVersion:         false,
		NoIndexRestore:           false,
		NoOptionsRestore:         false,
		NumInsertionWorkers:      20,
		NumParallelCollections:   4,
		StopOnError:              false,
		TempRolesColl:            "temproles",
		TempUsersColl:            "tempusers",
		WriteConcern:             "majority",
	}

	provider, err := db.NewSessionProvider(*toolOpts)
	if err != nil {
		return nil, errors.Wrap(err, "cannot instantiate a session provider")
	}
	if provider == nil {
		return nil, fmt.Errorf("Cannot set session provider (nil)")
	}

	progressManager := progress.NewBarWriter(log.Writer(0), progressBarWaitTime, progressBarLength, true)

	restore := &mongorestore.MongoRestore{
		ToolOptions:       toolOpts,
		OutputOptions:     outputOpts,
		InputOptions:      inputOpts,
		NSOptions:         &mongorestore.NSOptions{},
		SkipUsersAndRoles: i.SkipUsersAndRoles,
		SessionProvider:   provider,
		InputReader:       i.Reader,
		ProgressManager:   progressManager,
	}

	if err := restore.ParseAndValidateOptions(); err != nil {
		return nil, err
	}

	return &MongoRestore{
		MongoRestoreInput: i,
		mongorestore:      restore,
		lock:              &sync.Mutex{},
		running:           false,
	}, nil
}

func (mr *MongoRestore) LastError() error {
	return mr.lastError
}

func (mr *MongoRestore) Start() error {
	if mr.isRunning() {
		return fmt.Errorf("Dumper already running")
	}
	mr.waitChan = make(chan error, 1)
	mr.setRunning(true)
	go mr.restore()

	return nil
}

func (mr *MongoRestore) Stop() error {
	if !mr.isRunning() {
		return fmt.Errorf("The dumper is not running")
	}
	mr.mongorestore.HandleInterrupt()
	return mr.Wait()
}

func (mr *MongoRestore) Wait() error {
	if !mr.isRunning() {
		return fmt.Errorf("The dumper is not running")
	}
	mr.setRunning(false)
	defer close(mr.waitChan)
	mr.lastError = <-mr.waitChan
	return mr.lastError
}

func (mr *MongoRestore) restore() {
	mr.mongorestore.ProgressManager.(*progress.BarWriter).Start()
	defer mr.mongorestore.ProgressManager.(*progress.BarWriter).Stop()
	err := mr.mongorestore.Restore()
	mr.waitChan <- err
}

func (mr *MongoRestore) isRunning() bool {
	mr.lock.Lock()
	defer mr.lock.Unlock()
	return mr.running
}

func (mr *MongoRestore) setRunning(status bool) {
	mr.lock.Lock()
	defer mr.lock.Unlock()
	mr.running = status
}
