package hotbackup

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/otiai10/copy"
	flock "github.com/theckman/go-flock"
)

var (
	RestoreStopServerRetries = 60 * 4
	RestoreStopServerWait    = 250 * time.Millisecond
)

func getOwnerUid(path string) (*uint32, error) {
	fi, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil, err
	}
	uid := fi.Sys().(*syscall.Stat_t).Uid
	return &uid, nil
}

type Restore struct {
	backupPath     string
	dbPath         string
	session        *mgo.Session
	serverAddr     string
	serverArgv     []string
	serverShutdown bool
	lockFile       string
	lock           *flock.Flock
	uid            *uint32
	moveBackup     bool
	restored       bool
}

func NewRestore(session *mgo.Session, backupPath, dbPath string) (*Restore, error) {
	lockFile := filepath.Join(dbPath, "mongod.lock")
	uid, err := getOwnerUid(lockFile)
	return &Restore{
		backupPath: backupPath,
		dbPath:     dbPath,
		lockFile:   lockFile,
		session:    session,
		serverAddr: session.LiveServers()[0],
		uid:        uid,
	}, err
}

func (r *Restore) Close() {
	if r.lock != nil && r.lock.Locked() {
		r.lock.Unlock()
	}
}

func (r *Restore) checkBackupPath() error {
	wtBackupFile := filepath.Join(r.backupPath, "WiredTiger.backup")
	if _, err := os.Stat(wtBackupFile); os.IsNotExist(err) {
		return errors.New("could not find WiredTiger.backup file")
	}
	return nil
}

func (r *Restore) isServerRunning() (bool, error) {
	if r.session != nil && r.session.Ping() == nil {
		return true, nil
	} else if r.lock == nil {
		r.lock = flock.NewFlock(r.lockFile)
	}
	return r.lock.Locked(), nil
}

func (r *Restore) getServerCmdLine() ([]string, error) {
	resp := struct {
		Argv []string `bson:"argv"`
	}{}
	err := r.session.Run(bson.D{{"getCmdLineOpts", 1}}, &resp)
	return resp.Argv, err
}

func (r *Restore) waitForShutdown() error {
	tries := 0
	ticker := time.NewTicker(RestoreStopServerWait)
	for tries < RestoreStopServerRetries {
		select {
		case <-ticker.C:
			running, err := r.isServerRunning()
			if err != nil {
				return err
			} else if !running {
				ticker.Stop()
				r.serverShutdown = true
				return nil
			}
			tries++
		}
	}
	ticker.Stop()
	return errors.New("server did not stop")
}

func (r *Restore) stopServer() error {
	running, err := r.isServerRunning()
	if err != nil {
		return err
	} else if !running {
		return nil
	}

	// get running server command-line
	r.serverArgv, err = r.getServerCmdLine()
	if err != nil {
		return err
	}

	// shutdown the running server, expect 'EOF' error from mongo session
	err = r.session.Run(bson.D{{"shutdown", 1}}, nil)
	if err == nil {
		return errors.New("no EOF from server")
	} else if err.Error() != "EOF" {
		return err
	}
	r.session.Close()
	r.session = nil

	return r.waitForShutdown()
}

func (r *Restore) getLock() error {
	if r.lock == nil {
		r.lock = flock.NewFlock(r.lockFile)
	}

	err := r.lock.Lock()
	if err != nil {
		return err
	}
	return nil
}

func (r *Restore) restoreDBPath() error {
	err := r.checkBackupPath()
	if err != nil {
		return err
	}

	// move backup to dbpath if enabled
	// or copy if not
	backupUid, err := getOwnerUid(filepath.Join(r.backupPath, "storage.bson"))
	if err != nil {
		return err
	} else if backupUid != nil && *backupUid != *r.uid {
		return errors.New("uids do not match")
	}

	err = r.getLock()
	if err != nil {
		return err
	}
	defer r.lock.Unlock()

	if r.moveBackup {
		err = os.Rename(r.backupPath, r.dbPath)
	} else {
		err = copy.Copy(r.backupPath, r.dbPath)
	}
	if err != nil {
		return err
	}

	r.restored = true
	return nil
}

func (r *Restore) startServer() error {
	// #nosec G204
	mongod := exec.Command(r.serverArgv[0], r.serverArgv[1:]...)
	err := mongod.Start()
	if err != nil {
		return err
	}

	var tries int
	for tries <= 120 {
		var err error
		r.session, err = mgo.Dial(r.serverAddr)
		if err != nil {
			continue
		}
		if r.session.Ping() == nil {
			return nil
		}
		r.session.Close()
		time.Sleep(500 * time.Millisecond)
		tries++
	}
	return errors.New("could not start server")
}
