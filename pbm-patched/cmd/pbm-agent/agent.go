package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type Agent struct {
	leadConn connect.Client
	nodeConn *mongo.Client
	bcp      *currentBackup
	pitrjob  *currentPitr
	slicerMx sync.Mutex
	bcpMx    sync.Mutex

	brief topo.NodeBrief

	numParallelColls int

	closeCMD chan struct{}
	pauseHB  int32

	monMx sync.Mutex
	// signal for stopping pitr monitor jobs and flag that jobs are started/stopped
	monStopSig chan struct{}
}

func newAgent(
	ctx context.Context,
	leadConn connect.Client,
	uri string,
	numParallelColls int,
) (*Agent, error) {
	nodeConn, err := connect.MongoConnect(ctx, uri, connect.Direct(true))
	if err != nil {
		return nil, err
	}

	info, err := topo.GetNodeInfo(ctx, nodeConn)
	if err != nil {
		return nil, errors.Wrap(err, "get node info")
	}

	mongoVersion, err := version.GetMongoVersion(ctx, nodeConn)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo version")
	}

	a := &Agent{
		leadConn: leadConn,
		closeCMD: make(chan struct{}),
		nodeConn: nodeConn,
		brief: topo.NodeBrief{
			URI:       uri,
			SetName:   info.SetName,
			Me:        info.Me,
			Sharded:   info.IsSharded(),
			ConfigSvr: info.IsConfigSrv(),
			Version:   mongoVersion,
		},
		numParallelColls: numParallelColls,
	}
	return a, nil
}

var (
	ErrArbiterNode = errors.New("arbiter")
	ErrDelayedNode = errors.New("delayed")
)

func (a *Agent) CanStart(ctx context.Context) error {
	info, err := topo.GetNodeInfo(ctx, a.nodeConn)
	if err != nil {
		return errors.Wrap(err, "get node info")
	}

	if info.IsStandalone() {
		return errors.New("mongod node can not be used to fetch a consistent " +
			"backup because it has no oplog. Please restart it as a primary " +
			"in a single-node replicaset to make it compatible with PBM's " +
			"backup method using the oplog")
	}
	if info.Msg == "isdbgrid" {
		return errors.New("mongos is not supported")
	}
	if info.ArbiterOnly {
		return ErrArbiterNode
	}
	if info.IsDelayed() {
		return ErrDelayedNode
	}

	return nil
}

func (a *Agent) showIncompatibilityWarning(ctx context.Context) {
	if err := version.FeatureSupport(a.brief.Version).PBMSupport(); err != nil {
		log.FromContext(ctx).
			Warning("", "", "", primitive.Timestamp{}, "WARNING: %v", err)
	}

	if a.brief.Sharded && a.brief.Version.IsShardedTimeseriesSupported() {
		tss, err := topo.ListShardedTimeseries(ctx, a.leadConn)
		if err != nil {
			log.FromContext(ctx).
				Error("", "", "", primitive.Timestamp{},
					"failed to list sharded timeseries: %v", err)
		} else if len(tss) != 0 {
			log.FromContext(ctx).
				Warning("", "", "", primitive.Timestamp{},
					"WARNING: cannot backup following sharded timeseries: %s",
					strings.Join(tss, ", "))
		}
	}

	if a.brief.Sharded && a.brief.Version.IsConfigShardSupported() {
		hasConfigShard, err := topo.HasConfigShard(ctx, a.leadConn)
		if err != nil {
			log.FromContext(ctx).
				Error("", "", "", primitive.Timestamp{},
					"failed to check for Config Shard: %v", err)
		} else if hasConfigShard {
			log.FromContext(ctx).
				Warning("", "", "", primitive.Timestamp{},
					"WARNING: selective backup and restore is not supported with Config Shard")
		}
	}
}

// Start starts listening the commands stream.
func (a *Agent) Start(ctx context.Context) error {
	logger := log.FromContext(ctx)
	logger.Printf("pbm-agent:\n%s", version.Current().All(""))
	logger.Printf("node: %s/%s", a.brief.SetName, a.brief.Me)
	logger.Printf("conn level ReadConcern: %v; WriteConcern: %v",
		a.leadConn.MongoOptions().ReadConcern.Level,
		a.leadConn.MongoOptions().WriteConcern.W)

	c, cerr := ctrl.ListenCmd(ctx, a.leadConn, a.closeCMD)

	logger.Printf("listening for the commands")

	for {
		select {
		case cmd, ok := <-c:
			if !ok {
				logger.Printf("change stream was closed")
				return nil
			}

			logger.Printf("got command %s, opid: %s", cmd, cmd.OPID)

			ep, err := config.GetEpoch(ctx, a.leadConn)
			if err != nil {
				logger.Error(string(cmd.Cmd), "", cmd.OPID.String(), ep.TS(), "get epoch: %v", err)
				continue
			}

			logger.Printf("got epoch %v", ep)

			switch cmd.Cmd {
			case ctrl.CmdBackup:
				// backup runs in the go-routine so it can be canceled
				go a.Backup(ctx, cmd.Backup, cmd.OPID, ep)
			case ctrl.CmdCancelBackup:
				a.CancelBackup()
			case ctrl.CmdRestore:
				a.Restore(ctx, cmd.Restore, cmd.OPID, ep)
			case ctrl.CmdReplay:
				a.OplogReplay(ctx, cmd.Replay, cmd.OPID, ep)
			case ctrl.CmdAddConfigProfile:
				a.handleAddConfigProfile(ctx, cmd.Profile, cmd.OPID, ep)
			case ctrl.CmdRemoveConfigProfile:
				a.handleRemoveConfigProfile(ctx, cmd.Profile, cmd.OPID, ep)
			case ctrl.CmdResync:
				a.Resync(ctx, cmd.Resync, cmd.OPID, ep)
			case ctrl.CmdDeleteBackup:
				a.Delete(ctx, cmd.Delete, cmd.OPID, ep)
			case ctrl.CmdDeletePITR:
				a.DeletePITR(ctx, cmd.DeletePITR, cmd.OPID, ep)
			case ctrl.CmdCleanup:
				a.Cleanup(ctx, cmd.Cleanup, cmd.OPID, ep)
			}
		case err, ok := <-cerr:
			if !ok {
				logger.Printf("change stream was closed")
				return nil
			}

			if errors.Is(err, ctrl.CursorClosedError{}) {
				return errors.Wrap(err, "stop listening")
			}

			ep, _ := config.GetEpoch(ctx, a.leadConn)
			logger.Error("", "", "", ep.TS(), "listening commands: %v", err)
		}
	}
}

// acquireLock tries to acquire the lock. If there is a stale lock
// it tries to mark op that held the lock (backup, [pitr]restore) as failed.
func (a *Agent) acquireLock(ctx context.Context, l *lock.Lock, lg log.LogEvent) (bool, error) {
	got, err := l.Acquire(ctx)
	if err == nil {
		return got, nil
	}

	if errors.Is(err, lock.DuplicatedOpError{}) || errors.Is(err, lock.ConcurrentOpError{}) {
		lg.Debug("get lock: %v", err)
		return false, nil
	}

	var er lock.StaleLockError
	if !errors.As(err, &er) {
		return false, err
	}

	lck := er.Lock
	lg.Debug("stale lock: %v", lck)
	var fn func(context.Context, *lock.Lock, string) error
	switch lck.Type {
	case ctrl.CmdBackup:
		fn = markBcpStale
	case ctrl.CmdRestore:
		fn = markRestoreStale
	default:
		return l.Acquire(ctx)
	}

	if err := fn(ctx, l, lck.OPID); err != nil {
		lg.Warning("failed to mark stale op '%s' as failed: %v", lck.OPID, err)
	}

	return l.Acquire(ctx)
}

func (a *Agent) HbPause() {
	atomic.StoreInt32(&a.pauseHB, 1)
}

func (a *Agent) HbResume() {
	atomic.StoreInt32(&a.pauseHB, 0)
}

func (a *Agent) HbIsRun() bool {
	return atomic.LoadInt32(&a.pauseHB) == 0
}

func (a *Agent) HbStatus(ctx context.Context) {
	logger := log.FromContext(ctx)
	l := logger.NewEvent("agentCheckup", "", "", primitive.Timestamp{})
	ctx = log.SetLogEventToContext(ctx, l)

	nodeVersion, err := version.GetMongoVersion(ctx, a.nodeConn)
	if err != nil {
		l.Error("get mongo version: %v", err)
	}

	hb := topo.AgentStat{
		Node:       a.brief.Me,
		RS:         a.brief.SetName,
		AgentVer:   version.Current().Version,
		MongoVer:   nodeVersion.VersionString,
		PerconaVer: nodeVersion.PSMDBVersion,
	}

	updateAgentStat(ctx, a, l, true, &hb)
	err = topo.SetAgentStatus(ctx, a.leadConn, &hb)
	if err != nil {
		l.Error("set status: %v", err)
	}

	defer func() {
		l.Debug("deleting agent status")
		err := topo.RemoveAgentStatus(context.Background(), a.leadConn, hb)
		if err != nil {
			logger := logger.NewEvent("agentCheckup", "", "", primitive.Timestamp{})
			logger.Error("remove agent heartbeat: %v", err)
		}
	}()

	tk := time.NewTicker(defs.AgentsStatCheckRange)
	defer tk.Stop()

	storageCheckTime := time.Now()
	parallelAgentCheckTime := time.Now()

	// check storage once in a while if all is ok (see https://jira.percona.com/browse/PBM-647)
	const storageCheckInterval = 15 * time.Second
	const parallelAgentCheckInternval = time.Minute

	for {
		select {
		case <-ctx.Done():
			return
		case <-tk.C:
			// don't check if on pause (e.g. physical restore)
			if !a.HbIsRun() {
				continue
			}

			now := time.Now()
			if now.Sub(parallelAgentCheckTime) >= parallelAgentCheckInternval {
				a.warnIfParallelAgentDetected(ctx, l, hb.Heartbeat)
				parallelAgentCheckTime = now
			}

			if now.Sub(storageCheckTime) >= storageCheckInterval {
				updateAgentStat(ctx, a, l, true, &hb)
				err = topo.SetAgentStatus(ctx, a.leadConn, &hb)
				if err == nil {
					storageCheckTime = now
				}
			} else {
				updateAgentStat(ctx, a, l, false, &hb)
				err = topo.SetAgentStatus(ctx, a.leadConn, &hb)
			}
			if err != nil {
				l.Error("set status: %v", err)
			}
		}
	}
}

func updateAgentStat(
	ctx context.Context,
	agent *Agent,
	l log.LogEvent,
	checkStore bool,
	hb *topo.AgentStat,
) {
	hb.PBMStatus = agent.pbmStatus(ctx)
	logHbStatus("PBM connection", hb.PBMStatus, l)

	hb.NodeStatus = agent.nodeStatus(ctx)
	logHbStatus("node connection", hb.NodeStatus, l)

	hb.StorageStatus = agent.storStatus(ctx, l, checkStore, hb)
	logHbStatus("storage connection", hb.StorageStatus, l)

	hb.Err = ""
	hb.Hidden = false
	hb.Passive = false

	inf, err := topo.GetNodeInfo(ctx, agent.nodeConn)
	if err != nil {
		l.Error("get NodeInfo: %v", err)
		hb.Err += fmt.Sprintf("get NodeInfo: %v", err)
	} else {
		hb.Hidden = inf.Hidden
		hb.Passive = inf.Passive
		hb.Arbiter = inf.ArbiterOnly
		if inf.SecondaryDelayOld != 0 {
			hb.DelaySecs = inf.SecondaryDelayOld
		} else {
			hb.DelaySecs = inf.SecondaryDelaySecs
		}

		hb.Heartbeat, err = topo.ClusterTimeFromNodeInfo(inf)
		if err != nil {
			hb.Err += fmt.Sprintf("get cluster time: %v", err)
		}
	}

	if inf != nil && inf.ArbiterOnly {
		hb.State = defs.NodeStateArbiter
		hb.StateStr = "ARBITER"
	} else {
		n, err := topo.GetNodeStatus(ctx, agent.nodeConn, agent.brief.Me)
		if err != nil {
			l.Error("get replSetGetStatus: %v", err)
			hb.Err += fmt.Sprintf("get replSetGetStatus: %v", err)
			hb.State = defs.NodeStateUnknown
			hb.StateStr = "UNKNOWN"
		} else {
			hb.State = n.State
			hb.StateStr = n.StateStr

			rLag, err := topo.ReplicationLag(ctx, agent.nodeConn, agent.brief.Me)
			if err != nil {
				l.Error("get replication lag: %v", err)
				hb.Err += fmt.Sprintf("get replication lag: %v", err)
			}
			hb.ReplicationLag = rLag
		}
	}
}

func (a *Agent) warnIfParallelAgentDetected(
	ctx context.Context,
	l log.LogEvent,
	lastHeartbeat primitive.Timestamp,
) {
	s, err := topo.GetAgentStatus(ctx, a.leadConn, a.brief.SetName, a.brief.Me)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return
		}
		l.Error("detecting parallel agent: get status: %v", err)
		return
	}
	if !s.Heartbeat.Equal(lastHeartbeat) {
		l.Warning("detected possible parallel agent for the node: "+
			"expected last heartbeat to be %d.%d, actual is %d.%d",
			lastHeartbeat.T, lastHeartbeat.I, s.Heartbeat.T, s.Heartbeat.I)
		return
	}
}

func (a *Agent) pbmStatus(ctx context.Context) topo.SubsysStatus {
	err := a.leadConn.MongoClient().Ping(ctx, nil)
	if err != nil {
		return topo.SubsysStatus{Err: err.Error()}
	}

	return topo.SubsysStatus{OK: true}
}

func (a *Agent) nodeStatus(ctx context.Context) topo.SubsysStatus {
	err := a.nodeConn.Ping(ctx, nil)
	if err != nil {
		return topo.SubsysStatus{Err: err.Error()}
	}

	return topo.SubsysStatus{OK: true}
}

func (a *Agent) storStatus(
	ctx context.Context,
	log log.LogEvent,
	forceCheckStorage bool,
	stat *topo.AgentStat,
) topo.SubsysStatus {
	// check storage once in a while if all is ok (see https://jira.percona.com/browse/PBM-647)
	// but if storage was(is) failed, check it always
	if !forceCheckStorage && stat.StorageStatus.OK {
		return topo.SubsysStatus{OK: true}
	}

	stg, err := util.GetStorage(ctx, a.leadConn, a.brief.Me, log)
	if err != nil {
		return topo.SubsysStatus{Err: fmt.Sprintf("unable to get storage: %v", err)}
	}

	ok, err := storage.IsInitialized(ctx, stg)
	if err != nil {
		errStr := fmt.Sprintf("storage check failed with: %v", err)
		return topo.SubsysStatus{Err: errStr}
	}
	if !ok {
		log.Warning("storage is not initialized")
	}

	return topo.SubsysStatus{OK: true}
}

func logHbStatus(name string, st topo.SubsysStatus, l log.LogEvent) {
	if !st.OK {
		l.Error("check %s: %s", name, st.Err)
	}
}
