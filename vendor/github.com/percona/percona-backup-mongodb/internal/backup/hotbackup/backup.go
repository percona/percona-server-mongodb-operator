package hotbackup

import (
	"os"

	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
)

const (
	ErrMsgUnsupportedEngine = "This engine doesn't support hot backup."
)

type Backup struct {
	dir     string
	removed bool
}

// NewBackup creates a Percona Server for MongoDB Hot Backup and outputs
// it to the specified backup directory. The provided MongoDB session
// must be a direct connection to localhost/127.0.0.1
//
// https://www.percona.com/doc/percona-server-for-mongodb/LATEST/hot-backup.html
//
func NewBackup(session *mgo.Session, backupDir string) (*Backup, error) {
	err := checkLocalhostSession(session)
	if err != nil {
		return nil, err
	}

	err = checkHotBackup(session)
	if err != nil {
		return nil, err
	}

	b := Backup{dir: backupDir}
	err = session.Run(bson.D{{"createBackup", 1}, {"backupDir", b.dir}}, nil)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// Dir returns the path to the Hot Backup directory
func (b *Backup) Dir() string {
	return b.dir
}

// Remove removes the Hot Backup directory and data
func (b *Backup) Remove() error {
	if b.removed {
		return nil
	}
	err := os.RemoveAll(b.dir)
	if err != nil {
		return err
	}
	b.dir = ""
	b.removed = true
	return nil
}

// Close cleans-up and removes the Hot Backup
func (b *Backup) Close() {
	_ = b.Remove()
}
