package sdk

import (
	"context"
	"runtime"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

var ErrNotImplemented = errors.New("not implemented")

var (
	ErrBackupInProgress     = backup.ErrBackupInProgress
	ErrIncrementalBackup    = backup.ErrIncrementalBackup
	ErrNonIncrementalBackup = backup.ErrNonIncrementalBackup
	ErrNotBaseIncrement     = backup.ErrNotBaseIncrement
	ErrBaseForPITR          = backup.ErrBaseForPITR
)

type Client struct {
	conn connect.Client
	node string
}

func (c *Client) Close(ctx context.Context) error {
	return c.conn.Disconnect(ctx)
}

func (c *Client) CommandInfo(ctx context.Context, id CommandID) (*Command, error) {
	opid, err := ctrl.ParseOPID(string(id))
	if err != nil {
		return nil, ErrInvalidCommandID
	}

	res := c.conn.CmdStreamCollection().FindOne(ctx, bson.D{{"_id", opid.Obj()}})
	if err := res.Err(); err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrNotFound
		}
		return nil, errors.Wrap(err, "query")
	}

	cmd := &Command{}
	if err = res.Decode(&cmd); err != nil {
		return nil, errors.Wrap(err, "decode")
	}

	cmd.OPID = opid
	return cmd, nil
}

func (c *Client) GetConfig(ctx context.Context) (*Config, error) {
	return config.GetConfig(ctx, c.conn)
}

func (c *Client) SetConfig(ctx context.Context, cfg Config) (CommandID, error) {
	return NoOpID, config.SetConfig(ctx, c.conn, &cfg)
}

func (c *Client) GetAllConfigProfiles(ctx context.Context) ([]config.Config, error) {
	return config.ListProfiles(ctx, c.conn)
}

func (c *Client) GetConfigProfile(ctx context.Context, name string) (*config.Config, error) {
	profile, err := config.GetProfile(ctx, c.conn, name)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err = config.ErrMissedConfigProfile
		}
		return nil, err
	}

	return profile, nil
}

func (c *Client) AddConfigProfile(ctx context.Context, name string, cfg *Config) (CommandID, error) {
	opid, err := ctrl.SendAddConfigProfile(ctx, c.conn, name, cfg.Storage)
	return CommandID(opid.String()), err
}

func (c *Client) RemoveConfigProfile(ctx context.Context, name string) (CommandID, error) {
	opid, err := ctrl.SendRemoveConfigProfile(ctx, c.conn, name)
	return CommandID(opid.String()), err
}

func (c *Client) GetAllBackups(ctx context.Context) ([]BackupMetadata, error) {
	return backup.BackupsList(ctx, c.conn, 0)
}

func (c *Client) GetAllRestores(
	ctx context.Context,
	m connect.Client,
	options GetAllRestoresOptions,
) ([]RestoreMetadata, error) {
	limit := options.Limit
	if limit < 0 {
		limit = 0
	}
	return restore.RestoreList(ctx, c.conn, limit)
}

func (c *Client) GetBackupByName(
	ctx context.Context,
	name string,
	options GetBackupByNameOptions,
) (*BackupMetadata, error) {
	bcp, err := backup.NewDBManager(c.conn).GetBackupByName(ctx, name)
	if err != nil {
		return nil, errors.Wrap(err, "get backup meta")
	}

	return c.getBackupHelper(ctx, bcp, options)
}

func (c *Client) GetBackupByOpID(
	ctx context.Context,
	opid string,
	options GetBackupByNameOptions,
) (*BackupMetadata, error) {
	bcp, err := backup.NewDBManager(c.conn).GetBackupByOpID(ctx, opid)
	if err != nil {
		return nil, errors.Wrap(err, "get backup meta")
	}

	return c.getBackupHelper(ctx, bcp, options)
}

func (c *Client) getBackupHelper(
	ctx context.Context,
	bcp *BackupMetadata,
	options GetBackupByNameOptions,
) (*BackupMetadata, error) {
	if options.FetchIncrements && bcp.Type == IncrementalBackup {
		if bcp.SrcBackup != "" {
			return nil, ErrNotBaseIncrement
		}

		increments, err := backup.FetchAllIncrements(ctx, c.conn, bcp)
		if err != nil {
			return nil, errors.New("get increments")
		}
		if increments == nil {
			// use non-nil empty slice to mark fetch.
			// nil means it never tried to fetch before
			increments = make([][]*backup.BackupMeta, 0)
		}

		bcp.Increments = increments
	}

	if options.FetchFilelist {
		err := c.fillFilelistForBackup(ctx, bcp)
		if err != nil {
			return nil, errors.Wrap(err, "fetch filelist")
		}
	}

	return bcp, nil
}

func (c *Client) fillFilelistForBackup(ctx context.Context, bcp *BackupMetadata) error {
	var err error
	var stg storage.Storage

	eg, _ := errgroup.WithContext(ctx)
	eg.SetLimit(runtime.NumCPU())

	if version.HasFilelistFile(bcp.PBMVersion) {
		stg, err = util.StorageFromConfig(&bcp.Store.StorageConf, c.node, log.LogEventFromContext(ctx))
		if err != nil {
			return errors.Wrap(err, "get storage")
		}

		for i := range bcp.Replsets {
			rs := &bcp.Replsets[i]

			eg.Go(func() error {
				filelist, err := backup.ReadFilelistForReplset(stg, bcp.Name, rs.Name)
				if err != nil {
					return errors.Wrapf(err, "get filelist for %q [rs: %s] backup", bcp.Name, rs.Name)
				}

				rs.Files = filelist
				return nil
			})
		}
	}

	for i := range bcp.Increments {
		for j := range bcp.Increments[i] {
			bcp := bcp.Increments[i][j]

			if bcp.Status != defs.StatusDone {
				continue
			}
			if !version.HasFilelistFile(bcp.PBMVersion) {
				continue
			}

			if stg == nil {
				// in case if it is the first backup made with filelist file
				stg, err = c.getStorageForRead(ctx, bcp)
				if err != nil {
					return errors.Wrap(err, "get storage")
				}
			}

			for i := range bcp.Replsets {
				rs := &bcp.Replsets[i]

				eg.Go(func() error {
					filelist, err := backup.ReadFilelistForReplset(stg, bcp.Name, rs.Name)
					if err != nil {
						return errors.Wrapf(err, "fetch files for %q [rs: %s] backup", bcp.Name, rs.Name)
					}

					rs.Files = filelist
					return nil
				})
			}
		}
	}

	return eg.Wait()
}

func (c *Client) getStorageForRead(ctx context.Context, bcp *backup.BackupMeta) (storage.Storage, error) {
	stg, err := util.StorageFromConfig(&bcp.Store.StorageConf, c.node, log.LogEventFromContext(ctx))
	if err != nil {
		return nil, errors.Wrap(err, "get storage")
	}
	err = storage.HasReadAccess(ctx, stg)
	if err != nil && !errors.Is(err, storage.ErrUninitialized) {
		return nil, errors.Wrap(err, "check storage access")
	}

	return stg, nil
}

func (c *Client) GetRestoreByName(ctx context.Context, name string) (*RestoreMetadata, error) {
	return restore.GetRestoreMeta(ctx, c.conn, name)
}

func (c *Client) GetRestoreByOpID(ctx context.Context, opid string) (*RestoreMetadata, error) {
	return restore.GetRestoreMetaByOPID(ctx, c.conn, opid)
}

func (c *Client) SyncFromStorage(ctx context.Context, includeRestores bool) (CommandID, error) {
	var opts *ctrl.ResyncCmd
	if includeRestores {
		opts = &ctrl.ResyncCmd{IncludeRestores: true}
	}
	opid, err := ctrl.SendResync(ctx, c.conn, opts)
	return CommandID(opid.String()), err
}

func (c *Client) SyncFromExternalStorage(ctx context.Context, name string) (CommandID, error) {
	if name == "" {
		return NoOpID, errors.New("name is not provided")
	}

	opid, err := ctrl.SendResync(ctx, c.conn, &ctrl.ResyncCmd{Name: name})
	return CommandID(opid.String()), err
}

func (c *Client) SyncFromAllExternalStorages(ctx context.Context) (CommandID, error) {
	opid, err := ctrl.SendResync(ctx, c.conn, &ctrl.ResyncCmd{All: true})
	return CommandID(opid.String()), err
}

func (c *Client) ClearSyncFromExternalStorage(ctx context.Context, name string) (CommandID, error) {
	if name == "" {
		return NoOpID, errors.New("name is not provided")
	}

	opid, err := ctrl.SendResync(ctx, c.conn, &ctrl.ResyncCmd{Name: name, Clear: true})
	return CommandID(opid.String()), err
}

func (c *Client) ClearSyncFromAllExternalStorages(ctx context.Context) (CommandID, error) {
	opid, err := ctrl.SendResync(ctx, c.conn, &ctrl.ResyncCmd{All: true, Clear: true})
	return CommandID(opid.String()), err
}

func (c *Client) DeleteBackupByName(ctx context.Context, name string) (CommandID, error) {
	opts := GetBackupByNameOptions{FetchIncrements: true}
	bcp, err := c.GetBackupByName(ctx, name, opts)
	if err != nil {
		return NoOpID, errors.Wrap(err, "get backup meta")
	}
	if bcp.Type == defs.IncrementalBackup {
		err = CanDeleteIncrementalBackup(ctx, c, bcp, bcp.Increments)
	} else {
		err = CanDeleteBackup(ctx, c, bcp)
	}
	if err != nil {
		return NoOpID, err
	}

	opid, err := ctrl.SendDeleteBackupByName(ctx, c.conn, name)
	return CommandID(opid.String()), err
}

func (c *Client) DeleteBackupBefore(
	ctx context.Context,
	beforeTS Timestamp,
	options DeleteBackupBeforeOptions,
) (CommandID, error) {
	opid, err := ctrl.SendDeleteBackupBefore(ctx, c.conn, beforeTS, options.Type)
	return CommandID(opid.String()), err
}

func (c *Client) DeleteOplogRange(ctx context.Context, until Timestamp) (CommandID, error) {
	opid, err := ctrl.SendDeleteOplogRangeBefore(ctx, c.conn, until)
	return CommandID(opid.String()), err
}

func (c *Client) CleanupReport(ctx context.Context, beforeTS Timestamp) (CleanupReport, error) {
	return backup.MakeCleanupInfo(ctx, c.conn, beforeTS)
}

func (c *Client) RunCleanup(ctx context.Context, beforeTS Timestamp) (CommandID, error) {
	opid, err := ctrl.SendCleanup(ctx, c.conn, beforeTS)
	return CommandID(opid.String()), err
}

func (c *Client) CancelBackup(ctx context.Context) (CommandID, error) {
	opid, err := ctrl.SendCancelBackup(ctx, c.conn)
	return CommandID(opid.String()), err
}

func (c *Client) RunLogicalBackup(ctx context.Context, options LogicalBackupOptions) (CommandID, error) {
	return NoOpID, ErrNotImplemented
}

func (c *Client) RunPhysicalBackup(ctx context.Context, options PhysicalBackupOptions) (CommandID, error) {
	return NoOpID, ErrNotImplemented
}

func (c *Client) RunIncrementalBackup(ctx context.Context, options IncrementalBackupOptions) (CommandID, error) {
	return NoOpID, ErrNotImplemented
}

func (c *Client) Restore(ctx context.Context, backupName string, clusterTS Timestamp) (CommandID, error) {
	return NoOpID, ErrNotImplemented
}

var ErrStaleHeartbeat = errors.New("stale heartbeat")

func (c *Client) OpLocks(ctx context.Context) ([]OpLock, error) {
	locks, err := lock.GetLocks(ctx, c.conn, &lock.LockHeader{})
	if err != nil {
		return nil, errors.Wrap(err, "get locks")
	}
	if len(locks) == 0 {
		// no current op
		return nil, nil
	}

	clusterTime, err := ClusterTime(ctx, c)
	if err != nil {
		return nil, errors.Wrap(err, "get cluster time")
	}

	rv := make([]OpLock, len(locks))
	for i := range locks {
		rv[i].OpID = CommandID(locks[i].OPID)
		rv[i].Cmd = locks[i].Type
		rv[i].Replset = locks[i].Replset
		rv[i].Node = locks[i].Node
		rv[i].Heartbeat = locks[i].Heartbeat

		if rv[i].Heartbeat.T+defs.StaleFrameSec < clusterTime.T {
			rv[i].err = ErrStaleHeartbeat
		}
	}
	return rv, nil
}

// waitOp waits until operations which acquires a given lock are finished
func waitOp(ctx context.Context, conn connect.Client, lck *lock.LockHeader) error {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			lock, err := lock.GetLockData(ctx, conn, lck)
			if err != nil {
				if errors.Is(err, mongo.ErrNoDocuments) {
					// No lock, so operation has finished
					return nil
				}

				return errors.Wrap(err, "get lock data")
			}

			clusterTime, err := topo.GetClusterTime(ctx, conn)
			if err != nil {
				return errors.Wrap(err, "read cluster time")
			}

			if clusterTime.T-lock.Heartbeat.T >= defs.StaleFrameSec {
				return errors.Errorf("operation stale, last beat ts: %d", lock.Heartbeat.T)
			}
		}
	}
}

func lastLogErr(
	ctx context.Context,
	conn connect.Client,
	op ctrl.Command,
	after int64,
) (string, error) {
	r := &log.LogRequest{
		LogKeys: log.LogKeys{
			Severity: log.Error,
			Event:    string(op),
		},
		TimeMin: time.Unix(after, 0),
	}

	outC, errC := log.Follow(ctx, conn, r, false)

	for {
		select {
		case entry := <-outC:
			return entry.Msg, nil
		case err := <-errC:
			return "", err
		}
	}
}
