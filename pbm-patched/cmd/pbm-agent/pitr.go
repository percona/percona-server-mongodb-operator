package main

import (
	"context"
	"maps"
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
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/prio"
	"github.com/percona/percona-backup-mongodb/pbm/slicer"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

const (
	pitrCheckPeriod                 = 15 * time.Second
	pitrRenominationFrame           = 5 * time.Second
	pitrOpLockPollingCycle          = 15 * time.Second
	pitrOpLockPollingTimeOut        = 2 * time.Minute
	pitrNominationPollingCycle      = 2 * time.Second
	pitrNominationPollingTimeOut    = 2 * time.Minute
	pitrWatchMonitorPollingCycle    = 15 * time.Second
	pitrTopoMonitorPollingCycle     = 2 * time.Minute
	pitrActivityMonitorPollingCycle = 2 * time.Minute
	pitrHb                          = 5 * time.Second
)

type currentPitr struct {
	slicer *slicer.Slicer
	w      chan ctrl.OPID // to wake up a slicer on demand (not to wait for the tick)
	cancel context.CancelFunc
}

func (a *Agent) setPitr(p *currentPitr) {
	a.slicerMx.Lock()
	defer a.slicerMx.Unlock()

	if a.pitrjob != nil {
		a.pitrjob.cancel()
	}

	a.pitrjob = p
}

func (a *Agent) removePitr() {
	a.setPitr(nil)
}

func (a *Agent) getPitr() *currentPitr {
	a.slicerMx.Lock()
	defer a.slicerMx.Unlock()

	return a.pitrjob
}

// startMon starts monitor (watcher) and heartbeat jobs only on cluster leader.
func (a *Agent) startMon(ctx context.Context, cfg *config.Config) {
	a.monMx.Lock()
	defer a.monMx.Unlock()

	if a.monStopSig != nil {
		return
	}
	a.monStopSig = make(chan struct{})

	go a.pitrConfigMonitor(ctx, cfg)
	go a.pitrErrorMonitor(ctx)
	go a.pitrTopoMonitor(ctx)
	go a.pitrActivityMonitor(ctx)

	go a.pitrHb(ctx)
}

// stopMon stops monitor (watcher) jobs
func (a *Agent) stopMon() {
	a.monMx.Lock()
	defer a.monMx.Unlock()

	if a.monStopSig == nil {
		return
	}
	close(a.monStopSig)
	a.monStopSig = nil
}

func (a *Agent) sliceNow(opid ctrl.OPID) {
	a.slicerMx.Lock()
	defer a.slicerMx.Unlock()

	if a.pitrjob == nil {
		return
	}

	a.pitrjob.w <- opid
}

// PITR starts PITR processing routine
func (a *Agent) PITR(ctx context.Context) {
	l := log.FromContext(ctx)
	l.Printf("starting PITR routine")

	for {
		select {
		case <-a.closeCMD:
			l.Debug(string(ctrl.CmdPITR), "", "", primitive.Timestamp{}, "stopping main loop")
			return
		default:
		}

		err := a.pitr(ctx)
		if err != nil {
			// we need epoch just to log pitr err with an extra context
			// so not much care if we get it or not
			ep, _ := config.GetEpoch(ctx, a.leadConn)
			l.Error(string(ctrl.CmdPITR), "", "", ep.TS(), "init: %v", err)
		}

		time.Sleep(pitrCheckPeriod)
	}
}

// canSlicingNow returns lock.ConcurrentOpError if there is a parallel operation.
// Only physical backups (full, incremental, external) is allowed.
func canSlicingNow(ctx context.Context, conn connect.Client, stgCfg *config.StorageConf) error {
	ts, err := topo.GetClusterTime(ctx, conn)
	if err != nil {
		return errors.Wrap(err, "read cluster time")
	}

	locks, err := lock.GetLocks(ctx, conn, &lock.LockHeader{})
	if err != nil {
		return errors.Wrap(err, "get locks data")
	}

	for i := range locks {
		l := &locks[i]

		if l.Heartbeat.T+defs.StaleFrameSec < ts.T {
			// lock is stale, PITR can ignore it
			continue
		}

		if l.Type != ctrl.CmdBackup {
			return lock.ConcurrentOpError{l.LockHeader}
		}

		bcp, err := backup.GetBackupByOPID(ctx, conn, l.OPID)
		if err != nil {
			return errors.Wrap(err, "get backup metadata")
		}

		if bcp.Type == defs.LogicalBackup && bcp.Store.IsSameStorage(stgCfg) {
			return lock.ConcurrentOpError{l.LockHeader}
		}
	}

	return nil
}

func (a *Agent) pitr(ctx context.Context) error {
	cfg, err := config.GetConfig(ctx, a.leadConn)
	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return errors.Wrap(err, "get conf")
		}
		cfg = &config.Config{
			PITR: &config.PITRConf{},
		}
	}

	slicerInterval := cfg.OplogSlicerInterval()

	ep := config.Epoch(cfg.Epoch)
	l := log.FromContext(ctx).NewEvent(string(ctrl.CmdPITR), "", "", ep.TS())
	ctx = log.SetLogEventToContext(ctx, l)

	if !cfg.PITR.Enabled {
		a.removePitr()
		a.stopMon()
		return nil
	}

	if err := canSlicingNow(ctx, a.leadConn, &cfg.Storage); err != nil {
		e := lock.ConcurrentOpError{}
		if errors.As(err, &e) {
			l.Info("oplog slicer is paused for lock [%s, opid: %s]", e.Lock.Type, e.Lock.OPID)
			return nil
		}

		return errors.Wrap(err, "can slicing now")
	}

	if p := a.getPitr(); p != nil {
		// todo: remove this span changing detaction to leader
		// already do the job
		currInterval := p.slicer.GetSpan()
		if currInterval != slicerInterval {
			p.slicer.SetSpan(slicerInterval)

			// wake up slicer only if a new interval is smaller
			if currInterval > slicerInterval {
				a.sliceNow(ctrl.NilOPID)
			}
		}

		return nil
	}

	// just a check before a real locking
	// just trying to avoid redundant heavy operations
	moveOn, err := a.pitrLockCheck(ctx)
	if err != nil {
		return errors.Wrap(err, "check if already run")
	}
	if !moveOn {
		return nil
	}

	// should be after the lock pre-check
	// if node failing, then some other agent with healthy node will hopefully catch up
	// so this code won't be reached and will not pollute log with "pitr" errors while
	// the other node does successfully slice
	nodeInfo, err := topo.GetNodeInfoExt(ctx, a.nodeConn)
	if err != nil {
		return errors.Wrap(err, "get node info")
	}

	q, err := topo.NodeSuits(ctx, a.nodeConn, nodeInfo)
	if err != nil {
		return errors.Wrap(err, "node check")
	}
	// node is not suitable for doing pitr
	if !q {
		return nil
	}

	if nodeInfo.IsClusterLeader() {
		// start monitor jobs on cluster leader
		a.startMon(ctx, cfg)

		// start nomination process on cluster leader
		go a.leadNomination(ctx, cfg.PITR.Priority)
	}

	nominated, err := a.waitNominationForPITR(ctx, nodeInfo.SetName, nodeInfo.Me)
	if err != nil {
		return errors.Wrap(err, "wait nomination for pitr")
	}
	if !nominated {
		l.Debug("skip after pitr nomination, probably started by another node")
		return nil
	}

	epts := ep.TS()
	lck := lock.NewOpLock(a.leadConn, lock.LockHeader{
		Replset: a.brief.SetName,
		Node:    a.brief.Me,
		Type:    ctrl.CmdPITR,
		Epoch:   &epts,
	})

	got, err := a.acquireLock(ctx, lck, l)
	if err != nil {
		return errors.Wrap(err, "acquiring lock")
	}
	if !got {
		l.Debug("skip: lock not acquired")
		return nil
	}
	err = oplog.SetPITRNomineeACK(ctx, a.leadConn, a.brief.SetName, a.brief.Me)
	if err != nil {
		l.Error("set nominee ack: %v", err)
	}

	defer func() {
		if err != nil {
			l.Debug("setting RS error status for err: %v", err)
			if err := oplog.SetErrorRSStatus(ctx, a.leadConn, nodeInfo.SetName, nodeInfo.Me, err.Error()); err != nil {
				l.Error("error while setting error status: %v", err)
			}
		}
	}()

	stg, err := util.StorageFromConfig(&cfg.Storage, a.brief.Me, l)
	if err != nil {
		if err := lck.Release(); err != nil {
			l.Error("release lock: %v", err)
		}
		err = errors.Wrap(err, "unable to get storage configuration")
		return err
	}

	s := slicer.NewSlicer(a.brief.SetName, a.leadConn, a.nodeConn, stg, cfg, log.FromContext(ctx))
	s.SetSpan(slicerInterval)

	if cfg.PITR.OplogOnly {
		err = s.OplogOnlyCatchup(ctx)
	} else {
		err = s.Catchup(ctx)
	}
	if err != nil {
		if err := lck.Release(); err != nil {
			l.Error("release lock: %v", err)
		}
		err = errors.Wrap(err, "catchup")
		return err
	}

	go func() {
		stopSlicingCtx, stopSlicing := context.WithCancel(ctx)
		defer stopSlicing()
		stopC := make(chan struct{})

		w := make(chan ctrl.OPID)
		a.setPitr(&currentPitr{
			slicer: s,
			cancel: stopSlicing,
			w:      w,
		})

		go func() {
			<-stopSlicingCtx.Done()
			close(stopC)
			a.removePitr()
		}()

		go func() {
			tk := time.NewTicker(5 * time.Second)
			defer tk.Stop()

			for {
				select {
				case <-tk.C:
					cStatus, isHbStale := a.getPITRClusterAndStaleStatus(ctx)
					if cStatus == oplog.StatusReconfig {
						l.Debug("stop slicing because of reconfig")
						stopSlicing()
						return
					}
					if cStatus == oplog.StatusError {
						l.Debug("stop slicing because of error")
						stopSlicing()
						return
					}
					if isHbStale {
						l.Debug("stop slicing because PITR heartbeat is stale")
						stopSlicing()
						return
					}

				case <-stopSlicingCtx.Done():
					return
				}
			}
		}()

		// monitor implicit priority changes (secondary->primary)
		monitorPrio := cfg.PITR.Priority == nil

		streamErr := s.Stream(
			ctx,
			nodeInfo,
			stopC,
			w,
			cfg.PITR.Compression,
			cfg.PITR.CompressionLevel,
			cfg.Backup.Timeouts,
			monitorPrio,
		)
		if streamErr != nil {
			l.Error("streaming oplog: %v", streamErr)
			retErr := errors.Wrap(streamErr, "streaming oplog")
			if err := oplog.SetErrorRSStatus(ctx, a.leadConn, nodeInfo.SetName, nodeInfo.Me, retErr.Error()); err != nil {
				l.Error("setting RS status to StatusError: %v", err)
			}
		}

		if err := lck.Release(); err != nil {
			l.Error("release lock: %v", err)
		}
	}()

	return nil
}

// leadNomination does priority calculation and nomination part of PITR process.
// It requires to be run in separate go routine on cluster leader.
func (a *Agent) leadNomination(
	ctx context.Context,
	cfgPrio config.Priority,
) {
	l := log.LogEventFromContext(ctx)

	l.Debug("checking locks in the whole cluster")
	noLocks, err := a.waitAllOpLockRelease(ctx)
	if err != nil {
		l.Error("wait for all oplock release: %v", err)
		return
	}
	if !noLocks {
		l.Debug("there are still working pitr members, members nomination will not be continued")
		return
	}

	l.Debug("init pitr meta on the first usage")
	err = oplog.InitMeta(ctx, a.leadConn)
	if err != nil {
		l.Error("init meta: %v", err)
		return
	}

	candidates, err := topo.ListSteadyAgents(ctx, a.leadConn)
	if err != nil {
		l.Error("get agents list: %v", err)
		return
	}

	nodes := prio.CalcNodesPriority(nil, cfgPrio, candidates)

	shards, err := topo.ClusterMembers(ctx, a.leadConn.MongoClient())
	if err != nil {
		l.Error("get cluster members: %v", err)
		return
	}

	l.Debug("cluster is ready for nomination")
	err = oplog.SetClusterStatus(ctx, a.leadConn, oplog.StatusReady)
	if err != nil {
		l.Error("set cluster status ready: %v", err)
		return
	}

	err = a.reconcileReadyStatus(ctx, candidates)
	if err != nil {
		l.Error("reconciling ready status: %v", err)
		return
	}

	l.Debug("cluster leader sets running status")
	err = oplog.SetClusterStatus(ctx, a.leadConn, oplog.StatusRunning)
	if err != nil {
		l.Error("set running status: %v", err)
		return
	}

	for _, sh := range shards {
		go func(rs string) {
			if err := a.nominateRSForPITR(ctx, rs, nodes.RS(rs)); err != nil {
				l.Error("nodes nomination error for %s: %v", rs, err)
			}
		}(sh.RS)
	}
}

func (a *Agent) nominateRSForPITR(ctx context.Context, rs string, nodes [][]string) error {
	l := log.LogEventFromContext(ctx)
	l.Debug("pitr nomination list for %s: %v", rs, nodes)
	err := oplog.SetPITRNomination(ctx, a.leadConn, rs)
	if err != nil {
		return errors.Wrap(err, "set pitr nomination meta")
	}

	for _, n := range nodes {
		err = oplog.SetPITRNominees(ctx, a.leadConn, rs, n)
		if err != nil {
			return errors.Wrap(err, "set pitr nominees")
		}
		l.Debug("pitr nomination %s, set candidates %v", rs, n)

		time.Sleep(pitrRenominationFrame)

		nms, err := oplog.GetPITRNominees(ctx, a.leadConn, rs)
		if err != nil && !errors.Is(err, errors.ErrNotFound) {
			return errors.Wrap(err, "get pitr nominees")
		}
		if nms != nil && len(nms.Ack) > 0 {
			l.Debug("pitr nomination: %s won by %s", rs, nms.Ack)
			return nil
		}
	}

	return nil
}

func (a *Agent) pitrLockCheck(ctx context.Context) (bool, error) {
	ts, err := topo.GetClusterTime(ctx, a.leadConn)
	if err != nil {
		return false, errors.Wrap(err, "read cluster time")
	}

	tl, err := lock.GetOpLockData(ctx, a.leadConn, &lock.LockHeader{
		Replset: a.brief.SetName,
		Type:    ctrl.CmdPITR,
	})
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// no lock. good to move on
			return true, nil
		}

		return false, errors.Wrap(err, "get lock")
	}

	// stale lock means we should move on and clean it up during the lock.Acquire
	return tl.Heartbeat.T+defs.StaleFrameSec < ts.T, nil
}

// waitAllOpLockRelease waits to not have any live OpLock and in such a case returns true.
// Waiting process duration is deadlined, and in that case false will be returned.
func (a *Agent) waitAllOpLockRelease(ctx context.Context) (bool, error) {
	l := log.LogEventFromContext(ctx)

	tick := time.NewTicker(pitrOpLockPollingCycle)
	defer tick.Stop()

	tout := time.NewTimer(pitrOpLockPollingTimeOut)
	defer tout.Stop()

	for {
		select {
		case <-tick.C:
			running, err := oplog.IsOplogSlicing(ctx, a.leadConn)
			if err != nil {
				return false, errors.Wrap(err, "is oplog slicing check")
			}
			if !running {
				return true, nil
			}
			l.Debug("oplog slicing still running")
		case <-tout.C:
			l.Warning("timeout while waiting for relese all OpLocks")
			return false, nil
		}
	}
}

// waitNominationForPITR is used by potentional nominee to determinate if it
// is nominated by the leader. It returns true if member receive nomination.
// First, nominee needs to sync up about Ready status with cluster leader.
// After cluster Ready status is reached, nomination process will start.
// If nomination document is not found, nominee tries again on another tick.
// If Ack is found in fetched fragment, that means that another member confirmed
// nomination, so in that case current member lost nomination and false is returned.
func (a *Agent) waitNominationForPITR(ctx context.Context, rs, node string) (bool, error) {
	l := log.LogEventFromContext(ctx)

	err := a.confirmReadyStatus(ctx)
	if err != nil {
		return false, errors.Wrap(err, "confirming ready status")
	}

	tk := time.NewTicker(pitrNominationPollingCycle)
	defer tk.Stop()
	tout := time.NewTimer(pitrNominationPollingTimeOut)
	defer tout.Stop()

	l.Debug("waiting pitr nomination")
	for {
		select {
		case <-tk.C:
			nm, err := oplog.GetPITRNominees(ctx, a.leadConn, rs)
			if err != nil {
				if errors.Is(err, errors.ErrNotFound) {
					continue
				}
				return false, errors.Wrap(err, "check pitr nomination")
			}
			if len(nm.Ack) > 0 {
				return false, nil
			}
			for _, n := range nm.Nodes {
				if n == node {
					return true, nil
				}
			}
		case <-tout.C:
			return false, nil
		}
	}
}

func (a *Agent) confirmReadyStatus(ctx context.Context) error {
	l := log.LogEventFromContext(ctx)

	tk := time.NewTicker(pitrNominationPollingCycle)
	defer tk.Stop()
	tout := time.NewTimer(pitrNominationPollingTimeOut)
	defer tout.Stop()

	l.Debug("waiting for cluster ready status")
	for {
		select {
		case <-tk.C:
			status, err := oplog.GetClusterStatus(ctx, a.leadConn)
			if err != nil {
				if errors.Is(err, errors.ErrNotFound) {
					continue
				}
				return errors.Wrap(err, "getting cluser status")
			}
			if status == oplog.StatusReady {
				err = oplog.SetReadyRSStatus(ctx, a.leadConn, a.brief.SetName, a.brief.Me)
				if err != nil {
					return errors.Wrap(err, "setting ready status for RS")
				}
				return nil
			}
		case <-tout.C:
			return errors.New("timeout while waiting for ready status")
		}
	}
}

// reconcileReadyStatus waits all members to confirm Ready status.
// In case of timeout Ready status will be removed.
func (a *Agent) reconcileReadyStatus(ctx context.Context, agents []topo.AgentStat) error {
	l := log.LogEventFromContext(ctx)

	tk := time.NewTicker(pitrNominationPollingCycle)
	defer tk.Stop()

	tout := time.NewTimer(pitrNominationPollingTimeOut)
	defer tout.Stop()

	l.Debug("reconciling ready status from all agents")
	for {
		select {
		case <-tk.C:
			nodes, err := oplog.GetReplSetsWithStatus(ctx, a.leadConn, oplog.StatusReady)
			if err != nil {
				if errors.Is(err, errors.ErrNotFound) {
					continue
				}
				return errors.Wrap(err, "getting all nodes with ready status")
			}
			l.Debug("agents in ready: %d; waiting for agents: %d", len(nodes), len(agents))
			if len(nodes) >= len(agents) {
				return nil
			}
		case <-tout.C:
			// clean up cluster Ready status to not have an issue in next run
			if err := oplog.SetClusterStatus(ctx, a.leadConn, oplog.StatusUnset); err != nil {
				l.Error("error while cleaning cluster status: %v", err)
			}
			return errors.New("timeout while reconciling ready status")
		}
	}
}

// getPITRClusterAndStaleStatus gets cluster and heartbeat stale status from pbmPITR collection.
// In case of error, it returns StatusUnset and HB non stale status, and logs the error.
func (a *Agent) getPITRClusterAndStaleStatus(ctx context.Context) (oplog.Status, bool) {
	l := log.LogEventFromContext(ctx)
	isStale := false

	meta, err := oplog.GetMeta(ctx, a.leadConn)
	if err != nil {
		if !errors.Is(err, errors.ErrNotFound) {
			l.Error("getting metta for reconfig status check: %v", err)
		}
		return oplog.StatusUnset, isStale
	}

	ts, err := topo.GetClusterTime(ctx, a.leadConn)
	if err != nil {
		l.Error("read cluster time for pitr stale check: %v", err)
		return meta.Status, isStale
	}
	isStale = meta.Hb.T+defs.StaleFrameSec < ts.T

	return meta.Status, isStale
}

// pitrConfigMonitor watches changes in PITR section within PBM configuration.
// If relevant changes are detected (e.g. priorities, oplogOnly), it sets
// Reconfig cluster status, which means that slicing process needs to be restarted.
func (a *Agent) pitrConfigMonitor(ctx context.Context, firstConf *config.Config) {
	l := log.LogEventFromContext(ctx)
	l.Debug("start pitr config monitor")
	defer l.Debug("stop pitr config monitor")

	tk := time.NewTicker(pitrWatchMonitorPollingCycle)
	defer tk.Stop()

	currConf, currEpoh := firstConf.PITR, firstConf.Epoch

	for {
		select {
		case <-tk.C:
			cfg, err := config.GetConfig(ctx, a.leadConn)
			if err != nil {
				if !errors.Is(err, mongo.ErrNoDocuments) {
					l.Error("error while monitoring for pitr conf change: %v", err)
				}
				continue
			}

			if currEpoh == cfg.Epoch {
				continue
			}
			if !cfg.PITR.Enabled {
				// If pitr is disabled, there is no need to check its properties.
				// Enable/disable change is handled out of the monitor logic (in pitr main loop).
				currConf, currEpoh = cfg.PITR, cfg.Epoch
				continue
			}
			if isPITRConfigChanged(cfg.PITR, currConf) {
				continue
			}

			// there are differences between privious and new config in following
			// fields: Priority, OplogOnly, (OplogSpanMin)
			l.Info("pitr config has changed, re-config will be done")
			err = oplog.SetClusterStatus(ctx, a.leadConn, oplog.StatusReconfig)
			if err != nil {
				l.Error("error while setting cluster status reconfig: %v", err)
			}
			currConf, currEpoh = cfg.PITR, cfg.Epoch

		case <-ctx.Done():
			return

		case <-a.monStopSig:
			return
		}
	}
}

func isPITRConfigChanged(c1, c2 *config.PITRConf) bool {
	if c1 == nil || c2 == nil {
		return c1 == c2
	}
	if c1.OplogOnly != c2.OplogOnly {
		return false
	}
	// OplogSpanMin is compared and updated in the main pitr loop for now
	// if c1.OplogSpanMin != c2.OplogSpanMin {
	// 	return false
	// }
	if !maps.Equal(c1.Priority, c2.Priority) {
		return false
	}

	return true
}

func (a *Agent) pitrTopoMonitor(ctx context.Context) {
	l := log.LogEventFromContext(ctx)
	l.Debug("start pitr topo monitor")
	defer l.Debug("stop pitr topo monitor")

	tk := time.NewTicker(pitrTopoMonitorPollingCycle)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			nodeInfo, err := topo.GetNodeInfo(ctx, a.nodeConn)
			if err != nil {
				l.Error("topo monitor node info error", err)
				continue
			}

			if nodeInfo.IsClusterLeader() {
				continue
			}

			l.Info("topo/cluster leader has changed, re-configuring pitr members")
			err = oplog.SetClusterStatus(ctx, a.leadConn, oplog.StatusReconfig)
			if err != nil {
				l.Error("topo monitor reconfig status set", err)
				continue
			}
			a.removePitr()
			a.stopMon()

			return

		case <-ctx.Done():
			return

		case <-a.monStopSig:
			return
		}
	}
}

func (a *Agent) pitrActivityMonitor(ctx context.Context) {
	l := log.LogEventFromContext(ctx)
	l.Debug("start pitr agent activity monitor")
	defer l.Debug("stop pitr agent activity monitor")

	tk := time.NewTicker(pitrActivityMonitorPollingCycle)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			status, err := oplog.GetClusterStatus(ctx, a.leadConn)
			if err != nil {
				if errors.Is(err, errors.ErrNotFound) {
					continue
				}
				l.Error("agent activity get cluster status", err)
				continue
			}
			if status != oplog.StatusRunning {
				continue
			}

			ackedAgents, err := oplog.GetAgentsWithACK(ctx, a.leadConn)
			if err != nil {
				l.Error("activity get acked agents", err)
				continue
			}

			activeLocks, err := oplog.FetchSlicersWithActiveLocks(ctx, a.leadConn)
			if err != nil {
				l.Error("fetching active pitr locks", err)
				continue
			}

			if len(ackedAgents) == len(activeLocks) {
				continue
			}

			l.Debug("expected agents: %v; working agents: %v", ackedAgents, activeLocks)

			l.Info("not all ack agents are working, re-configuring pitr members")
			err = oplog.SetClusterStatus(ctx, a.leadConn, oplog.StatusReconfig)
			if err != nil {
				l.Error("activity monitor reconfig status set", err)
				continue
			}
			a.removePitr()
			a.stopMon()

			return

		case <-ctx.Done():
			return

		case <-a.monStopSig:
			return
		}
	}
}

// pitrErrorMonitor watches reported errors by agents on replica set(s)
// which are running PITR.
// In case of any reported error within pbmPITR collection (replicaset subdoc),
// cluster status Error is set.
func (a *Agent) pitrErrorMonitor(ctx context.Context) {
	l := log.LogEventFromContext(ctx)
	l.Debug("start pitr error monitor")
	defer l.Debug("stop pitr error monitor")

	tk := time.NewTicker(pitrWatchMonitorPollingCycle)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			replsets, err := oplog.GetReplSetsWithStatus(ctx, a.leadConn, oplog.StatusError)
			if err != nil {
				if errors.Is(err, errors.ErrNotFound) {
					continue
				}
				l.Error("get error replsets", err)
			}

			if len(replsets) == 0 {
				continue
			}

			l.Debug("error while executing pitr, pitr procedure will be restarted")
			err = oplog.SetClusterStatus(ctx, a.leadConn, oplog.StatusError)
			if err != nil {
				l.Error("error while setting cluster status Error: %v", err)
			}

		case <-ctx.Done():
			return

		case <-a.monStopSig:
			return
		}
	}
}

// pitrHB job sets PITR heartbeat.
func (a *Agent) pitrHb(ctx context.Context) {
	l := log.LogEventFromContext(ctx)
	l.Debug("start pitr hb")
	defer l.Debug("stop pitr hb")

	tk := time.NewTicker(pitrHb)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			err := oplog.SetHbForPITR(ctx, a.leadConn)
			if err != nil {
				l.Error("error while setting hb for pitr: %v", err)
			}

		case <-ctx.Done():
			return

		case <-a.monStopSig:
			return
		}
	}
}
