package backup

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type Backup struct {
	leadConn            connect.Client
	nodeConn            *mongo.Client
	brief               topo.NodeBrief
	config              *config.Config
	mongoVersion        string
	typ                 defs.BackupType
	incrBase            bool
	timeouts            *config.BackupTimeouts
	numParallelColls    int
	oplogSlicerInterval time.Duration
}

func New(leadConn connect.Client, conn *mongo.Client, brief topo.NodeBrief, dumpConns int) *Backup {
	return &Backup{
		leadConn:         leadConn,
		nodeConn:         conn,
		brief:            brief,
		typ:              defs.LogicalBackup,
		numParallelColls: dumpConns,
	}
}

func NewPhysical(leadConn connect.Client, conn *mongo.Client, brief topo.NodeBrief) *Backup {
	return &Backup{
		leadConn: leadConn,
		nodeConn: conn,
		brief:    brief,
		typ:      defs.PhysicalBackup,
	}
}

func NewExternal(leadConn connect.Client, conn *mongo.Client, brief topo.NodeBrief) *Backup {
	return &Backup{
		leadConn: leadConn,
		nodeConn: conn,
		brief:    brief,
		typ:      defs.ExternalBackup,
	}
}

func NewIncremental(leadConn connect.Client, conn *mongo.Client, brief topo.NodeBrief, base bool) *Backup {
	return &Backup{
		leadConn: leadConn,
		nodeConn: conn,
		brief:    brief,
		typ:      defs.IncrementalBackup,
		incrBase: base,
	}
}

func (b *Backup) SetConfig(cfg *config.Config) {
	b.config = cfg
}

func (b *Backup) SetMongoVersion(v string) {
	b.mongoVersion = v
}

func (b *Backup) SetTimeouts(t *config.BackupTimeouts) {
	b.timeouts = t
}

func (b *Backup) SetSlicerInterval(d time.Duration) {
	b.oplogSlicerInterval = d
}

func (b *Backup) SlicerInterval() time.Duration {
	if b.oplogSlicerInterval == 0 {
		return defs.DefaultPITRInterval
	}

	return b.oplogSlicerInterval
}

func (b *Backup) Init(
	ctx context.Context,
	bcp *ctrl.BackupCmd,
	opid ctrl.OPID,
	balancer topo.BalancerMode,
) error {
	ts, err := topo.GetClusterTime(ctx, b.leadConn)
	if err != nil {
		return errors.Wrap(err, "read cluster time")
	}

	meta := &BackupMeta{
		Type:        b.typ,
		OPID:        opid.String(),
		Name:        bcp.Name,
		Namespaces:  bcp.Namespaces,
		Compression: bcp.Compression,
		Store: Storage{
			Name:        b.config.Name,
			IsProfile:   b.config.IsProfile,
			StorageConf: b.config.Storage,
		},
		StartTS:  time.Now().Unix(),
		Status:   defs.StatusStarting,
		Replsets: []BackupReplset{},
		// the driver (mongo?) sets TS to the current wall clock if TS was 0, so have to init with 1
		LastWriteTS: primitive.Timestamp{T: 1, I: 1},
		// the driver (mongo?) sets TS to the current wall clock if TS was 0, so have to init with 1
		FirstWriteTS:   primitive.Timestamp{T: 1, I: 1},
		PBMVersion:     version.Current().Version,
		MongoVersion:   b.mongoVersion,
		Nomination:     []BackupRsNomination{},
		BalancerStatus: balancer,
		Hb:             ts,
	}

	fcv, err := version.GetFCV(ctx, b.nodeConn)
	if err != nil {
		return errors.Wrap(err, "get featureCompatibilityVersion")
	}
	meta.FCV = fcv

	if b.brief.Sharded {
		ss, err := topo.ClusterMembers(ctx, b.leadConn.MongoClient())
		if err != nil {
			return errors.Wrap(err, "get shards")
		}

		shards := make(map[string]string)
		for i := range ss {
			s := &ss[i]
			if s.RS != s.ID {
				shards[s.RS] = s.ID
			}
		}
		if len(shards) != 0 {
			meta.ShardRemap = shards
		}
	}

	return saveBackupMeta(ctx, b.leadConn, meta)
}

// Run runs backup.
// TODO: describe flow
//
//nolint:nonamedreturns
func (b *Backup) Run(ctx context.Context, bcp *ctrl.BackupCmd, opid ctrl.OPID, l log.LogEvent) (err error) {
	inf, err := topo.GetNodeInfoExt(ctx, b.nodeConn)
	if err != nil {
		return errors.Wrap(err, "get cluster info")
	}

	oplogTS, err := oplog.GetOplogStartTime(ctx, b.nodeConn)
	if err != nil {
		return errors.Wrap(err, "define oplog start position")
	}
	rsMeta := BackupReplset{
		Name:         inf.SetName,
		Node:         inf.Me,
		PBMVersion:   version.Current().Version,
		MongoVersion: b.mongoVersion,
		StartTS:      time.Now().UTC().Unix(),
		Status:       defs.StatusRunning,
		Conditions:   []Condition{},
		FirstWriteTS: oplogTS,
	}
	if v := inf.IsConfigSrv(); v {
		rsMeta.IsConfigSvr = &v

		isConfigShard, err := topo.HasConfigShard(ctx, b.leadConn)
		if err != nil {
			return errors.Wrap(err, "has configshard")
		}
		if isConfigShard {
			rsMeta.IsConfigShard = &isConfigShard
		}
	}

	stg, err := util.StorageFromConfig(&b.config.Storage, inf.Me, l)
	if err != nil {
		return errors.Wrap(err, "unable to get PBM storage configuration settings")
	}

	bcpm, err := NewDBManager(b.leadConn).GetBackupByName(ctx, bcp.Name)
	if err != nil {
		return errors.Wrap(err, "balancer status, get backup meta")
	}

	// on any error the RS' and the backup' (in case this is the backup leader) meta will be marked appropriately
	defer func() {
		if err != nil {
			status := defs.StatusError
			if errors.Is(err, storage.ErrCancelled) || errors.Is(err, context.Canceled) {
				status = defs.StatusCancelled
			}

			ferr := ChangeRSState(b.leadConn, bcp.Name, rsMeta.Name, status, err.Error())
			l.Info("mark RS as %s `%v`: %v", status, err, ferr)

			if inf.IsLeader() {
				ferr := ChangeBackupState(b.leadConn, bcp.Name, status, err.Error())
				l.Info("mark backup as %s `%v`: %v", status, err, ferr)
			}
		}

		// Turn the balancer back on if needed
		//
		// Every agent will check if the balancer was on before the backup started.
		// And will try to turn it on again if so. So if the leader node went down after turning off
		// the balancer some other node will bring it back.
		// TODO: what if all agents went down.
		if bcpm.BalancerStatus != topo.BalancerModeOn {
			return
		}

		errd := topo.SetBalancerStatus(context.Background(), b.leadConn, topo.BalancerModeOn)
		if errd != nil {
			l.Error("set balancer ON: %v", errd)
			return
		}
		l.Debug("set balancer on")
	}()

	if inf.IsLeader() {
		hbstop := make(chan struct{})
		defer close(hbstop)

		err := BackupHB(ctx, b.leadConn, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "init heartbeat")
		}

		go func() {
			tk := time.NewTicker(time.Second * 5)
			defer tk.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-tk.C:
					err = BackupHB(ctx, b.leadConn, bcp.Name)
					if err != nil {
						l.Error("send pbm heartbeat: %v", err)
					}
				case <-hbstop:
					return
				}
			}
		}()
	}

	err = storage.HasReadAccess(ctx, stg)
	if err != nil {
		if !errors.Is(err, storage.ErrUninitialized) {
			return errors.Wrap(err, "check read access")
		}

		if inf.IsLeader() {
			err = util.Initialize(ctx, stg)
			if err != nil {
				return errors.Wrap(err, "init storage")
			}
		}
	}

	if inf.IsSharded() && inf.IsLeader() {
		if bcpm.BalancerStatus == topo.BalancerModeOn {
			err = topo.SetBalancerStatus(ctx, b.leadConn, topo.BalancerModeOff)
			if err != nil {
				return errors.Wrap(err, "set balancer OFF")
			}

			l.Debug("waiting for balancer off")
			bs := waitForBalancerOff(ctx, b.leadConn, time.Second*30, l)
			l.Debug("balancer status: %s", bs)
		}
	}

	// Waiting for StatusStarting to move further.
	// In case some preparations has to be done before backup.
	err = b.waitForStatus(ctx, bcp.Name, defs.StatusStarting, util.Ref(b.timeouts.StartingStatus()))
	if err != nil {
		return errors.Wrap(err, "waiting for start")
	}

	defer func() {
		if err == nil || !inf.IsLeader() {
			return
		}

		if err := DeleteBackupFiles(stg, bcp.Name); err != nil {
			l.Error("Failed to delete leftover files for canceled backup %q", bcpm.Name)
		}
	}()

	switch b.typ {
	case defs.LogicalBackup:
		err = b.doLogical(ctx, bcp, opid, &rsMeta, inf, stg, l)
	case defs.PhysicalBackup, defs.IncrementalBackup, defs.ExternalBackup:
		err = b.doPhysical(ctx, bcp, opid, &rsMeta, inf, stg, l)
	default:
		return errors.New("undefined backup type")
	}
	if err != nil {
		return err
	}

	err = ChangeRSState(b.leadConn, bcp.Name, rsMeta.Name, defs.StatusDone, "")
	if err != nil {
		return errors.Wrap(err, "set shard's StatusDone")
	}

	if inf.IsLeader() {
		shards, err := topo.ClusterMembers(ctx, b.leadConn.MongoClient())
		if err != nil {
			return errors.Wrap(err, "check cluster for backup done: get cluster members")
		}

		err = b.convergeCluster(ctx, bcp.Name, opid.String(), shards, defs.StatusDone)
		err = errors.Wrap(err, "check cluster for backup done: convergeCluster")
		if err != nil {
			return err
		}

		bcpm, err = NewDBManager(b.leadConn).GetBackupByName(ctx, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "get backup metadata")
		}

		// PBM-1114: update file metadata with the same values as in database
		unix := time.Now().Unix()
		bcpm.Status = defs.StatusDone
		bcpm.LastTransitionTS = unix
		bcpm.Conditions = append(bcpm.Conditions, Condition{
			Timestamp: unix,
			Status:    defs.StatusDone,
		})

		err = writeMeta(stg, bcpm)
		if err != nil {
			return errors.Wrap(err, "dump metadata")
		}

		err = CheckBackupFiles(ctx, stg, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "check backup files")
		}

		err = ChangeBackupStateWithUnixTime(ctx, b.leadConn, bcp.Name, defs.StatusDone, unix, "")
		return errors.Wrapf(err, "check cluster for backup done: update backup meta with %s",
			defs.StatusDone)
	} else {
		// to be sure the locks released only after the "done" status had written
		err = b.waitForStatus(ctx, bcp.Name, defs.StatusDone, nil)
		return errors.Wrap(err, "waiting for done")
	}
}

func waitForBalancerOff(ctx context.Context, conn connect.Client, t time.Duration, l log.LogEvent) topo.BalancerMode {
	dn := time.NewTimer(t)
	defer dn.Stop()

	tk := time.NewTicker(time.Millisecond * 500)
	defer tk.Stop()

	var bs *topo.BalancerStatus
	var err error

Loop:
	for {
		select {
		case <-tk.C:
			bs, err = topo.GetBalancerStatus(ctx, conn)
			if err != nil {
				l.Error("get balancer status: %v", err)
				continue
			}
			if bs.Mode == topo.BalancerModeOff {
				return topo.BalancerModeOff
			}
		case <-dn.C:
			break Loop
		}
	}

	if bs == nil {
		return topo.BalancerMode("")
	}

	return bs.Mode
}

func (b *Backup) toState(
	ctx context.Context,
	status defs.Status,
	bcp, opid string,
	inf *topo.NodeInfo,
	wait *time.Duration,
) error {
	err := ChangeRSState(b.leadConn, bcp, inf.SetName, status, "")
	if err != nil {
		return errors.Wrap(err, "set shard's status")
	}

	if inf.IsLeader() {
		err = b.reconcileStatus(ctx, bcp, opid, status, wait)
		if err != nil {
			if errors.Is(err, errConvergeTimeOut) {
				return errors.Wrap(err, "couldn't get response from all shards")
			}
			return errors.Wrapf(err, "check cluster for backup `%s`", status)
		}
	} else {
		err = b.waitForStatus(ctx, bcp, status, wait)
		if err != nil {
			return errors.Wrapf(err, "waiting for %s", status)
		}
	}

	return nil
}

func (b *Backup) reconcileStatus(
	ctx context.Context,
	bcpName, opid string,
	status defs.Status,
	timeout *time.Duration,
) error {
	shards, err := topo.ClusterMembers(ctx, b.leadConn.MongoClient())
	if err != nil {
		return errors.Wrap(err, "get cluster members")
	}

	if timeout != nil {
		err = b.convergeClusterWithTimeout(ctx, bcpName, opid, shards, status, *timeout)
		err = errors.Wrap(err, "convergeClusterWithTimeout")
	} else {
		err = b.convergeCluster(ctx, bcpName, opid, shards, status)
		err = errors.Wrap(err, "convergeCluster")
	}
	if err != nil {
		return err
	}

	err = ChangeBackupState(b.leadConn, bcpName, status, "")
	return errors.Wrapf(err, "update backup meta with %s", status)
}

// convergeCluster waits until all given shards reached `status` and updates a cluster status
func (b *Backup) convergeCluster(
	ctx context.Context,
	bcpName, opid string,
	shards []topo.Shard,
	status defs.Status,
) error {
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			ok, err := b.converged(ctx, bcpName, opid, shards, status)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

var errConvergeTimeOut = errors.New("reached converge timeout")

// convergeClusterWithTimeout waits up to the geiven timeout until
// all given shards reached `status` and then updates the cluster status
func (b *Backup) convergeClusterWithTimeout(
	ctx context.Context,
	bcpName,
	opid string,
	shards []topo.Shard,
	status defs.Status,
	t time.Duration,
) error {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()

	tout := time.NewTimer(t)
	defer tout.Stop()

	for {
		select {
		case <-tk.C:
			ok, err := b.converged(ctx, bcpName, opid, shards, status)
			if err != nil {
				return err
			}
			if ok {
				return nil
			}
		case <-tout.C:
			return errors.Wrap(errConvergeTimeOut, t.String())
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (b *Backup) converged(
	ctx context.Context,
	bcpName, opid string,
	shards []topo.Shard,
	status defs.Status,
) (bool, error) {
	shardsToFinish := len(shards)
	bmeta, err := NewDBManager(b.leadConn).GetBackupByName(ctx, bcpName)
	if err != nil {
		return false, errors.Wrap(err, "get backup metadata")
	}

	clusterTime, err := topo.GetClusterTime(ctx, b.leadConn)
	if err != nil {
		return false, errors.Wrap(err, "read cluster time")
	}

	for _, sh := range shards {
		for _, shard := range bmeta.Replsets {
			if shard.Name == sh.RS {
				// check if node alive
				lck, err := lock.GetLockData(ctx, b.leadConn, &lock.LockHeader{
					Type:    ctrl.CmdBackup,
					OPID:    opid,
					Replset: shard.Name,
				})

				// nodes are cleaning its locks moving to the done status
				// so no lock is ok and no need to ckech the heartbeats
				if status != defs.StatusDone && !errors.Is(err, mongo.ErrNoDocuments) {
					if err != nil {
						return false, errors.Wrapf(err, "unable to read lock for shard %s", shard.Name)
					}
					if lck.Heartbeat.T+defs.StaleFrameSec < clusterTime.T {
						return false, errors.Errorf("lost shard %s, last beat ts: %d", shard.Name, lck.Heartbeat.T)
					}
				}

				// check status
				switch shard.Status {
				case status:
					shardsToFinish--
				case defs.StatusCancelled:
					return false, storage.ErrCancelled
				case defs.StatusError:
					return false, errors.Errorf("backup on shard %s failed with: %s", shard.Name, bmeta.Error())
				}
			}
		}
	}

	return shardsToFinish == 0, nil
}

func (b *Backup) waitForStatus(
	ctx context.Context,
	bcpName string,
	status defs.Status,
	waitFor *time.Duration,
) error {
	var tout <-chan time.Time
	if waitFor != nil {
		tmr := time.NewTimer(*waitFor)
		defer tmr.Stop()

		tout = tmr.C
	}

	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			bmeta, err := NewDBManager(b.leadConn).GetBackupByName(ctx, bcpName)
			if errors.Is(err, errors.ErrNotFound) {
				continue
			}
			if err != nil {
				return errors.Wrap(err, "get backup metadata")
			}

			clusterTime, err := topo.GetClusterTime(ctx, b.leadConn)
			if err != nil {
				return errors.Wrap(err, "read cluster time")
			}

			if bmeta.Hb.T+defs.StaleFrameSec < clusterTime.T {
				return errors.Errorf("backup stuck, last beat ts: %d", bmeta.Hb.T)
			}

			switch bmeta.Status {
			case status:
				return nil
			case defs.StatusCancelled:
				return storage.ErrCancelled
			case defs.StatusError:
				return errors.Errorf("cluster failed: %v", err)
			}
		case <-tout:
			return errors.New("no backup meta, looks like a leader failed to start")
		case <-ctx.Done():
			return nil
		}
	}
}

//nolint:nonamedreturns
func (b *Backup) waitForFirstLastWrite(
	ctx context.Context,
	bcpName string,
) (first, last primitive.Timestamp, err error) {
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()

	for {
		select {
		case <-tk.C:
			bmeta, err := NewDBManager(b.leadConn).GetBackupByName(ctx, bcpName)
			if err != nil {
				return first, last, errors.Wrap(err, "get backup metadata")
			}

			clusterTime, err := topo.GetClusterTime(ctx, b.leadConn)
			if err != nil {
				return first, last, errors.Wrap(err, "read cluster time")
			}

			if bmeta.Hb.T+defs.StaleFrameSec < clusterTime.T {
				return first, last, errors.Errorf("backup stuck, last beat ts: %d", bmeta.Hb.T)
			}

			if bmeta.FirstWriteTS.T > 1 && bmeta.LastWriteTS.T > 1 {
				return bmeta.FirstWriteTS, bmeta.LastWriteTS, nil
			}
		case <-ctx.Done():
			return first, last, nil
		}
	}
}

func writeMeta(stg storage.Storage, meta *BackupMeta) error {
	b, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return errors.Wrap(err, "marshal data")
	}

	err = stg.Save(meta.Name+defs.MetadataFileSuffix, bytes.NewReader(b), -1)
	return errors.Wrap(err, "write to store")
}

func (b *Backup) setClusterFirstWrite(ctx context.Context, bcpName string) error {
	var err error
	var bcp *BackupMeta
	dbManager := NewDBManager(b.leadConn)

	// make sure all replset has the first write ts
	for {
		bcp, err = dbManager.GetBackupByName(ctx, bcpName)
		if err != nil {
			return errors.Wrap(err, "get backup metadata")
		}
		if len(bcp.Replsets) == 0 {
			return errors.New("no replset metadata")
		}

		if condAll(bcp.Replsets, func(br *BackupReplset) bool { return br.FirstWriteTS.T > 1 }) {
			break
		}

		time.Sleep(time.Second)
	}

	fw := bcp.Replsets[0].FirstWriteTS
	for i := 1; i != len(bcp.Replsets); i++ {
		if rs := &bcp.Replsets[i]; rs.FirstWriteTS.After(fw) {
			fw = rs.FirstWriteTS
		}
	}

	err = SetFirstWrite(ctx, b.leadConn, bcpName, fw)
	return errors.Wrap(err, "set timestamp")
}

func (b *Backup) setClusterLastWrite(ctx context.Context, bcpName string) error {
	return setClusterLastWriteImpl(ctx, b.leadConn, primitive.Timestamp.Before, bcpName)
}

func (b *Backup) setClusterLastWriteForPhysical(ctx context.Context, bcpName string) error {
	return setClusterLastWriteImpl(ctx, b.leadConn, primitive.Timestamp.After, bcpName)
}

func setClusterLastWriteImpl(
	ctx context.Context,
	conn connect.Client,
	cmp func(a, b primitive.Timestamp) bool,
	bcpName string,
) error {
	var err error
	var bcp *BackupMeta
	dbManager := NewDBManager(conn)

	// make sure all replset has the last write ts
	for {
		bcp, err = dbManager.GetBackupByName(ctx, bcpName)
		if err != nil {
			return errors.Wrap(err, "get backup metadata")
		}
		if len(bcp.Replsets) == 0 {
			return errors.New("no replset metadata")
		}

		if condAll(bcp.Replsets, func(br *BackupReplset) bool { return br.LastWriteTS.T > 1 }) {
			break
		}

		// before we try another time, let's check if we have lost agent
		clusterTime, err := topo.GetClusterTime(ctx, conn)
		if err != nil {
			return errors.Wrap(err, "read cluster time")
		}

		locks, err := lock.GetLocks(ctx, conn, &lock.LockHeader{
			Type: ctrl.CmdBackup,
			OPID: bcp.OPID,
		})
		if err != nil {
			return errors.Wrap(err, "get locks")
		}

		for _, replset := range bcp.Replsets {
			var lck *lock.LockData
			for _, l := range locks {
				if l.Replset == replset.Name {
					lck = &l
					break
				}
			}
			if lck == nil {
				continue
			}
			if lck.Heartbeat.T+defs.StaleFrameSec < clusterTime.T {
				return errors.Errorf("lost shard %s, last beat ts: %d", replset.Name, lck.Heartbeat.T)
			}
		}

		time.Sleep(10 * time.Second)
	}

	lw := bcp.Replsets[0].LastWriteTS
	for i := 1; i != len(bcp.Replsets); i++ {
		if rs := &bcp.Replsets[i]; cmp(rs.LastWriteTS, lw) {
			lw = rs.LastWriteTS
		}
	}

	err = SetLastWrite(ctx, conn, bcpName, lw)
	return errors.Wrap(err, "set timestamp")
}

func condAll[T any, Cond func(*T) bool](ts []T, ok Cond) bool {
	for i := range ts {
		if !ok(&ts[i]) {
			return false
		}
	}

	return true
}
