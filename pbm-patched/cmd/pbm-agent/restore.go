package main

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

const (
	numInsertionWorkersDefault = 10
)

func (a *Agent) Restore(ctx context.Context, r *ctrl.RestoreCmd, opid ctrl.OPID, ep config.Epoch) {
	logger := log.FromContext(ctx)
	if r == nil {
		l := logger.NewEvent(string(ctrl.CmdRestore), "", opid.String(), ep.TS())
		l.Error("missed command")
		return
	}

	l := logger.NewEvent(string(ctrl.CmdRestore), r.Name, opid.String(), ep.TS())
	ctx = log.SetLogEventToContext(ctx, l)

	if !r.OplogTS.IsZero() {
		l.Info("to time: %s", time.Unix(int64(r.OplogTS.T), 0).UTC().Format(time.RFC3339))
	}

	nodeInfo, err := topo.GetNodeInfoExt(ctx, a.nodeConn)
	if err != nil {
		l.Error("get node info: %v", err)
		return
	}
	if nodeInfo.ArbiterOnly {
		l.Debug("arbiter node. skip")
		return
	}

	var lck *lock.Lock
	if nodeInfo.IsPrimary {
		epts := ep.TS()
		lck = lock.NewLock(a.leadConn, lock.LockHeader{
			Type:    ctrl.CmdRestore,
			Replset: nodeInfo.SetName,
			Node:    nodeInfo.Me,
			OPID:    opid.String(),
			Epoch:   &epts,
		})

		got, err := a.acquireLock(ctx, lck, l)
		if err != nil {
			l.Error("acquiring lock: %v", err)
			return
		}
		if !got {
			l.Debug("skip: lock not acquired")
			l.Error("unable to run the restore while another backup or restore process running")
			return
		}

		defer func() {
			if lck == nil {
				return
			}

			if err := lck.Release(); err != nil {
				l.Error("release lock: %v", err)
			}
		}()

		err = config.SetConfigVar(ctx, a.leadConn, "pitr.enabled", "false")
		if err != nil {
			l.Error("disable oplog slicer: %v", err)
		} else {
			l.Info("oplog slicer disabled")
		}
		a.removePitr()
	}

	// stop balancer during the restore
	if a.brief.Sharded && nodeInfo.IsClusterLeader() {
		bs, err := topo.GetBalancerStatus(ctx, a.leadConn)
		if err != nil {
			l.Error("get balancer status: %v", err)
			return
		}

		if bs.IsOn() {
			err := topo.SetBalancerStatus(ctx, a.leadConn, topo.BalancerModeOff)
			if err != nil {
				l.Error("set balancer off: %v", err)
			}

			l.Debug("waiting for balancer off")
			bs := topo.WaitForBalancerDisabled(ctx, a.leadConn, time.Second*30, l)
			if bs.IsDisabled() {
				l.Debug("balancer is disabled")
			} else {
				l.Warning("balancer is not disabled: balancer mode: %s, in balancer round: %t",
					bs.Mode, bs.InBalancerRound)
			}
		}
	}

	var bcpType defs.BackupType
	var bcp *backup.BackupMeta

	if r.External && r.BackupName == "" {
		bcpType = defs.ExternalBackup
	} else {
		l.Info("backup: %s", r.BackupName)

		// XXX: why is backup searched on storage?
		bcp, err = restore.LookupBackupMeta(ctx, a.leadConn, r.BackupName, a.brief.Me)
		if err != nil {
			err1 := addRestoreMetaWithError(ctx, a.leadConn, l, opid, r, nodeInfo.SetName,
				"define base backup: %v", err)
			if err1 != nil {
				l.Error("failed to save meta: %v", err1)
			}
			return
		}

		if !r.OplogTS.IsZero() && bcp.LastWriteTS.Compare(r.OplogTS) >= 0 {
			err1 := addRestoreMetaWithError(ctx, a.leadConn, l, opid, r, nodeInfo.SetName,
				"snapshot's last write is later than the target time. "+
					"Try to set an earlier snapshot. Or leave the snapshot empty "+
					"so PBM will choose one.")
			if err1 != nil {
				l.Error("failed to save meta: %v", err)
			}
			return
		}
		bcpType = bcp.Type
		r.BackupName = bcp.Name
	}

	cfg, err := config.GetConfig(ctx, a.leadConn)
	if err != nil {
		l.Error("get PBM configuration: %v", err)
		return
	}

	l.Info("recovery started")

	switch bcpType {
	case defs.LogicalBackup:
		if !nodeInfo.IsPrimary {
			l.Info("This node is not the primary. Check pbm agent on the primary for restore progress")
			return
		}

		numParallelColls := getNumParallelCollsConfig(r.NumParallelColls, cfg.Restore)
		numInsertionWorkersPerCol := getNumInsertionWorkersConfig(r.NumInsertionWorkers, cfg.Restore)

		rr := restore.New(a.leadConn, a.nodeConn, a.brief, cfg, r.RSMap, numParallelColls, numInsertionWorkersPerCol)
		if r.OplogTS.IsZero() {
			err = rr.Snapshot(ctx, r, opid, bcp)
		} else {
			err = rr.PITR(ctx, r, opid, bcp)
		}
	case defs.PhysicalBackup, defs.IncrementalBackup, defs.ExternalBackup:
		if lck != nil {
			// Don't care about errors. Anyway, the lock gonna disappear after the
			// restore. And the commands stream is down as well.
			// The lock also updates its heartbeats but Restore waits only for one state
			// with the timeout twice as short defs.StaleFrameSec.
			_ = lck.Release()
			lck = nil
		}

		fallbackOpt := cfg.Restore.GetFallbackEnabled()
		if r.Fallback != nil {
			fallbackOpt = *r.Fallback
		}
		allowPartlyDoneOpt := cfg.Restore.GetAllowPartlyDone()
		if r.AllowPartlyDone != nil {
			allowPartlyDoneOpt = *r.AllowPartlyDone
		}

		var rstr *restore.PhysRestore
		rstr, err = restore.NewPhysical(
			ctx,
			a.leadConn,
			a.nodeConn,
			nodeInfo,
			r.RSMap,
			fallbackOpt,
			allowPartlyDoneOpt,
		)
		if err != nil {
			l.Error("init physical backup: %v", err)
			return
		}

		err = rstr.Snapshot(ctx, r, r.OplogTS, opid, l, a.closeCMD, a.HbPause)
	}
	if err != nil {
		if errors.Is(err, restore.ErrNoDataForShard) {
			l.Info("no data for the shard in backup, skipping")
		} else {
			l.Error("restore: %v", err)
		}
		return
	}

	if bcpType == defs.LogicalBackup && nodeInfo.IsLeader() {
		epch, err := config.ResetEpoch(ctx, a.leadConn)
		if err != nil {
			l.Error("reset epoch: %v", err)
		}
		l.Debug("epoch set to %v", epch)
	}

	l.Info("recovery successfully finished")
}

func getNumParallelCollsConfig(rParallelColls *int32, restoreConf *config.RestoreConf) int {
	numParallelColls := max(runtime.NumCPU()/2, 1)
	if rParallelColls != nil && *rParallelColls > 0 {
		numParallelColls = int(*rParallelColls)
	} else if restoreConf != nil && restoreConf.NumParallelCollections > 0 {
		numParallelColls = restoreConf.NumParallelCollections
	}
	return numParallelColls
}

func getNumInsertionWorkersConfig(rInsWorkers *int32, restoreConf *config.RestoreConf) int {
	numInsertionWorkersPerCol := numInsertionWorkersDefault
	if rInsWorkers != nil && int(*rInsWorkers) > 0 {
		numInsertionWorkersPerCol = int(*rInsWorkers)
	} else if restoreConf != nil && restoreConf.NumInsertionWorkers > 0 {
		numInsertionWorkersPerCol = restoreConf.NumInsertionWorkers
	}
	return numInsertionWorkersPerCol
}

func addRestoreMetaWithError(
	ctx context.Context,
	conn connect.Client,
	l log.LogEvent,
	opid ctrl.OPID,
	cmd *ctrl.RestoreCmd,
	setName string,
	errStr string,
	args ...any,
) error {
	errMsg := fmt.Sprintf(errStr, args...)

	l.Error(errMsg)

	meta := &restore.RestoreMeta{
		Type:     defs.LogicalBackup,
		OPID:     opid.String(),
		Name:     cmd.Name,
		Backup:   cmd.BackupName,
		PITR:     int64(cmd.OplogTS.T),
		StartTS:  time.Now().UTC().Unix(),
		Status:   defs.StatusError,
		Error:    errMsg,
		Replsets: []restore.RestoreReplset{},
	}
	err := restore.SetRestoreMetaIfNotExists(ctx, conn, meta)
	if err != nil {
		return errors.Wrap(err, "write restore meta to db")
	}

	rs := restore.RestoreReplset{
		Name:       setName,
		StartTS:    time.Now().UTC().Unix(),
		Status:     defs.StatusError,
		Error:      errMsg,
		Conditions: restore.Conditions{},
	}
	err = restore.AddRestoreRSMeta(ctx, conn, cmd.Name, rs)
	if err != nil {
		return errors.Wrap(err, "write backup meta to db")
	}

	return nil
}
