package slicer

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/prio"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

// Slicer is an incremental backup object
type Slicer struct {
	leadClient connect.Client
	node       *mongo.Client
	rs         string
	span       int64
	lastTS     primitive.Timestamp
	storage    storage.Storage
	oplog      *oplog.OplogBackup
	l          log.LogEvent
	cfg        *config.Config
}

// NewSlicer creates an incremental backup object
func NewSlicer(
	rs string,
	cn connect.Client,
	node *mongo.Client,
	to storage.Storage,
	cfg *config.Config,
	logger log.Logger,
) *Slicer {
	return &Slicer{
		leadClient: cn,
		node:       node,
		rs:         rs,
		span:       int64(defs.DefaultPITRInterval),
		storage:    to,
		oplog:      oplog.NewOplogBackup(node),
		cfg:        cfg,
		l:          logger.NewEvent(string(ctrl.CmdPITR), "", "", cfg.Epoch),
	}
}

// SetSpan sets span duration. Streaming will recognize the change and adjust on the next iteration.
func (s *Slicer) SetSpan(d time.Duration) {
	atomic.StoreInt64(&s.span, int64(d))
}

func (s *Slicer) GetSpan() time.Duration {
	return time.Duration(atomic.LoadInt64(&s.span))
}

func (s *Slicer) Catchup(ctx context.Context) error {
	s.l.Debug("start_catchup")

	lastBackup, err := backup.GetLastBackup(ctx, s.leadClient, nil)
	if err != nil {
		if errors.Is(err, errors.ErrNotFound) {
			err = errors.New("no backup found. full backup is required to start PITR")
		}
		return errors.Wrap(err, "get last backup")
	}

	var rs *backup.BackupReplset
	for i := range lastBackup.Replsets {
		r := &lastBackup.Replsets[i]
		if r.Name == s.rs {
			rs = r
			break
		}
	}
	if rs == nil {
		return errors.Errorf("no replset %q in the last backup %q. "+
			"full backup is required to start PITR",
			s.rs, lastBackup.Name)
	}

	lastRestore, err := restore.GetLastRestore(ctx, s.leadClient)
	if err != nil && !errors.Is(err, errors.ErrNotFound) {
		return errors.Wrap(err, "get last restore")
	}
	if lastRestore != nil && lastBackup.StartTS < lastRestore.StartTS {
		return errors.Errorf("no backup found after the restored %s, "+
			"a new backup is required to resume PITR",
			lastRestore.Backup)
	}

	lastChunk, err := oplog.PITRLastChunkMeta(ctx, s.leadClient, s.rs)
	if err != nil {
		if errors.Is(err, errors.ErrNotFound) {
			s.lastTS = lastBackup.LastWriteTS
			return nil
		}

		return errors.Wrap(err, "get last chunk")
	}
	if lastRestore != nil && int64(lastChunk.EndTS.T) < lastRestore.StartTS {
		s.lastTS = lastBackup.LastWriteTS
		return nil
	}

	if !lastChunk.EndTS.Before(rs.LastWriteTS) {
		// no need to copy oplog from backup
		s.lastTS = lastChunk.EndTS
		return nil
	}

	// if there is a gap between chunk and the backup - fill it
	// failed gap shouldn't prevent further chunk creation
	if lastChunk.EndTS.Before(rs.FirstWriteTS) {
		cfg, err := config.GetConfig(ctx, s.leadClient)
		if err != nil {
			return errors.Wrap(err, "get config")
		}

		err = s.upload(ctx,
			lastChunk.EndTS,
			rs.FirstWriteTS,
			cfg.PITR.Compression,
			cfg.PITR.CompressionLevel)
		if err != nil {
			var rangeErr oplog.InsuffRangeError
			if !errors.As(err, &rangeErr) {
				return err
			}

			s.l.Warning("skip chunk %s - %s: oplog has insufficient range",
				formatts(lastChunk.EndTS), formatts(rs.FirstWriteTS))
		} else {
			s.l.Info("uploaded chunk %s - %s", formatts(lastChunk.EndTS), formatts(rs.FirstWriteTS))
			s.lastTS = rs.FirstWriteTS
		}
	}

	if lastBackup.Type == defs.LogicalBackup {
		err = s.copyReplsetOplog(ctx, rs)
		if err != nil {
			s.l.Error("copy oplog from %q backup: %v", lastBackup.Name, err)
			return nil
		}
	}
	s.lastTS = rs.LastWriteTS

	return nil
}

func (s *Slicer) OplogOnlyCatchup(ctx context.Context) error {
	s.l.Debug("start_catchup [oplog only]")

	lastChunk, err := oplog.PITRLastChunkMeta(ctx, s.leadClient, s.rs)
	if err != nil {
		if !errors.Is(err, errors.ErrNotFound) {
			return errors.Wrap(err, "get last slice")
		}

		// no chunk before. start oplog slicing from now
		ts, err := topo.GetClusterTime(ctx, s.leadClient)
		if err != nil {
			return errors.Wrap(err, "get cluster time")
		}

		s.lastTS = ts
		s.l.Debug("lastTS set to %v %s", s.lastTS, formatts(s.lastTS))
		return nil
	}

	lastRestore, err := restore.GetLastRestore(ctx, s.leadClient)
	if err != nil && !errors.Is(err, errors.ErrNotFound) {
		return errors.Wrap(err, "get last restore")
	}
	if lastRestore != nil && lastRestore.StartTS > int64(lastChunk.EndTS.T) {
		// start a new oplog slicing after recent restore
		ts, err := topo.GetClusterTime(ctx, s.leadClient)
		if err != nil {
			return errors.Wrap(err, "get cluster time")
		}

		s.lastTS = ts
		s.l.Debug("lastTS set to %v %s", s.lastTS, formatts(s.lastTS))
	}

	ok, err := s.oplog.IsSufficient(lastChunk.EndTS)
	if err != nil {
		return errors.Wrapf(err, "check oplog sufficiency for %v", lastChunk)
	}
	if !ok {
		return oplog.InsuffRangeError{lastChunk.EndTS}
	}

	s.lastTS = lastChunk.EndTS
	s.l.Debug("lastTS set to %v %s", s.lastTS, formatts(s.lastTS))
	return nil
}

func (s *Slicer) copyReplsetOplog(ctx context.Context, rs *backup.BackupReplset) error {
	files, err := s.storage.List(rs.OplogName, "")
	if err != nil {
		return errors.Wrap(err, "list oplog files")
	}
	if len(files) == 0 {
		return nil
	}

	for _, file := range files {
		fw, lw, cmp, err := backup.ParseChunkName(file.Name)
		if err != nil {
			return errors.Wrapf(err, "parse chunk name %q", file.Name)
		}

		n := oplog.FormatChunkFilepath(s.rs, fw, lw, cmp)
		err = s.storage.Copy(rs.OplogName+"/"+file.Name, n)
		if err != nil {
			return errors.Wrap(err, "storage copy")
		}
		stat, err := s.storage.FileStat(n)
		if err != nil {
			return errors.Wrap(err, "file stat")
		}

		meta := oplog.OplogChunk{
			RS:          s.rs,
			FName:       n,
			Compression: cmp,
			StartTS:     fw,
			EndTS:       lw,
			Size:        stat.Size,
		}
		err = oplog.PITRAddChunk(ctx, s.leadClient, meta)
		if err != nil && !mongo.IsDuplicateKeyError(err) {
			return errors.Wrapf(err, "unable to save chunk meta %v", meta)
		}
	}

	s.l.Info("copied chunks %s - %s", formatts(rs.FirstWriteTS), formatts(rs.LastWriteTS))
	return nil
}

// OpMovedError is the error signaling that slicing op
// now being run by the other node
type OpMovedError struct {
	to string
}

func (e OpMovedError) Error() string {
	return fmt.Sprintf("pitr slicing resumed on node %s", e.to)
}

func (e OpMovedError) Is(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(OpMovedError) //nolint:errorlint
	return ok
}

// LogStartMsg message to log on successful streaming start
const LogStartMsg = "start_ok"

// Stream streaming (saving) chunks of the oplog to the given storage
func (s *Slicer) Stream(
	ctx context.Context,
	startingNode *topo.NodeInfo,
	stopC <-chan struct{},
	backupSig <-chan ctrl.OPID,
	compression compress.CompressionType,
	level *int,
	timeouts *config.BackupTimeouts,
	monitorPrio bool,
) error {
	if s.lastTS.T == 0 {
		return errors.New("no starting point defined")
	}
	s.l.Info("streaming started from %v / %v", time.Unix(int64(s.lastTS.T), 0).UTC(), s.lastTS.T)

	cspan := s.GetSpan()
	tk := time.NewTicker(cspan)
	defer tk.Stop()

	// early check for the log sufficiency to display error
	// before the timer clicks (not to wait minutes to report)
	ok, err := s.oplog.IsSufficient(s.lastTS)
	if err != nil {
		return errors.Wrap(err, "check oplog sufficiency")
	}
	if !ok {
		return oplog.InsuffRangeError{s.lastTS}
	}
	s.l.Debug(LogStartMsg)

	lastSlice := false
	llock := &lock.LockHeader{
		Replset: s.rs,
		Type:    ctrl.CmdPITR,
	}

	for {
		sliceTo := primitive.Timestamp{}
		// waiting for a trigger
		select {
		// wrapping up at the current point-in-time
		// upload the chunks up to the current time and return
		case <-stopC:
			s.l.Info("got done signal, stopping")
			lastSlice = true
		// on wakeup or tick whatever comes first do the job
		case bcp := <-backupSig:
			s.l.Info("got wake_up signal")
			if bcp != ctrl.NilOPID {
				opid := bcp.String()
				s.l.Info("wake_up for bcp %s", opid)

				sliceTo, err = s.backupRSStartTS(ctx, opid, timeouts.StartingStatus())
				if err != nil {
					if !errors.Is(err, errUnsuitableBackup) {
						return errors.Wrap(err, "get backup start TS")
					}

					s.l.Info("unsuitable backup [opid: %q]", opid)
					s.l.Info("pausing/stopping with last_ts %v", time.Unix(int64(s.lastTS.T), 0).UTC())
					sliceTo = primitive.Timestamp{}
				} else if s.lastTS.After(sliceTo) {
					// it can happen that prevoius slice >= backup's fisrt_write
					// in that case we have to just back off.
					s.l.Info("pausing/stopping with last_ts %v", time.Unix(int64(s.lastTS.T), 0).UTC())
					return nil
				}

				lastSlice = true
			}
		case <-tk.C:
		}

		// check if the node is still any good to make backups
		ninf, err := topo.GetNodeInfoExt(ctx, s.node)
		if err != nil {
			return errors.Wrap(err, "get node info")
		}
		q, err := topo.NodeSuits(ctx, s.node, ninf)
		if err != nil {
			return errors.Wrap(err, "node check")
		}
		if !q {
			return nil
		}

		// before any action check if we still got a lock. if no:
		//
		// - if there is another lock for a backup operation and we've got a
		//   `backupSig`- wait for the backup to start, make the last slice up
		//   unlit backup StartTS and return;
		// - if there is no other lock, we have to wait for the snapshot backup - see above
		//   (snapshot cmd can delete pitr lock but might not yet acquire the own one);
		// - if there is another lock and it is for pitr - return, probably split happened
		//   and a new worker was elected;
		// - any other case (including no lock) is the undefined behavior - return.
		//
		ld, err := s.getOpLock(ctx, llock, timeouts.StartingStatus())
		if err != nil {
			return errors.Wrap(err, "check lock")
		}

		// in case there is a lock, even a legit one (our own, or backup's one) but it is stale
		// we should return so the slicer would get through the lock acquisition again.
		ts, err := topo.GetClusterTime(ctx, s.leadClient)
		if err != nil {
			return errors.Wrap(err, "read cluster time")
		}
		if ld.Heartbeat.T+defs.StaleFrameSec < ts.T {
			return errors.Errorf("stale lock %#v, last beat ts: %d", ld.LockHeader, ld.Heartbeat.T)
		}
		if ld.Type != ctrl.CmdPITR {
			return errors.Errorf("another operation is running: %v", ld)
		}
		if ld.Node != startingNode.Me {
			return OpMovedError{ld.Node}
		}
		if monitorPrio &&
			prio.CalcPriorityForNode(startingNode) > prio.CalcPriorityForNode(ninf) {
			return errors.Errorf("node priority has changed %.1f->%.1f",
				prio.CalcPriorityForNode(startingNode),
				prio.CalcPriorityForNode(ninf))
		}
		if sliceTo.IsZero() {
			majority, err := topo.IsWriteMajorityRequested(ctx, s.node, s.leadClient.MongoOptions().WriteConcern)
			if err != nil {
				return errors.Wrap(err, "define requested majority")
			}
			sliceTo, err = topo.GetLastWrite(ctx, s.node, majority)
			if err != nil {
				return errors.Wrap(err, "define last write timestamp")
			}
		}

		err = s.upload(ctx, s.lastTS, sliceTo, compression, level)
		if err != nil {
			return err
		}

		logm := fmt.Sprintf("created chunk %s - %s", formatts(s.lastTS), formatts(sliceTo))
		if !lastSlice {
			nextChunkT := time.Now().Add(cspan)
			logm += fmt.Sprintf(". Next chunk creation scheduled to begin at ~%s", nextChunkT)
		}
		s.l.Info(logm)

		if lastSlice {
			s.l.Info("pausing/stopping with last_ts %v", time.Unix(int64(sliceTo.T), 0).UTC())
			return nil
		}

		s.lastTS = sliceTo

		if ispan := s.GetSpan(); cspan != ispan {
			tk.Reset(ispan)
			cspan = ispan
		}
	}
}

func (s *Slicer) upload(
	ctx context.Context,
	from primitive.Timestamp,
	to primitive.Timestamp,
	compression compress.CompressionType,
	level *int,
) error {
	s.oplog.SetTailingSpan(from, to)
	fname := oplog.FormatChunkFilepath(s.rs, from, to, compression)
	// if use parent ctx, upload will be canceled on the "done" signal
	size, err := storage.Upload(ctx, s.oplog, s.storage, compression, level, fname, -1)
	if err != nil {
		// PITR chunks have no metadata to indicate any failed state and if something went
		// wrong during the data read we may end up with an already created file. Although
		// the failed range won't be saved in db as the available for restore. It would get
		// in there after the storage resync. see: https://jira.percona.com/browse/PBM-602
		s.l.Debug("remove %s due to upload errors", fname)
		derr := s.storage.Delete(fname)
		if derr != nil {
			s.l.Error("remove %s: %v", fname, derr)
		}
		return errors.Wrapf(err, "unable to upload chunk %v.%v", from, to)
	}

	meta := oplog.OplogChunk{
		RS:          s.rs,
		FName:       fname,
		Compression: compression,
		StartTS:     from,
		EndTS:       to,
		Size:        size,
	}
	err = oplog.PITRAddChunk(ctx, s.leadClient, meta)
	if err != nil {
		s.l.Info("create last_chunk<->snapshot slice: %v", err)
		// duplicate key means chunk is already created by probably another routine
		// so we're safe to continue
		if !mongo.IsDuplicateKeyError(err) {
			return errors.Wrapf(err, "unable to save chunk meta %v", meta)
		}
	}

	return nil
}

func formatts(t primitive.Timestamp) string {
	return time.Unix(int64(t.T), 0).UTC().Format("2006-01-02T15:04:05")
}

func (s *Slicer) getOpLock(ctx context.Context, l *lock.LockHeader, t time.Duration) (lock.LockData, error) {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()

	var lck lock.LockData
	for j := 0; j < int(t.Seconds()); j++ {
		var err error
		lck, err = lock.GetOpLockData(ctx, s.leadClient, l)
		if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
			return lck, errors.Wrap(err, "get")
		}
		if lck.Type != ctrl.CmdUndefined {
			return lck, nil
		}
		<-tk.C
	}

	return lck, nil
}

var errUnsuitableBackup = errors.New("unsuitable backup")

func (s *Slicer) backupRSStartTS(ctx context.Context, opid string, t time.Duration) (primitive.Timestamp, error) {
	var ts primitive.Timestamp
	tk := time.NewTicker(time.Second)
	defer tk.Stop()

	for j := 0; j < int(t.Seconds()); j++ {
		<-tk.C

		b, err := backup.GetBackupByOPID(ctx, s.leadClient, opid)
		if err != nil {
			if errors.Is(err, errors.ErrNotFound) {
				continue
			}

			return ts, errors.Wrap(err, "get backup meta")
		}
		if b.Type != defs.LogicalBackup || util.IsSelective(b.Namespaces) {
			return ts, errUnsuitableBackup
		}
		if b.Status == defs.StatusCancelled || b.Status == defs.StatusError {
			return ts, errUnsuitableBackup
		}

		var rs *backup.BackupReplset
		for i := range b.Replsets {
			if r := &b.Replsets[i]; r.Name == s.rs {
				rs = r
				break
			}
		}
		if rs != nil && !rs.FirstWriteTS.IsZero() {
			return rs.FirstWriteTS, nil
		}
	}

	return ts, errors.New("run out of tries")
}
