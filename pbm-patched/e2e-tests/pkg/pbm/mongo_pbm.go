package pbm

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/resync"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

type MongoPBM struct {
	conn connect.Client
}

func NewMongoPBM(ctx context.Context, connectionURI string) (*MongoPBM, error) {
	conn, err := connect.Connect(ctx, connectionURI, "e2e-tests-pbm")
	if err != nil {
		return nil, errors.Wrap(err, "connect")
	}

	return &MongoPBM{conn: conn}, nil
}

func (m *MongoPBM) SendCmd(ctx context.Context, cmd ctrl.Cmd) error {
	cmd.TS = time.Now().UTC().Unix()
	_, err := m.conn.CmdStreamCollection().InsertOne(ctx, cmd)
	return err
}

func (m *MongoPBM) BackupsList(ctx context.Context, limit int64) ([]backup.BackupMeta, error) {
	return backup.BackupsList(ctx, m.conn, limit)
}

func (m *MongoPBM) GetBackupMeta(ctx context.Context, bcpName string) (*backup.BackupMeta, error) {
	return backup.NewDBManager(m.conn).GetBackupByName(ctx, bcpName)
}

func (m *MongoPBM) DeleteBackup(ctx context.Context, bcpName string) error {
	l := log.FromContext(ctx).
		NewEvent(string(ctrl.CmdDeleteBackup), "", "", primitive.Timestamp{})
	ctx = log.SetLogEventToContext(ctx, l)
	return backup.DeleteBackup(ctx, m.conn, bcpName, "")
}

func (m *MongoPBM) Storage(ctx context.Context) (storage.Storage, error) {
	l := log.FromContext(ctx).
		NewEvent("", "", "", primitive.Timestamp{})
	return util.GetStorage(ctx, m.conn, "", l)
}

func (m *MongoPBM) StoreResync(ctx context.Context) error {
	l := log.FromContext(ctx).
		NewEvent(string(ctrl.CmdResync), "", "", primitive.Timestamp{})
	ctx = log.SetLogEventToContext(ctx, l)

	cfg, err := config.GetConfig(ctx, m.conn)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	return resync.Resync(ctx, m.conn, &cfg.Storage, "", false)
}

func (m *MongoPBM) Conn() connect.Client {
	return m.conn
}

// WaitOp waits up to waitFor duration until operations which acquires a given lock are finished
func (m *MongoPBM) WaitOp(ctx context.Context, lck *lock.LockHeader, waitFor time.Duration) error {
	return m.waitOp(ctx, lck, waitFor, lock.GetLockData)
}

// WaitConcurentOp waits up to waitFor duration until operations which acquires a given lock are finished
func (m *MongoPBM) WaitConcurentOp(ctx context.Context, lck *lock.LockHeader, waitFor time.Duration) error {
	return m.waitOp(ctx, lck, waitFor, lock.GetOpLockData)
}

// WaitOp waits up to waitFor duration until operations which acquires a given lock are finished
func (m *MongoPBM) waitOp(
	ctx context.Context,
	lck *lock.LockHeader,
	waitFor time.Duration,
	f func(ctx context.Context, conn connect.Client, lh *lock.LockHeader) (lock.LockData, error),
) error {
	// just to be sure the check hasn't started before the lock were created
	time.Sleep(1 * time.Second)

	tmr := time.NewTimer(waitFor)
	tkr := time.NewTicker(500 * time.Millisecond)
	for {
		select {
		case <-tmr.C:
			return errors.Errorf("timeout reached")
		case <-tkr.C:
			lock, err := f(ctx, m.conn, lck)
			if err != nil {
				// No lock, so operation has finished
				if errors.Is(err, mongo.ErrNoDocuments) {
					return nil
				}
				return errors.Wrap(err, "get lock data")
			}
			clusterTime, err := topo.GetClusterTime(ctx, m.conn)
			if err != nil {
				return errors.Wrap(err, "read cluster time")
			}
			if lock.Heartbeat.T+defs.StaleFrameSec < clusterTime.T {
				return errors.Errorf("operation stale, last beat ts: %d", lock.Heartbeat.T)
			}
		}
	}
}
