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
	Archive  string
	Host     string
	Port     string
	Username string
	Password string
	AuthDB   string
	Threads  int
	DryRun   bool // Used only for testing
	// There is an error in MongoDB/MongoRestore when trying to restore the config servers.
	// Mongo Restore throws:  cannot drop config.version document while in --configsvr mode"
	// See: https://jira.mongodb.org/browse/SERVER-28796
	DropCollections bool
	// This field is not StopOnError as we should send it to MongoRestore, because the default
	// we want is true so, just to prevent forgetting setting it on the struct, we are going to
	// use the opposite.
	PreserveUUID      bool
	IgnoreErrors      bool
	Gzip              bool
	Oplog             bool
	SkipUsersAndRoles bool

	Reader io.ReadCloser
}

type MongoRestore struct {
	*MongoRestoreInput
	mongorestore *mongorestore.MongoRestore

	lastError error
	waitChan  chan error

	lock    *sync.Mutex
	running bool
}

func NewMongoRestore(i *MongoRestoreInput) (*MongoRestore, error) {
	if i.Reader == nil && i.Archive == "" {
		return nil, fmt.Errorf("you need to specify an archive or a reader")
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
		Drop:                     i.DropCollections,
		DryRun:                   false,
		KeepIndexVersion:         false,
		NoIndexRestore:           false,
		NoOptionsRestore:         false,
		NumInsertionWorkers:      20,
		NumParallelCollections:   4,
		PreserveUUID:             i.PreserveUUID,
		StopOnError:              !i.IgnoreErrors,
		TempRolesColl:            "temproles",
		TempUsersColl:            "tempusers",
		WriteConcern:             "majority",
	}

	provider, err := db.NewSessionProvider(*toolOpts)
	if err != nil {
		return nil, errors.Wrap(err, "cannot instantiate a session provider")
	}
	if provider == nil {
		return nil, fmt.Errorf("cannot set session provider (nil)")
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
		return fmt.Errorf("a dumper is already running")
	}
	mr.waitChan = make(chan error, 1)
	mr.setRunning(true)
	go mr.restore()

	return nil
}

func (mr *MongoRestore) Stop() error {
	if !mr.isRunning() {
		return fmt.Errorf("the dumper is not running")
	}
	mr.mongorestore.HandleInterrupt()
	return mr.Wait()
}

func (mr *MongoRestore) Wait() error {
	if !mr.isRunning() {
		fmt.Println("mongo restore is no running")
		return fmt.Errorf("the dumper is not running")
	}
	mr.setRunning(false)
	defer close(mr.waitChan)
	mr.lastError = <-mr.waitChan
	return mr.lastError
}

func (mr *MongoRestore) restore() {
	fmt.Println("Starting mongo restore progress bar")
	mr.mongorestore.ProgressManager.(*progress.BarWriter).Start()
	defer mr.mongorestore.ProgressManager.(*progress.BarWriter).Stop()
	fmt.Println("calling mr.mongorestore.Restore()")
	err := mr.mongorestore.Restore()
	mr.waitChan <- err
}

func (mr *MongoRestore) isRunning() bool {
	fmt.Println("isRunning lock")
	mr.lock.Lock()
	defer mr.lock.Unlock()
	fmt.Println("isRunning unlock")
	return mr.running
}

func (mr *MongoRestore) setRunning(status bool) {
	fmt.Println("setRunning lock")
	mr.lock.Lock()
	defer mr.lock.Unlock()
	fmt.Println("setRunning unlock")
	mr.running = status
}
