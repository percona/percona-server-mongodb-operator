package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	slog "log"
	"math/rand"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mongodb/mongo-tools/common/db"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v2"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/restore/phys"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

const (
	defaultRSdbpath   = "/data/db"
	defaultCSRSdbpath = "/data/configdb"

	mongofslock = "mongod.lock"

	defaultPort = 27017

	tryConnCount   = 5
	tryConnTimeout = 5 * time.Minute
)

type files struct {
	BcpName string
	Cmpr    compress.CompressionType
	Data    []backup.File

	// dbpath to cut from destination if there is any (see PBM-1058)
	dbpath string
}

type PhysRestore struct {
	leadConn connect.Client
	node     *mongo.Client
	dbpath   string
	// an ephemeral port to restart mongod on during the restore
	tmpPort int
	tmpConf *os.File
	rsConf  *topo.RSConfig    // original replset config
	shards  map[string]string // original shards list on config server
	cfgConn string            // shardIdentity configsvrConnectionString
	startTS int64
	secOpts *topo.MongodOptsSec

	name      string
	opid      string
	nodeInfo  *topo.NodeInfo
	stg       storage.Storage
	bcpStg    storage.Storage
	bcp       *backup.BackupMeta
	files     []files
	restoreTS primitive.Timestamp

	confOpts *config.RestoreConf

	mongod string // location of mongod used for internal restarts

	// path to files on a storage the node will sync its
	// state with the resto of the cluster
	syncPathNode     string
	syncPathNodeStat string
	syncPathRS       string
	syncPathCluster  string
	syncPathPeers    map[string]struct{}
	// Shards to participate in restore.
	syncPathShards map[string]struct{}
	// Non-ConfigServer shards
	syncPathDataShards map[string]struct{}

	stopHB chan struct{}

	log log.LogEvent

	rsMap map[string]string
}

func NewPhysical(
	ctx context.Context,
	leadConn connect.Client,
	node *mongo.Client,
	inf *topo.NodeInfo,
	rsMap map[string]string,
) (*PhysRestore, error) {
	opts, err := topo.GetMongodOpts(ctx, node, nil)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo options")
	}
	p := opts.Storage.DBpath
	if p == "" {
		switch inf.ReplsetRole() {
		case topo.RoleConfigSrv:
			p = defaultCSRSdbpath
		default:
			p = defaultRSdbpath
		}
	}

	if opts.Net.Port == 0 {
		opts.Net.Port = defaultPort
	}

	rcf, err := topo.GetReplSetConfig(ctx, node)
	if err != nil {
		return nil, errors.Wrap(err, "get replset config")
	}

	var shards map[string]string
	var csvr string
	if inf.IsSharded() {
		ss, err := topo.ClusterMembers(ctx, leadConn.MongoClient())
		if err != nil {
			return nil, errors.Wrap(err, "get shards")
		}

		shards = make(map[string]string)
		for _, s := range ss {
			shards[s.ID] = s.Host
		}

		if inf.ReplsetRole() != topo.RoleConfigSrv {
			csvr, err = topo.ConfSvrConn(ctx, node)
			if err != nil {
				return nil, errors.Wrap(err, "get configsvrConnectionString")
			}
		}
	}

	if inf.SetName == "" {
		return nil, errors.New("undefined replica set")
	}

	tmpPort, err := peekTmpPort(opts.Net.Port)
	if err != nil {
		return nil, errors.Wrap(err, "peek tmp port")
	}

	return &PhysRestore{
		leadConn: leadConn,
		node:     node,
		dbpath:   p,
		rsConf:   rcf,
		shards:   shards,
		cfgConn:  csvr,
		nodeInfo: inf,
		tmpPort:  tmpPort,
		secOpts:  opts.Security,
		rsMap:    rsMap,
	}, nil
}

// peeks a random free port in a range [minPort, maxPort]
func peekTmpPort(current int) (int, error) {
	const (
		rng = 1111
		try = 150
	)

	for i := 0; i < try; i++ {
		p := current + rand.Intn(rng) + 1
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(p))
		if err == nil {
			ln.Close()
			return p, nil
		}
	}

	return -1, errors.Errorf("can't find unused port in range [%d, %d]", current, current+rng)
}

// Close releases object resources.
// Should be run to avoid leaks.
func (r *PhysRestore) close(noerr, cleanup bool) {
	if r.tmpConf != nil {
		r.log.Debug("rm tmp conf")
		err := os.Remove(r.tmpConf.Name())
		if err != nil {
			r.log.Error("remove tmp config %s: %v", r.tmpConf.Name(), err)
		}
	}
	// clean-up internal mongod log only if there is no error
	if noerr {
		r.log.Debug("rm tmp logs")
		err := os.Remove(path.Join(r.dbpath, internalMongodLog))
		if err != nil {
			r.log.Warning("remove tmp mongod logs %s: %v", path.Join(r.dbpath, internalMongodLog), err)
		}
		extMeta := filepath.Join(r.dbpath,
			fmt.Sprintf(defs.ExternalRsMetaFile, util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)))
		err = os.Remove(extMeta)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			r.log.Warning("remove external rs meta <%s>: %v", extMeta, err)
		}
		err = os.Remove(backup.FilelistName)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			r.log.Warning("remove file <%s>: %v", backup.FilelistName, err)
		}
	} else if cleanup { // clean-up dbpath on err if needed
		r.log.Debug("clean-up dbpath")
		err := removeAll(r.dbpath, r.log)
		if err != nil {
			r.log.Error("flush dbpath %s: %v", r.dbpath, err)
		}
	}
	if r.stopHB != nil {
		close(r.stopHB)
	}
}

func (r *PhysRestore) flush(ctx context.Context) error {
	r.log.Debug("shutdown server")
	rsStat, err := topo.GetReplsetStatus(ctx, r.node)
	if err != nil {
		return errors.Wrap(err, "get replset status")
	}

	if r.nodeInfo.IsConfigSrv() {
		r.log.Debug("waiting for shards to shutdown")
		_, err := r.waitFiles(defs.StatusDown, r.syncPathDataShards, false)
		if err != nil {
			return errors.Wrap(err, "wait for datashards to shutdown")
		}
	}

	for {
		inf, err := topo.GetNodeInfoExt(ctx, r.node)
		if err != nil {
			return errors.Wrap(err, "get node info")
		}
		// single-node replica set won't stepdown do secondary
		// so we have to shut it down despite of role
		if !inf.IsPrimary || len(rsStat.Members) == 1 {
			err = nodeShutdown(ctx, r.node)
			if err != nil &&
				strings.Contains(err.Error(), // wait a bit and let the node to stepdown
					"(ConflictingOperationInProgress) This node is already in the process of stepping down") {
				return errors.Wrap(err, "shutdown server")
			}
			break
		}
		r.log.Debug("waiting to became secondary")
		time.Sleep(time.Second * 1)
	}

	r.log.Debug("waiting for the node to shutdown")
	err = waitMgoShutdown(r.dbpath)
	if err != nil {
		return errors.Wrap(err, "shutdown")
	}

	if r.nodeInfo.IsPrimary {
		err = util.RetryableWrite(r.stg, r.syncPathRS+"."+string(defs.StatusDown), okStatus())
		if err != nil {
			return errors.Wrap(err, "write replset StatusDown")
		}
	}

	r.log.Debug("remove old data")
	err = removeAll(r.dbpath, r.log)
	if err != nil {
		return errors.Wrapf(err, "flush dbpath %s", r.dbpath)
	}

	return nil
}

func nodeShutdown(ctx context.Context, m *mongo.Client) error {
	err := m.Database("admin").RunCommand(ctx, bson.D{{"shutdown", 1}}).Err()
	if err == nil || strings.Contains(err.Error(), "socket was unexpectedly closed") {
		return nil
	}
	return err
}

func waitMgoShutdown(dbpath string) error {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()

	for range tk.C {
		f, err := os.Stat(path.Join(dbpath, mongofslock))
		if err != nil {
			return errors.Wrapf(err, "check for lock file %s", path.Join(dbpath, mongofslock))
		}

		if f.Size() == 0 {
			return nil
		}
	}

	return nil
}

// waitToBecomePrimary pause execution until RS member becomes primary node.
// Error is returned in case of timeout.
// Unexpected error while getting node info is just logged.
func (r *PhysRestore) waitToBecomePrimary(ctx context.Context, m *mongo.Client) error {
	tk := time.NewTicker(time.Second)
	defer tk.Stop()

	tout := time.NewTimer(2 * time.Minute)
	defer tout.Stop()

	for {
		select {
		case <-tk.C:
			inf, err := topo.GetNodeInfo(ctx, m)
			if err != nil {
				r.log.Debug("get node info error while waiting to become primary: %v", err)
				continue
			}

			if inf.IsPrimary {
				return nil
			}
			r.log.Debug("node: %s is still not primary, waiting for another cycle", inf.Me)

		case <-tout.C:
			return errors.New("timeout while waiting the node to become primary")

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// toState moves cluster to the given restore state.
// All communication happens via files in the restore dir on storage.
//
//		Status "done" is a special case. If at least one node in the replset moved
//		to the "done", the replset is "partlyDone". And a replset is "done" if all
//		nodes moved to "done". For cluster success, all replsets must move either
//		to "done" or "partlyDone". Cluster is "partlyDone" if at least one replset
//		is "partlyDone".
//
//	  - Each node writes a file with the given state.
//	  - The replset leader (primary node) or every rs node, in case of status
//	    "done",  waits for files from all replica set nodes. And writes a status
//	    file for the replica set.
//	  - The cluster leader (primary node - on config server in case of sharded) or
//	    every node, in case of status "done",  waits for status files from all
//	    replica sets. And sets the status file for the cluster.
//	  - Each node in turn waits for the cluster status file and returns (move further)
//	    once it's observed.
//
// State structure on storage:
//
//		.pbm.restore/<restore-name>
//			rs.<rs-name>/
//				node.<node-name>.hb			// hearbeats. last beat ts inside.
//				node.<node-name>.<status>	// node's PBM status. Inside is the ts of the transition. In case of error, file contains an error text.
//				rs.<status>					// replicaset's PBM status. Inside is the ts of the transition. In case of error, file contains an error text.
//			cluster.hb						// hearbeats. last beat ts inside.
//			cluster.<status>				// cluster's PBM status. Inside is the ts of the transition. In case of error, file contains an error text.
//
//	 For example:
//
//	     2022-08-02T18:50:35.1889332Z
//	     ├── cluster.done
//	     ├── cluster.hb
//	     ├── cluster.running
//	     ├── cluster.starting
//	     ├── rs.rs1
//	     │   ├── node.rs101:27017.done
//	     │   ├── node.rs101:27017.hb
//	     │   ├── node.rs101:27017.running
//	     │   ├── node.rs101:27017.starting
//	     │   ├── node.rs102:27017.done
//	     │   ├── node.rs102:27017.hb
//	     │   ├── node.rs102:27017.running
//	     │   ├── node.rs102:27017.starting
//	     │   ├── node.rs103:27017.done
//	     │   ├── node.rs103:27017.hb
//	     │   ├── node.rs103:27017.running
//	     │   ├── node.rs103:27017.starting
//	     │   ├── rs.done
//	     │   ├── rs.hb
//	     │   ├── rs.running
//	     │   └── rs.starting
//
//nolint:lll,nonamedreturns
func (r *PhysRestore) toState(status defs.Status) (_ defs.Status, err error) {
	defer func() {
		if err != nil {
			if r.nodeInfo.IsPrimary && status != defs.StatusDone {
				serr := util.RetryableWrite(r.stg,
					r.syncPathRS+"."+string(defs.StatusError), errStatus(err))
				if serr != nil {
					r.log.Error("toState: write replset error state `%v`: %v", err, serr)
				}
			}
			if r.nodeInfo.IsClusterLeader() && status != defs.StatusDone {
				serr := util.RetryableWrite(r.stg,
					r.syncPathCluster+"."+string(defs.StatusError), errStatus(err))
				if serr != nil {
					r.log.Error("toState: write cluster error state `%v`: %v", err, serr)
				}
			}
		}
	}()

	r.log.Info("moving to state %s", status)

	err = util.RetryableWrite(r.stg, r.syncPathNode+"."+string(status), okStatus())
	if err != nil {
		return defs.StatusError, errors.Wrap(err, "write node state")
	}

	if r.nodeInfo.IsPrimary || status == defs.StatusDone {
		r.log.Info("waiting for `%s` status in rs %v", status, r.syncPathPeers)
		cstat, err := r.waitFiles(status, copyMap(r.syncPathPeers), false)
		if err != nil {
			return defs.StatusError, errors.Wrap(err, "wait for nodes in rs")
		}

		err = util.RetryableWrite(r.stg, r.syncPathRS+"."+string(cstat), okStatus())
		if err != nil {
			return defs.StatusError, errors.Wrap(err, "write replset state")
		}
	}

	if r.nodeInfo.IsClusterLeader() || status == defs.StatusDone {
		r.log.Info("waiting for shards %v", r.syncPathShards)
		cstat, err := r.waitFiles(status, copyMap(r.syncPathShards), true)
		if err != nil {
			return defs.StatusError, errors.Wrap(err, "wait for shards")
		}

		err = util.RetryableWrite(r.stg, r.syncPathCluster+"."+string(cstat), okStatus())
		if err != nil {
			return defs.StatusError, errors.Wrap(err, "write cluster state")
		}
	}

	r.log.Info("waiting for cluster")
	cstat, err := r.waitFiles(status, map[string]struct{}{r.syncPathCluster: {}}, true)
	if err != nil {
		return defs.StatusError, errors.Wrap(err, "wait for cluster")
	}

	r.log.Debug("converged to state %s", cstat)

	return cstat, nil
}

func (r *PhysRestore) getTSFromSyncFile(path string) (primitive.Timestamp, error) {
	res, err := r.stg.SourceReader(path + "." + string(defs.StatusExtTS))
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "get timestamp")
	}
	b, err := io.ReadAll(res)
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "read timestamp")
	}
	tsb := bytes.Split(b, []byte(":"))
	if len(tsb) != 2 {
		return primitive.Timestamp{}, errors.Errorf("wrong file format: %s", tsb)
	}
	tsparts := bytes.Split(tsb[1], []byte(","))
	if len(tsparts) != 2 {
		return primitive.Timestamp{}, errors.Errorf("wrong timestamp format: %s", tsparts)
	}
	ctsT, err := strconv.Atoi(string(tsparts[0]))
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "parse ts.T")
	}
	ctsI, err := strconv.Atoi(string(tsparts[1]))
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "parse ts.I")
	}

	return primitive.Timestamp{
		T: uint32(ctsT),
		I: uint32(ctsI),
	}, nil
}

func errStatus(err error) []byte {
	return []byte(fmt.Sprintf("%d:%v", time.Now().Unix(), err))
}

func okStatus() []byte {
	return []byte(fmt.Sprintf("%d", time.Now().Unix()))
}

type nodeError struct {
	node string
	msg  string
}

func (n nodeError) Error() string {
	return fmt.Sprintf("%s failed: %s", n.node, n.msg)
}

func copyMap[K comparable, V any](m map[K]V) map[K]V {
	cp := make(map[K]V)
	for k, v := range m {
		cp[k] = v
	}

	return cp
}

func (r *PhysRestore) waitFiles(
	status defs.Status,
	objs map[string]struct{},
	cluster bool,
) (defs.Status, error) {
	if len(objs) == 0 {
		return defs.StatusError, errors.New("empty objects maps")
	}

	tk := time.NewTicker(time.Second * 5)
	defer tk.Stop()

	retStatus := status

	var curErr error
	var haveDone bool
	for range tk.C {
		for f := range objs {
			errFile := f + "." + string(defs.StatusError)
			_, err := r.stg.FileStat(errFile)
			if err != nil && !errors.Is(err, storage.ErrNotExist) {
				return defs.StatusError, errors.Wrapf(err, "get file %s", errFile)
			}

			if err == nil {
				r, err := r.stg.SourceReader(errFile)
				if err != nil {
					return defs.StatusError, errors.Wrapf(err, "open error file %s", errFile)
				}

				b, err := io.ReadAll(r)
				r.Close()
				if err != nil {
					return defs.StatusError, errors.Wrapf(err, "read error file %s", errFile)
				}
				if status != defs.StatusDone {
					return defs.StatusError, nodeError{filepath.Base(f), string(b)}
				}
				curErr = nodeError{filepath.Base(f), string(b)}
				delete(objs, f)
				continue
			}

			err = r.checkHB(f + "." + syncHbSuffix)
			if err != nil {
				curErr = errors.Wrapf(err, "check heartbeat in %s.%s", f, syncHbSuffix)
				if status != defs.StatusDone {
					return defs.StatusError, curErr
				}
				delete(objs, f)
				continue
			}

			ok, err := checkFile(f+"."+string(status), r.stg)
			if err != nil {
				return defs.StatusError, errors.Wrapf(err, "check file %s", f+"."+string(status))
			}

			if !ok {
				if status != defs.StatusDone {
					continue
				}

				ok, err := checkFile(f+"."+string(defs.StatusPartlyDone), r.stg)
				if err != nil {
					return defs.StatusError, errors.Wrapf(err,
						"check file %s", f+"."+string(defs.StatusPartlyDone))
				}

				if !ok {
					continue
				}
				retStatus = defs.StatusPartlyDone
			}

			haveDone = true
			delete(objs, f)
		}

		if len(objs) == 0 {
			if curErr == nil {
				return retStatus, nil
			}

			if haveDone && !cluster {
				return defs.StatusPartlyDone, nil
			}

			return defs.StatusError, curErr
		}
	}

	return defs.StatusError, storage.ErrNotExist
}

func checkFile(f string, stg storage.Storage) (bool, error) {
	_, err := stg.FileStat(f)

	if err == nil {
		return true, nil
	}

	if errors.Is(err, storage.ErrNotExist) || errors.Is(err, storage.ErrEmpty) {
		return false, nil
	}

	return false, err
}

type nodeStatus int

const (
	restoreStared nodeStatus = 1 << iota
	restoreDone
)

func (n nodeStatus) is(s nodeStatus) bool { return n&s != 0 }

// log buffer that will dump content to the storage on restore
// finish (whether it's successful or not). It also dumps content
// and reset buffer when logs size hist the limit.
type logBuff struct {
	buf   *bytes.Buffer
	path  string
	cnt   int
	write func(name string, data io.Reader) error
	limit int64
	mx    sync.Mutex
}

func (l *logBuff) Write(p []byte) (int, error) {
	l.mx.Lock()
	defer l.mx.Unlock()

	if l.buf.Len()+len(p) > int(l.limit) {
		err := l.flush()
		if err != nil {
			return 0, err
		}
	}

	return l.buf.Write(p)
}

func (l *logBuff) flush() error {
	fname := fmt.Sprintf("%s.%d.log", l.path, l.cnt)
	err := l.write(fname, l.buf)
	if err != nil {
		return errors.Wrapf(err, "write logs buffer to %s", fname)
	}
	l.buf.Reset()
	l.cnt++

	return nil
}

func (l *logBuff) Flush() error {
	l.mx.Lock()
	defer l.mx.Unlock()

	return l.flush()
}

// Snapshot restores data from the physical snapshot.
//
// Initial sync and coordination between nodes happens via `admin.pbmRestore`
// metadata as with logical restores. But later, since mongod being shutdown,
// status sync going via storage (see `PhysRestore.toState`)
//
// Unlike in logical restore, _every_ node of the replicaset is taking part in
// a physical restore. In that way, we avoid logical resync between nodes after
// the restore. Each node in the cluster does:
//   - Stores current replicset config and mongod port.
//   - Checks backup and stops all routine writes to the db.
//   - Stops mongod and wipes out datadir.
//   - Copies backup data to the datadir.
//   - Starts standalone mongod on ephemeral (tmp) port and reset some internal
//     data also setting one-node replicaset and sets oplogTruncateAfterPoint
//     to the backup's `last write`. `oplogTruncateAfterPoint` set the time up
//     to which journals would be replayed.
//   - Starts standalone mongod to recover oplog from journals.
//   - Cleans up data and resets replicaset config to the working state.
//   - Shuts down mongod and agent (the leader also dumps metadata to the storage).
//
// An External restore doesn't need to have specified backup. It will look
// for the replset metadata in the datadir after data is copied. And take
// all needed inputs from there (restoreTS, files list, and mongod config).
// The user may also specify restoreTS and a mongod config via CLI. The priority:
// - CLI provided values
// - replset metada in the datadir
// - backup meta
//
//nolint:nonamedreturns
func (r *PhysRestore) Snapshot(
	ctx context.Context,
	cmd *ctrl.RestoreCmd,
	pitr primitive.Timestamp,
	opid ctrl.OPID,
	l log.LogEvent,
	stopAgentC chan<- struct{},
	pauseHB func(),
) (err error) {
	l.Debug("port: %d", r.tmpPort)

	meta := &RestoreMeta{
		Type:     defs.PhysicalBackup,
		OPID:     opid.String(),
		Name:     cmd.Name,
		Backup:   cmd.BackupName,
		StartTS:  time.Now().Unix(),
		Status:   defs.StatusInit,
		Replsets: []RestoreReplset{{Name: r.nodeInfo.Me}},
	}
	if r.nodeInfo.IsClusterLeader() {
		meta.Leader = r.nodeInfo.Me + "/" + r.rsConf.ID
	}

	var progress nodeStatus
	defer func() {
		// set failed status of node on error, but
		// don't mark node as failed after the local restore succeed
		if err != nil && !progress.is(restoreDone) && !errors.Is(err, ErrNoDataForShard) {
			r.MarkFailed(meta, err, !progress.is(restoreStared))
		}

		r.close(err == nil, progress.is(restoreStared) && !progress.is(restoreDone))
	}()

	err = r.init(ctx, cmd.Name, opid, l)
	if err != nil {
		return errors.Wrap(err, "init")
	}

	if cmd.BackupName == "" && !cmd.External {
		return errors.New("restore isn't external and no backup set")
	}

	if cmd.BackupName != "" {
		err = r.prepareBackup(ctx, cmd.BackupName)
		if err != nil {
			return err
		}
		r.restoreTS = r.bcp.LastWriteTS
	}
	if cmd.ExtTS.T > 0 {
		r.restoreTS = cmd.ExtTS
	}
	if cmd.External {
		meta.Type = defs.ExternalBackup
	} else {
		meta.Type = r.bcp.Type
	}

	var oplogRanges []oplogRange
	if !pitr.IsZero() {
		chunks, err := chunks(ctx, r.leadConn, r.stg, r.restoreTS, pitr, r.rsConf.ID, r.rsMap)
		if err != nil {
			return err
		}

		oplogRanges = append(oplogRanges, oplogRange{chunks: chunks, storage: r.stg})
	}

	if meta.Type == defs.IncrementalBackup {
		meta.BcpChain = make([]string, 0, len(r.files))
		for i := len(r.files) - 1; i >= 0; i-- {
			meta.BcpChain = append(meta.BcpChain, r.files[i].BcpName)
		}
	}

	_, err = r.toState(defs.StatusStarting)
	if err != nil {
		return errors.Wrap(err, "move to running state")
	}
	l.Debug("%s", defs.StatusStarting)

	// don't write logs to the mongo anymore
	// but dump it on storage
	logger := log.FromContext(ctx)
	logger.SefBuffer(&logBuff{
		buf:   &bytes.Buffer{},
		path:  fmt.Sprintf("%s/%s/rs.%s/log/%s", defs.PhysRestoresDir, r.name, r.rsConf.ID, r.nodeInfo.Me),
		limit: 1 << 20, // 1Mb
		write: func(name string, data io.Reader) error { return r.stg.Save(name, data, -1) },
	})
	logger.PauseMgo()

	_, err = r.toState(defs.StatusRunning)
	if err != nil {
		return errors.Wrapf(err, "moving to state %s", defs.StatusRunning)
	}

	// On this stage, the agent has to be closed on any outcome as mongod
	// is gonna be turned off. Besides, the agent won't be able to listen to
	// the cmd stream anymore and will flood logs with errors on that.
	l.Info("send to stopAgent chan")
	if stopAgentC != nil {
		stopAgentC <- struct{}{}
	}
	// anget will be stopped only after we exit this func
	// so stop heartbeats not to spam logs while the restore is running
	l.Debug("stop agents heartbeats")
	pauseHB()

	l.Info("stopping mongod and flushing old data")
	err = r.flush(ctx)
	if err != nil {
		return err
	}

	// A point of no return. From now on, we should clean the dbPath if an
	// error happens before the node is restored successfully.
	// The mongod most probably won't start anyway (or will start in an
	// inconsistent state). But the clean path will allow starting the cluster
	// and doing InitialSync on this node should the restore succeed on other
	// nodes.
	//
	// Should not be set before `r.flush()` as `flush` cleans the dbPath on its
	// own (which sets the no-return point).
	progress |= restoreStared

	var excfg *topo.MongodOpts
	var stats phys.RestoreShardStat

	if cmd.External {
		_, err = r.toState(defs.StatusCopyReady)
		if err != nil {
			return errors.Wrapf(err, "moving to state %s", defs.StatusCopyReady)
		}

		l.Info("waiting for the datadir to be copied")
		_, err := r.waitFiles(defs.StatusCopyDone, map[string]struct{}{r.syncPathCluster: {}}, true)
		if err != nil {
			return errors.Wrapf(err, "check %s state", defs.StatusCopyDone)
		}

		// try to read replset meta from the backup and use its data
		setName := util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)
		rsMetaFilename := fmt.Sprintf(defs.ExternalRsMetaFile, setName)
		rsMetaF := filepath.Join(r.dbpath, rsMetaFilename)
		conff, err := os.Open(rsMetaF)
		var needFiles []backup.File
		if err == nil {
			rsMeta := &backup.BackupReplset{}
			err := json.NewDecoder(conff).Decode(rsMeta)
			if err != nil {
				return errors.Wrap(err, "decode replset meta from the backup")
			}
			l.Debug("got rs meta from the backup")
			if r.restoreTS.T == 0 {
				r.restoreTS = rsMeta.LastWriteTS
			}
			excfg = rsMeta.MongodOpts

			if version.HasFilelistFile(rsMeta.PBMVersion) {
				filelistPath := filepath.Join(r.dbpath, backup.FilelistName)
				f, err := os.Open(filelistPath)
				if err != nil {
					return errors.Wrapf(err, "open filelist %q", filelistPath)
				}
				defer f.Close()

				filelist, err := backup.ReadFilelist(f)
				f.Close()
				if err != nil {
					return errors.Wrap(err, "parse filelist")
				}

				rsMeta.Files = filelist
			}

			needFiles = rsMeta.Files
		} else {
			l.Info("open replset metadata file <%s>: %v. Continue without.", rsMetaF, err)
		}

		err = r.cleanupDatadir(needFiles)
		if err != nil {
			return errors.Wrap(err, "cleanup datadir")
		}
	} else {
		l.Info("copying backup data")
		stats.D, err = r.copyFiles()
		if err != nil {
			return errors.Wrap(err, "copy files")
		}
	}

	if o, ok := cmd.ExtConf[r.nodeInfo.SetName]; ok {
		excfg = &o
	}
	err = r.setTmpConf(excfg)
	if err != nil {
		return errors.Wrap(err, "set tmp config")
	}

	if r.restoreTS.T == 0 {
		l.Info("restore timestamp isn't set, get latest common ts for the cluster")
		r.restoreTS, err = r.agreeCommonRestoreTS()
		if err != nil {
			return errors.Wrap(err, "get common restore timestamp")
		}
	}

	l.Info("preparing data")
	err = r.prepareData()
	if err != nil {
		return errors.Wrap(err, "prepare data")
	}

	l.Info("recovering oplog as standalone")
	err = r.recoverStandalone()
	if err != nil {
		return errors.Wrap(err, "recover oplog as standalone")
	}

	if !pitr.IsZero() && r.nodeInfo.IsPrimary {
		l.Info("replaying pitr oplog")
		err = r.replayOplog(r.bcp.LastWriteTS, pitr, oplogRanges, &stats)
		if err != nil {
			return errors.Wrap(err, "replay pitr oplog")
		}
	}

	l.Info("clean-up and reset replicaset config")
	err = r.resetRS()
	if err != nil {
		return errors.Wrap(err, "clean-up, rs_reset")
	}

	l.Info("restore on node succeed")
	// The node at this stage was restored successfully, so we shouldn't
	// clean up dbPath nor write error status for the node whatever happens
	// next.
	progress |= restoreDone

	stat, err := r.toState(defs.StatusDone)
	if err != nil {
		return errors.Wrapf(err, "moving to state %s", defs.StatusDone)
	}

	err = r.writeStat(stats)
	if err != nil {
		r.log.Warning("write download stat: %v", err)
	}

	r.log.Info("writing restore meta")
	err = r.dumpMeta(meta, stat, "")
	if err != nil {
		return errors.Wrap(err, "writing restore meta to storage")
	}

	return nil
}

var rmFromDatadir = map[string]struct{}{
	"WiredTiger.lock":    {},
	"WiredTiger.turtle":  {},
	"WiredTiger.wt":      {},
	"mongod.lock":        {},
	"ongoingBackup.lock": {},
}

// removes obsolete files from the datadir
func (r *PhysRestore) cleanupDatadir(bcpFiles []backup.File) error {
	var rm func(f string) bool

	needFiles := bcpFiles
	if needFiles == nil && r.bcp != nil {
		rs := getRS(r.bcp, r.nodeInfo.SetName)
		if rs != nil {
			needFiles = rs.Files
		}
	}

	if needFiles != nil {
		m := make(map[string]struct{})
		for _, f := range needFiles {
			m[f.Name] = struct{}{}
		}
		rm = func(f string) bool {
			_, ok := m[f]
			return !ok
		}
	} else {
		rm = func(f string) bool {
			_, ok := rmFromDatadir[f]
			return ok
		}
	}

	dbpath := path.Clean(r.dbpath) + "/"
	return filepath.Walk(dbpath, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return errors.Wrap(err, "walking the path")
		}
		if info.IsDir() || !rm(strings.TrimPrefix(p, dbpath)) {
			return nil
		}

		r.log.Debug("rm %s", p)
		err = os.Remove(p)
		if err != nil {
			r.log.Error("datadir cleanup: remove %s: %v", p, err)
		}
		return nil
	})
}

func (r *PhysRestore) writeStat(stat any) error {
	b, err := json.Marshal(stat)
	if err != nil {
		return errors.Wrap(err, "marshal")
	}

	err = util.RetryableWrite(r.stg, r.syncPathNodeStat, b)
	if err != nil {
		return errors.Wrap(err, "write")
	}

	return nil
}

func (r *PhysRestore) dumpMeta(meta *RestoreMeta, s defs.Status, msg string) error {
	name := fmt.Sprintf("%s/%s.json", defs.PhysRestoresDir, meta.Name)
	_, err := r.stg.FileStat(name)
	if err == nil {
		r.log.Warning("meta `%s` already exists, trying write %s status with '%s'", name, s, msg)
		return nil
	}
	if !errors.Is(err, storage.ErrNotExist) {
		return errors.Wrapf(err, "check restore meta `%s`", name)
	}

	// We'll try to build as accurate meta as possible but it won't
	// be 100% accurate as not all agents have reported its final state yet
	// The meta generated here is more for debugging porpuses (just in case).
	// `pbm status` and `resync` will always rebuild it from agents' reports
	// not relying solely on this file.
	condsm, err := GetPhysRestoreMeta(meta.Name, r.stg, r.log)
	if err == nil {
		meta.Replsets = condsm.Replsets
		meta.Status = condsm.Status
		meta.LastTransitionTS = condsm.LastTransitionTS
		meta.Error = condsm.Error
		meta.Hb = condsm.Hb
		meta.Conditions = condsm.Conditions
	}
	if err != nil || s == defs.StatusError {
		ts := time.Now().Unix()
		meta.Status = s
		meta.Conditions = append(meta.Conditions, &Condition{Timestamp: ts, Status: s})
		meta.LastTransitionTS = ts
		meta.Error = fmt.Sprintf("%s/%s: %s", r.nodeInfo.SetName, r.nodeInfo.Me, msg)
	}

	buf, err := json.MarshalIndent(meta, "", "\t")
	if err != nil {
		return errors.Wrap(err, "encode restore meta")
	}
	err = util.RetryableWrite(r.stg, name, buf)
	if err != nil {
		return errors.Wrap(err, "write restore meta")
	}

	return nil
}

func (r *PhysRestore) copyFiles() (*s3.DownloadStat, error) {
	var stat *s3.DownloadStat
	readFn := r.bcpStg.SourceReader
	if t, ok := r.bcpStg.(*s3.S3); ok {
		d := t.NewDownload(r.confOpts.NumDownloadWorkers, r.confOpts.MaxDownloadBufferMb, r.confOpts.DownloadChunkMb)
		readFn = d.SourceReader

		defer func() {
			s := d.Stat()
			stat = &s

			r.log.Debug("download stat: %s", s)
		}()
	}

	setName := util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)
	cpbuf := make([]byte, 32*1024)
	for i := len(r.files) - 1; i >= 0; i-- {
		set := r.files[i]
		for _, f := range set.Data {
			src := filepath.Join(set.BcpName, setName, f.Path(set.Cmpr))
			// cut dbpath from destination if there is any (see PBM-1058)
			fname := f.Name
			if set.dbpath != "" {
				fname = strings.TrimPrefix(fname, set.dbpath)
			}
			dst := filepath.Join(r.dbpath, fname)

			err := os.MkdirAll(filepath.Dir(dst), os.ModeDir|0o700)
			if err != nil {
				return stat, errors.Wrapf(err, "create path %s", filepath.Dir(dst))
			}
			// if this is a directory, only ensure it is created.
			if set.BcpName == bcpDir {
				r.log.Info("create dir <%s>", filepath.Dir(f.Name))
				continue
			}

			r.log.Info("copy <%s> to <%s>", src, dst)
			sr, err := readFn(src)
			if err != nil {
				return stat, errors.Wrapf(err, "create source reader for <%s>", src)
			}
			defer sr.Close()

			data, err := compress.Decompress(sr, set.Cmpr)
			if err != nil {
				return stat, errors.Wrapf(err, "decompress object %s", src)
			}
			defer data.Close()

			fw, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE, f.Fmode)
			if err != nil {
				return stat, errors.Wrapf(err, "create/open destination file <%s>", dst)
			}
			defer fw.Close()

			if f.Off != 0 {
				_, err := fw.Seek(f.Off, io.SeekStart)
				if err != nil {
					return stat, errors.Wrapf(err, "set file offset <%s>|%d", dst, f.Off)
				}
			}

			_, err = io.CopyBuffer(fw, data, cpbuf)
			if err != nil {
				return stat, errors.Wrapf(err, "copy file <%s>", dst)
			}

			if f.Size != 0 {
				err = fw.Truncate(f.Size)
				if err != nil {
					return stat, errors.Wrapf(err, "truncate file <%s>|%d", dst, f.Size)
				}
			}
		}
	}
	return stat, nil
}

func (r *PhysRestore) getLasOpTime() (primitive.Timestamp, error) {
	err := r.startMongo("--dbpath", r.dbpath,
		"--setParameter", "disableLogicalSessionCacheRefresh=true")
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "start mongo")
	}

	c, err := tryConn(r.tmpPort, path.Join(r.dbpath, internalMongodLog))
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "connect to mongo")
	}

	ctx := context.TODO()

	res := c.Database("local").Collection("oplog.rs").FindOne(
		ctx,
		bson.M{},
		options.FindOne().SetSort(bson.D{{"ts", -1}}),
	)
	if res.Err() != nil {
		return primitive.Timestamp{}, errors.Wrap(res.Err(), "get oplog entry")
	}
	rb, err := res.Raw()
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "decode oplog entry")
	}
	ts := primitive.Timestamp{}
	var ok bool
	ts.T, ts.I, ok = rb.Lookup("ts").TimestampOK()
	if !ok {
		return ts, errors.Errorf("get the timestamp of record %v", rb)
	}

	err = r.shutdown(c)
	return ts, err
}

func (r *PhysRestore) prepareData() error {
	err := r.startMongo("--dbpath", r.dbpath,
		"--setParameter", "disableLogicalSessionCacheRefresh=true")
	if err != nil {
		return errors.Wrap(err, "start mongo")
	}

	c, err := tryConn(r.tmpPort, path.Join(r.dbpath, internalMongodLog))
	if err != nil {
		return errors.Wrap(err, "connect to mongo")
	}

	ctx := context.Background()

	_, err = c.Database("local").Collection("replset.minvalid").DeleteMany(ctx, bson.D{})
	if err != nil {
		return errors.Wrap(err, "drop replset.minvalid")
	}
	_, err = c.Database("local").Collection("replset.oplogTruncateAfterPoint").DeleteMany(ctx, bson.D{})
	if err != nil {
		return errors.Wrap(err, "drop replset.oplogTruncateAfterPoint")
	}
	_, err = c.Database("local").Collection("replset.election").DeleteMany(ctx, bson.D{})
	if err != nil {
		return errors.Wrap(err, "drop replset.election")
	}
	_, err = c.Database("local").Collection("system.replset").DeleteMany(ctx, bson.D{})
	if err != nil {
		return errors.Wrap(err, "delete from system.replset")
	}

	_, err = c.Database("local").Collection("replset.minvalid").InsertOne(ctx,
		bson.M{"_id": primitive.NewObjectID(), "t": -1, "ts": primitive.Timestamp{0, 1}},
	)
	if err != nil {
		return errors.Wrap(err, "insert to replset.minvalid")
	}

	r.log.Debug("oplogTruncateAfterPoint: %v", r.restoreTS)
	_, err = c.Database("local").Collection("replset.oplogTruncateAfterPoint").InsertOne(ctx,
		bson.M{"_id": "oplogTruncateAfterPoint", "oplogTruncateAfterPoint": r.restoreTS},
	)
	if err != nil {
		return errors.Wrap(err, "set oplogTruncateAfterPoint")
	}

	return r.shutdown(c)
}

func (r *PhysRestore) shutdown(c *mongo.Client) error {
	err := shutdownImpl(c, r.dbpath, false)
	if err != nil {
		if strings.Contains(err.Error(), "ConflictingOperationInProgress") {
			r.log.Warning("try force shutdown. reason: %v", err)
			err = shutdownImpl(c, r.dbpath, true)
			return errors.Wrap(err, "force shutdown mongo")
		}

		return errors.Wrap(err, "shutdown mongo") // unexpected
	}

	return nil
}

func shutdownImpl(c *mongo.Client, dbpath string, force bool) error {
	res := c.Database("admin").RunCommand(context.TODO(),
		bson.D{{"shutdown", 1}, {"force", force}})
	err := res.Err()
	if err != nil && !strings.Contains(err.Error(), "socket was unexpectedly closed") {
		return errors.Wrapf(err, "run shutdown (force: %v)", force)
	}

	err = waitMgoShutdown(dbpath)
	if err != nil {
		return errors.Wrap(err, "wait")
	}

	return nil
}

func (r *PhysRestore) recoverStandalone() error {
	err := r.startMongo("--dbpath", r.dbpath,
		"--setParameter", "recoverFromOplogAsStandalone=true",
		"--setParameter", "takeUnstableCheckpointOnShutdown=true")
	if err != nil {
		return errors.Wrap(err, "start mongo")
	}

	c, err := tryConn(r.tmpPort, path.Join(r.dbpath, internalMongodLog))
	if err != nil {
		return errors.Wrap(err, "connect to mongo")
	}

	return r.shutdown(c)
}

func (r *PhysRestore) replayOplog(
	from primitive.Timestamp,
	to primitive.Timestamp,
	oplogRanges []oplogRange,
	stat *phys.RestoreShardStat,
) error {
	err := r.startMongo("--dbpath", r.dbpath,
		"--setParameter", "disableLogicalSessionCacheRefresh=true")
	if err != nil {
		return errors.Wrap(err, "start mongo")
	}

	nodeConn, err := tryConn(r.tmpPort, path.Join(r.dbpath, internalMongodLog))
	if err != nil {
		return errors.Wrap(err, "connect to mongo")
	}

	ctx := context.Background()
	_, err = nodeConn.Database("local").Collection("system.replset").InsertOne(ctx,
		topo.RSConfig{
			ID:      r.rsConf.ID,
			CSRS:    r.nodeInfo.IsConfigSrv(),
			Version: 1,
			Members: []topo.RSMember{{
				ID:           0,
				Host:         "localhost:" + strconv.Itoa(r.tmpPort),
				Votes:        1,
				Priority:     1,
				BuildIndexes: true,
			}},
		},
	)
	if err != nil {
		return errors.Wrapf(err, "update rs.member host to %s", r.nodeInfo.Me)
	}

	err = r.shutdown(nodeConn)
	if err != nil {
		return errors.Wrap(err, "after update member host")
	}

	flags := []string{
		"--dbpath", r.dbpath,
		"--setParameter", "disableLogicalSessionCacheRefresh=true",
		"--setParameter", "takeUnstableCheckpointOnShutdown=true",
		"--replSet", r.rsConf.ID,
	}
	if r.nodeInfo.IsConfigSrv() {
		flags = append(flags, "--configsvr")
	}
	err = r.startMongo(flags...)
	if err != nil {
		return errors.Wrap(err, "start mongo as rs")
	}

	nodeConn, err = tryConn(r.tmpPort, path.Join(r.dbpath, internalMongodLog))
	if err != nil {
		return errors.Wrap(err, "connect to mongo rs")
	}

	mgoV, err := version.GetMongoVersion(ctx, nodeConn)
	if err != nil || len(mgoV.Version) < 1 {
		return errors.Wrap(err, "define mongo version")
	}

	err = r.waitToBecomePrimary(ctx, nodeConn)
	if err != nil {
		return errors.Wrap(err, "wait to become primary before applying oplog")
	}

	oplogOption := applyOplogOption{
		start:  &from,
		end:    &to,
		unsafe: true,
	}
	partial, err := applyOplog(ctx,
		nodeConn,
		oplogRanges,
		&oplogOption,
		r.nodeInfo.IsSharded(),
		nil,
		r.setcommittedTxn,
		r.getcommittedTxn,
		&stat.Txn,
		&mgoV)
	if err != nil {
		return errors.Wrap(err, "reply oplog")
	}
	if len(partial) > 0 {
		tops := []db.Oplog{}
		for _, t := range partial {
			tops = append(tops, t.Oplog...)
		}

		buf, err := json.Marshal(tops)
		if err != nil {
			return errors.Wrap(err, "encode")
		}
		err = util.RetryableWrite(r.stg, r.syncPathRS+".partTxn", buf)
		if err != nil {
			return errors.Wrap(err, "write partial transactions")
		}
	}

	return r.shutdown(nodeConn)
}

func (r *PhysRestore) resetRS() error {
	err := r.startMongo("--dbpath", r.dbpath,
		"--setParameter", "disableLogicalSessionCacheRefresh=true",
		"--setParameter", "skipShardingConfigurationChecks=true")
	if err != nil {
		return errors.Wrap(err, "start mongo")
	}

	c, err := tryConn(r.tmpPort, path.Join(r.dbpath, internalMongodLog))
	if err != nil {
		return errors.Wrap(err, "connect to mongo")
	}

	ctx := context.TODO()

	if r.nodeInfo.IsConfigSrv() {
		_, err = c.Database("config").Collection("mongos").DeleteMany(ctx, bson.D{})
		if err != nil {
			return errors.Wrap(err, "drop config.mongos")
		}
		_, err = c.Database("config").Collection("lockpings").DeleteMany(ctx, bson.D{})
		if err != nil {
			return errors.Wrap(err, "drop config.lockpings")
		}

		cur, err := c.Database("config").Collection("shards").Find(ctx, bson.D{})
		if err != nil {
			return errors.Wrap(err, "find: config.shards")
		}

		var docs []struct {
			I string         `bson:"_id"`
			H string         `bson:"host"`
			R map[string]any `bson:",inline"`
		}
		if err := cur.All(ctx, &docs); err != nil {
			return errors.Wrap(err, "decode: config.shards")
		}

		sMap := r.getShardMapping(r.bcp)
		mapS := util.MakeRSMapFunc(sMap)
		ms := []mongo.WriteModel{&mongo.DeleteManyModel{Filter: bson.D{}}}
		for _, doc := range docs {
			doc.I = mapS(doc.I)
			doc.H = r.shards[doc.I]
			ms = append(ms, &mongo.InsertOneModel{Document: doc})
		}

		_, err = c.Database("config").Collection("shards").BulkWrite(ctx, ms)
		if err != nil {
			return errors.Wrap(err, "update config.shards")
		}

		if len(sMap) != 0 {
			r.log.Debug("updating router config")
			if err := updateRouterTables(ctx, connect.UnsafeClient(c), sMap); err != nil {
				return errors.Wrap(err, "update router tables")
			}
		}
	} else {
		var currS string
		for s, uri := range r.shards {
			rs, _, _ := strings.Cut(uri, "/")
			if rs == r.nodeInfo.SetName {
				currS = s
				break
			}
		}

		_, err = c.Database("admin").Collection("system.version").UpdateOne(
			ctx,
			bson.D{{"_id", "shardIdentity"}},
			bson.D{{"$set", bson.M{
				"shardName":                 currS,
				"configsvrConnectionString": r.cfgConn,
			}}},
		)
		if err != nil {
			return errors.Wrap(err, "update shardIdentity in admin.system.version")
		}
	}

	colls, err := c.Database("config").ListCollectionNames(ctx, bson.D{{"name", bson.M{"$regex": `^cache\.`}}})
	if err != nil {
		return errors.Wrap(err, "list cache collections")
	}
	for _, coll := range colls {
		_, err := c.Database("config").Collection(coll).DeleteMany(ctx, bson.D{})
		if err != nil {
			return errors.Wrapf(err, "drop %q", coll)
		}
	}

	const retry = 5
	for i := 0; i < retry; i++ {
		_, err = c.Database("config").Collection("system.sessions").DeleteMany(ctx, bson.D{})
		if err == nil || !strings.Contains(err.Error(), "(BackgroundOperationInProgressForNamespace)") {
			break
		}
		r.log.Debug("drop config.system.sessions: BackgroundOperationInProgressForNamespace, retrying")
		time.Sleep(time.Second * time.Duration(i+1))
	}
	if err != nil {
		return errors.Wrap(err, "drop config.system.sessions")
	}

	_, err = c.Database("local").Collection("system.replset").DeleteMany(ctx, bson.D{})
	if err != nil {
		return errors.Wrap(err, "delete from system.replset")
	}

	_, err = c.Database("local").Collection("system.replset").InsertOne(ctx,
		topo.RSConfig{
			ID:       r.rsConf.ID,
			CSRS:     r.nodeInfo.IsConfigSrv(),
			Version:  1,
			Members:  r.rsConf.Members,
			Settings: r.rsConf.Settings,
		},
	)
	if err != nil {
		return errors.Wrapf(err, "update rs.member host to %s", r.nodeInfo.Me)
	}

	// PITR should be turned off after the physical restore. Otherwise, slicing resumes
	// right after the cluster start while the system in the state of the backup's
	// recovery time. No resync yet. Hence the system knows nothing about the recent
	// restore and chunks made after the backup. So it would successfully start slicing
	// and overwrites chunks after the backup.
	if r.nodeInfo.IsLeader() {
		_, err = c.Database(defs.DB).Collection(defs.ConfigCollection).UpdateOne(ctx, bson.D{},
			bson.D{{"$set", bson.M{"pitr.enabled": false}}},
		)
		if err != nil {
			return errors.Wrap(err, "turn off pitr")
		}

		r.cleanUpPBMCollections(ctx, c)
	}

	return r.shutdown(c)
}

func (r *PhysRestore) cleanUpPBMCollections(ctx context.Context, c *mongo.Client) {
	pbmCollections := []string{
		defs.LockCollection,
		defs.LogCollection,
		// defs.ConfigCollection,
		defs.LockCollection,
		defs.LockOpCollection,
		defs.BcpCollection,
		defs.RestoresCollection,
		defs.CmdStreamCollection,
		defs.PITRChunksCollection,
		defs.PITRCollection,
		defs.PBMOpLogCollection,
		defs.AgentsStatusCollection,
	}

	wg := &sync.WaitGroup{}
	wg.Add(len(pbmCollections))

	for _, coll := range pbmCollections {
		go func() {
			defer wg.Done()

			r.log.Debug("dropping 'admin.%s'", coll)
			_, err := c.Database(defs.DB).Collection(coll).DeleteMany(ctx, bson.D{})
			if err != nil {
				r.log.Warning("failed to delete all from 'admin.%s': %v", coll, err)
			}
		}()
	}

	wg.Wait()
}

func (r *PhysRestore) getShardMapping(bcp *backup.BackupMeta) map[string]string {
	source := make(map[string]string)
	if bcp != nil && bcp.ShardRemap != nil {
		for i := range bcp.Replsets {
			rs := bcp.Replsets[i].Name
			if s, ok := bcp.ShardRemap[rs]; ok {
				source[rs] = s
			}
		}
	}

	mapRevRS := util.MakeReverseRSMapFunc(r.rsMap)
	rv := make(map[string]string)
	for targetS, uri := range r.shards {
		targetRS, _, _ := strings.Cut(uri, "/")
		sourceRS := mapRevRS(targetRS)
		sourceS, ok := source[sourceRS]
		if !ok {
			sourceS = sourceRS
		}
		if sourceS != targetS {
			rv[sourceS] = targetS
		}
	}

	return rv
}

// in case restore-to-time isn't specified (external restore w/o backup
// and --ts provided) the cluster will agree on the latest common ts. Each
// node will pick the ts of the last event in the oplog. Each replset will
// pick the oldest ts and put it as the reples ts. And the cluster will
// pick the oldest ts proposed by replsets. All comms done via storage. Similar
// to the restore states with proposed ts in *.lastTS files.
func (r *PhysRestore) agreeCommonRestoreTS() (primitive.Timestamp, error) {
	var ts primitive.Timestamp
	cts, err := r.getLasOpTime()
	if err != nil {
		return ts, errors.Wrap(err, "define last op time")
	}

	// saving straight for RS as backup for nodes in the RS the same,
	// hence TS would be the same as well
	err = util.RetryableWrite(r.stg,
		r.syncPathRS+"."+string(defs.StatusExtTS),
		[]byte(fmt.Sprintf("%d:%d,%d", time.Now().Unix(), cts.T, cts.I)))
	if err != nil {
		return ts, errors.Wrap(err, "write RS timestamp")
	}

	if r.nodeInfo.IsClusterLeader() {
		_, err := r.waitFiles(defs.StatusExtTS, copyMap(r.syncPathShards), true)
		if err != nil {
			return ts, errors.Wrap(err, "wait for shards timestamp")
		}
		var mints primitive.Timestamp
		for sh := range r.syncPathShards {
			ts, err := r.getTSFromSyncFile(sh)
			if err != nil {
				return ts, errors.Wrapf(err, "get timestamp for RS %s", sh)
			}

			if mints.IsZero() || ts.Compare(mints) == -1 {
				mints = ts
			}
		}

		err = util.RetryableWrite(r.stg,
			r.syncPathCluster+"."+string(defs.StatusExtTS),
			[]byte(fmt.Sprintf("%d:%d,%d", time.Now().Unix(), mints.T, mints.I)))
		if err != nil {
			return ts, errors.Wrap(err, "write")
		}
	}

	_, err = r.waitFiles(defs.StatusExtTS, map[string]struct{}{r.syncPathCluster: {}}, false)
	if err != nil {
		return ts, errors.Wrap(err, "wait for cluster timestamp")
	}

	ts, err = r.getTSFromSyncFile(r.syncPathCluster)
	if err != nil {
		return ts, errors.Wrapf(err, "get cluster timestamp")
	}

	return ts, nil
}

func (r *PhysRestore) setcommittedTxn(_ context.Context, txn []phys.RestoreTxn) error {
	if txn == nil {
		txn = []phys.RestoreTxn{}
	}
	b, err := json.Marshal(txn)
	if err != nil {
		return errors.Wrap(err, "encode")
	}
	return util.RetryableWrite(r.stg, r.syncPathRS+".txn", b)
}

func (r *PhysRestore) getcommittedTxn(context.Context) (map[string]primitive.Timestamp, error) {
	shards := copyMap(r.syncPathShards)
	txn := make(map[string]primitive.Timestamp)
	for len(shards) > 0 {
		for f := range shards {
			dr, err := r.stg.FileStat(f + "." + string(defs.StatusDone))
			if err != nil && !errors.Is(err, storage.ErrNotExist) {
				return nil, errors.Wrapf(err, "check done for <%s>", f)
			}
			if err == nil && dr.Size != 0 {
				delete(shards, f)
				continue
			}

			txnr, err := r.stg.SourceReader(f + ".txn")
			if err != nil && errors.Is(err, storage.ErrNotExist) {
				continue
			}
			if err != nil {
				return nil, errors.Wrapf(err, "get txns <%s>", f)
			}
			txns := []phys.RestoreTxn{}
			err = json.NewDecoder(txnr).Decode(&txns)
			if err != nil {
				return nil, errors.Wrapf(err, "deconde txns <%s>", f)
			}
			for _, t := range txns {
				if t.State == phys.TxnCommit {
					txn[t.ID] = t.Ctime
				}
			}
			delete(shards, f)
		}
		time.Sleep(time.Second * 5)
	}

	return txn, nil
}

// Tries to connect to mongo n times, timeout is applied for each try.
// If a try is unsuccessful, it will check the mongo logs and retry if
// there are no errors or fatals.
func tryConn(port int, logpath string) (*mongo.Client, error) {
	type mlog struct {
		T struct {
			Date string `json:"$date"`
		} `json:"t"`
		S   string `json:"s"`
		Msg string `json:"msg"`
	}

	var cn *mongo.Client
	var err error
	host := fmt.Sprintf("mongodb://localhost:%d", port)
	for i := 0; i < tryConnCount; i++ {
		cn, err = connect.MongoConnect(context.Background(), host,
			connect.AppName("pbm-physical-restore"),
			connect.Direct(true),
			connect.WriteConcern(writeconcern.W1()),
			connect.ConnectTimeout(time.Second*120),
			connect.ServerSelectionTimeout(tryConnTimeout),
		)
		if err == nil {
			return cn, nil
		}

		f, ferr := os.Open(logpath)
		if ferr != nil {
			return nil, errors.Errorf("open logs: %v, connect err: %v", ferr, err)
		}
		defer f.Close()

		dec := json.NewDecoder(f)
		for {
			var m mlog
			if derr := dec.Decode(&m); errors.Is(derr, io.EOF) {
				break
			} else if derr != nil {
				return nil, errors.Errorf("decode logs: %v, connect err: %v", derr, err)
			}
			if m.S == "E" || m.S == "F" {
				return nil, errors.Errorf("mongo failed with [%s] %s / %s, connect err: %v", m.S, m.Msg, m.T.Date, err)
			}
		}
	}

	return nil, errors.Errorf("failed to  connect after %d tries: %v", tryConnCount, err)
}

const internalMongodLog = "pbm.restore.log"

func (r *PhysRestore) startMongo(opts ...string) error {
	if r.tmpConf != nil {
		opts = append(opts, []string{"-f", r.tmpConf.Name()}...)
	}

	opts = append(opts, []string{"--logpath", path.Join(r.dbpath, internalMongodLog)}...)

	errBuf := &bytes.Buffer{}
	cmd := exec.Command(r.mongod, opts...)

	cmd.Stderr = errBuf
	err := cmd.Start()
	if err != nil {
		return err
	}

	// release process resources
	go func() {
		err := cmd.Wait()
		if err != nil {
			slog.Printf("mongod process: %v, %s", err, errBuf)
			mlog := path.Join(r.dbpath, internalMongodLog)
			f, err := os.Open(mlog)
			if err != nil {
				slog.Printf("open mongod log %s: %v", mlog, err)
				return
			}
			buf, err := io.ReadAll(f)
			if err != nil {
				slog.Printf("read mongod log %s: %v", mlog, err)
				return
			}
			slog.Printf("mongod log:\n%s", buf)
		}
	}()
	return nil
}

const hbFrameSec = 60 * 2

func (r *PhysRestore) init(ctx context.Context, name string, opid ctrl.OPID, l log.LogEvent) error {
	cfg, err := config.GetConfig(ctx, r.leadConn)
	if err != nil {
		return errors.Wrap(err, "get pbm config")
	}

	r.stg, err = util.StorageFromConfig(&cfg.Storage, r.nodeInfo.Me, l)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	r.confOpts = cfg.Restore

	r.mongod = "mongod" // run from $PATH by default
	if r.confOpts.MongodLocation != "" {
		r.mongod = r.confOpts.MongodLocation
	}
	if m, ok := r.confOpts.MongodLocationMap[r.nodeInfo.Me]; ok {
		r.mongod = m
	}

	r.log = l

	r.name = name
	r.opid = opid.String()

	r.startTS = time.Now().Unix()

	r.syncPathNode = fmt.Sprintf("%s/%s/rs.%s/node.%s", defs.PhysRestoresDir, r.name, r.rsConf.ID, r.nodeInfo.Me)
	r.syncPathNodeStat = fmt.Sprintf("%s/%s/rs.%s/stat.%s", defs.PhysRestoresDir, r.name, r.rsConf.ID, r.nodeInfo.Me)
	r.syncPathRS = fmt.Sprintf("%s/%s/rs.%s/rs", defs.PhysRestoresDir, r.name, r.rsConf.ID)
	r.syncPathCluster = fmt.Sprintf("%s/%s/cluster", defs.PhysRestoresDir, r.name)
	r.syncPathPeers = make(map[string]struct{})
	for _, m := range r.rsConf.Members {
		if !m.ArbiterOnly {
			r.syncPathPeers[fmt.Sprintf("%s/%s/rs.%s/node.%s", defs.PhysRestoresDir, r.name, r.rsConf.ID, m.Host)] = struct{}{}
		}
	}

	dsh, err := topo.ClusterMembers(ctx, r.leadConn.MongoClient())
	if err != nil {
		return errors.Wrap(err, "get  shards")
	}

	r.syncPathShards = make(map[string]struct{})
	r.syncPathDataShards = make(map[string]struct{})
	for _, s := range dsh {
		r.syncPathShards[fmt.Sprintf("%s/%s/rs.%s/rs", defs.PhysRestoresDir, r.name, s.RS)] = struct{}{}
		if s.ID != "config" {
			r.syncPathDataShards[fmt.Sprintf("%s/%s/rs.%s/rs", defs.PhysRestoresDir, r.name, s.RS)] = struct{}{}
		}
	}

	err = r.hb()
	if err != nil {
		l.Error("send init heartbeat: %v", err)
	}

	r.stopHB = make(chan struct{})
	go func() {
		tk := time.NewTicker(time.Second * hbFrameSec)
		defer func() {
			tk.Stop()
			l.Debug("hearbeats stopped")
		}()

		for {
			select {
			case <-tk.C:
				err := r.hb()
				if err != nil {
					l.Warning("send heartbeat: %v", err)
				}
			case <-r.stopHB:
				return
			}
		}
	}()

	return nil
}

const syncHbSuffix = "hb"

func (r *PhysRestore) hb() error {
	now := []byte(strconv.FormatInt(time.Now().Unix(), 10))

	err := util.RetryableWrite(r.stg, r.syncPathNode+"."+syncHbSuffix, now)
	if err != nil {
		return errors.Wrap(err, "write node hb")
	}

	err = util.RetryableWrite(r.stg, r.syncPathRS+"."+syncHbSuffix, now)
	if err != nil {
		return errors.Wrap(err, "write rs hb")
	}

	err = util.RetryableWrite(r.stg, r.syncPathCluster+"."+syncHbSuffix, now)
	if err != nil {
		return errors.Wrap(err, "write rs hb")
	}

	return nil
}

func (r *PhysRestore) checkHB(file string) error {
	ts := time.Now().Unix()

	_, err := r.stg.FileStat(file)
	// compare with restore start if heartbeat files are yet to be created.
	// basically wait another hbFrameSec*2 sec for heartbeat files.
	if errors.Is(err, storage.ErrNotExist) {
		if r.startTS+hbFrameSec*2 < ts {
			return errors.Errorf("stuck, last beat ts: %d", r.startTS)
		}
		return nil
	}
	if err != nil {
		return errors.Wrap(err, "get file stat")
	}

	f, err := r.stg.SourceReader(file)
	if err != nil {
		return errors.Wrap(err, "get hb file")
	}

	b, err := io.ReadAll(f)
	if err != nil {
		return errors.Wrap(err, "read content")
	}

	t, err := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 0)
	if err != nil {
		return errors.Wrap(err, "decode")
	}

	if t+hbFrameSec*2 < ts {
		return errors.Errorf("stuck, last beat ts: %d", t)
	}

	return nil
}

func (r *PhysRestore) setTmpConf(xopts *topo.MongodOpts) error {
	opts := &topo.MongodOpts{}
	opts.Storage = *topo.NewMongodOptsStorage()
	if xopts != nil {
		opts.Storage = xopts.Storage
	} else if r.bcp != nil {
		setName := util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)
		for _, v := range r.bcp.Replsets {
			if v.Name == setName {
				if v.MongodOpts != nil {
					opts.Storage = v.MongodOpts.Storage
				}
				break
			}
		}
	}

	opts.Net.BindIP = "localhost"
	opts.Net.Port = r.tmpPort
	opts.Storage.DBpath = r.dbpath
	opts.Security = r.secOpts

	var err error
	r.tmpConf, err = os.CreateTemp("", "pbmMongdTmpConf")
	if err != nil {
		return errors.Wrap(err, "create tmp config")
	}
	defer r.tmpConf.Close()

	enc := yaml.NewEncoder(r.tmpConf)
	err = enc.Encode(opts)
	if err != nil {
		return errors.Wrap(err, "encode options")
	}
	err = enc.Close()
	if err != nil {
		return errors.Wrap(err, "close encoder")
	}
	err = r.tmpConf.Sync()
	if err != nil {
		return errors.Wrap(err, "fsync")
	}

	return nil
}

const bcpDir = "__dir__"

// Sets replset files that have to be copied to the target during the restore.
// For non-incremental backups it's just the content of backups files (data) and
// journals. For the incrementals, it will gather files from preceding backups
// traveling back in time from the target backup up to the closest base.
// `Off == -1 && Len == -1` means the file remains unchanged since the last
// backup and there is no data in this backup. We need such info in
// the target backup to know which files from preceding backups should be
// restored. Only files listed in the target backup will be restored.
//
// The restore should be done in reverse order. Applying files (diffs)
// starting from the base and moving forward in time up to the target backup.
func (r *PhysRestore) setBcpFiles(ctx context.Context) error {
	bcp := r.bcp

	setName := util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)
	rs := getRS(bcp, setName)
	if rs == nil {
		return errors.Errorf("no data in the backup for the replica set %s", setName)
	}

	if version.HasFilelistFile(bcp.PBMVersion) {
		filelistPath := path.Join(bcp.Name, setName, backup.FilelistName)
		rdr, err := r.bcpStg.SourceReader(filelistPath)
		if err != nil {
			return errors.Wrapf(err, "open filelist %q", filelistPath)
		}
		defer rdr.Close()

		filelist, err := backup.ReadFilelist(rdr)
		rdr.Close()
		if err != nil {
			return errors.Wrap(err, "parse filelist")
		}

		rs.Files = filelist
	}

	targetFiles := make(map[string]bool)
	for _, f := range append(rs.Files, rs.Journal...) {
		targetFiles[f.Name] = false
	}

	for {
		data := files{
			BcpName: bcp.Name,
			Cmpr:    bcp.Compression,
			Data:    []backup.File{},
		}
		// PBM-1058
		var is1058 bool
		for _, f := range append(rs.Files, rs.Journal...) {
			if _, ok := targetFiles[f.Name]; ok && f.Off >= 0 && f.Len >= 0 {
				data.Data = append(data.Data, f)
				targetFiles[f.Name] = true

				if data.dbpath == "" {
					is1058, data.dbpath = findDBpath(f.Name)
				}
			}
		}
		if is1058 {
			r.log.Debug("issue PBM-1058 detected in backup %s, dbpath is %s", bcp.Name, data.dbpath)
		}

		r.files = append(r.files, data)

		if bcp.SrcBackup == "" {
			break
		}

		r.log.Debug("get src %s", bcp.SrcBackup)
		var err error
		bcp, err = backup.NewDBManager(r.leadConn).GetBackupByName(ctx, bcp.SrcBackup)
		if err != nil {
			return errors.Wrapf(err, "get source backup")
		}
		rs = getRS(bcp, setName)

		if version.HasFilelistFile(bcp.PBMVersion) {
			filelistPath := path.Join(bcp.Name, setName, backup.FilelistName)
			rdr, err := r.bcpStg.SourceReader(filelistPath)
			if err != nil {
				return errors.Wrapf(err, "open filelist %q", filelistPath)
			}
			defer rdr.Close()

			filelist, err := backup.ReadFilelist(rdr)
			rdr.Close()
			if err != nil {
				return errors.Wrap(err, "parse filelist")
			}

			rs.Files = filelist
		}
	}

	// Directories only. Incremental $backupCusor returns collections that
	// were created but haven't ended up in the checkpoint yet in the format
	// if they don't belong to this backup (see PBM-1063). PBM doesn't copy
	// such files. But they are already in WT metadata. PSMDB (WT) handles
	// this by creating such files during the start. But fails to do so if
	// it runs with `directoryPerDB` option. Namely fails to create a directory
	// for the collections. So we have to detect and create these directories
	// during the restore.
	var dirs []backup.File
	dirsm := make(map[string]struct{})
	for f, was := range targetFiles {
		if !was {
			dir := path.Dir(f)
			if _, ok := dirsm[dir]; dir != "." && !ok {
				dirs = append(dirs, backup.File{
					Name: f,
					Off:  -1,
					Len:  -1,
					Size: -1,
				})
				dirsm[dir] = struct{}{}
			}
		}
	}

	if len(dirs) > 0 {
		r.files = append(r.files, files{
			BcpName: bcpDir,
			Data:    dirs,
		})
	}

	return nil
}

// Checks if dbpath exists in the file name (affected by PBM-1058) and
// returns it.
// We suppose that "journal" will always be present in the backup and it is
// always in the dbpath root and doesn't contain subdirs, only files. Having
// a leading `/` indicates that restore was affected by PBM-1058 but only
// with journal files we may detect the exact prefix.
func findDBpath(fname string) (bool, string) {
	if !strings.HasPrefix(fname, "/") {
		return false, ""
	}

	is := true
	d, _ := path.Split(fname)
	prefix := ""
	if strings.HasSuffix(d, "/journal/") {
		prefix = path.Dir(d[0 : len(d)-1])
	}
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	return is, prefix
}

func getRS(bcp *backup.BackupMeta, rs string) *backup.BackupReplset {
	for _, r := range bcp.Replsets {
		if r.Name == rs {
			return &r
		}
	}
	return nil
}

func (r *PhysRestore) prepareBackup(ctx context.Context, backupName string) error {
	var err error
	r.bcp, err = backup.NewDBManager(r.leadConn).GetBackupByName(ctx, backupName)
	if errors.Is(err, errors.ErrNotFound) {
		r.bcp, err = GetMetaFromStore(r.stg, backupName)
	}
	if err != nil {
		return errors.Wrap(err, "get backup metadata")
	}

	r.bcpStg, err = util.StorageFromConfig(&r.bcp.Store.StorageConf, r.nodeInfo.Me, log.LogEventFromContext(ctx))
	if err != nil {
		return errors.Wrap(err, "get backup storage")
	}

	if r.bcp == nil {
		return errors.New("snapshot name doesn't set")
	}

	err = setRestoreBackup(ctx, r.leadConn, r.name, r.bcp.Name, nil)
	if err != nil {
		return errors.Wrap(err, "set backup name")
	}

	if r.bcp.Status != defs.StatusDone {
		return errors.Errorf("backup wasn't successful: status: %s, error: %s", r.bcp.Status, r.bcp.Error())
	}

	if !version.CompatibleWith(r.bcp.PBMVersion, version.BreakingChangesMap[r.bcp.Type]) {
		return errors.Errorf("backup version (v%s) is not compatible with PBM v%s",
			r.bcp.PBMVersion, version.Current().Version)
	}

	mgoV, err := version.GetMongoVersion(ctx, r.node)
	if err != nil || len(mgoV.Version) < 1 {
		return errors.Wrap(err, "define mongo version")
	}

	if semver.Compare(majmin(r.bcp.MongoVersion), majmin(mgoV.VersionString)) != 0 {
		return errors.Errorf("backup's Mongo version (%s) is not compatible with Mongo %s",
			r.bcp.MongoVersion, mgoV.VersionString)
	}

	mv, err := r.checkMongod(r.bcp.MongoVersion)
	if err != nil {
		return errors.Wrap(err, "check mongod binary")
	}
	r.log.Debug("mongod binary: %s, version: %s", r.mongod, mv)

	err = r.setBcpFiles(ctx)
	if err != nil {
		return errors.Wrap(err, "get data for restore")
	}

	s, err := topo.ClusterMembers(ctx, r.leadConn.MongoClient())
	if err != nil {
		return errors.Wrap(err, "get cluster members")
	}

	mapRevRS := util.MakeReverseRSMapFunc(r.rsMap)
	fl := make(map[string]topo.Shard, len(s))
	for _, rs := range s {
		fl[mapRevRS(rs.RS)] = rs
	}

	var nors []string
	for _, sh := range r.bcp.Replsets {
		if _, ok := fl[sh.Name]; !ok {
			nors = append(nors, sh.Name)
		}
	}

	if len(nors) > 0 {
		return errors.Errorf("extra/unknown replica set found in the backup: %s", strings.Join(nors, ","))
	}

	setName := mapRevRS(r.nodeInfo.SetName)
	var ok bool
	for _, v := range r.bcp.Replsets {
		if v.Name == setName {
			ok = true
			break
		}
	}
	if !ok {
		if r.nodeInfo.IsLeader() {
			return ErrNoDataForConfigsvr
		}
		return ErrNoDataForShard
	}

	return nil
}

// ensure mongod for internal restarts is available and matches
// the backup's version
//
//nolint:nonamedreturns
func (r *PhysRestore) checkMongod(needVersion string) (version string, err error) {
	cmd := exec.Command(r.mongod, "--version")

	stderr := &bytes.Buffer{}
	stdout := &bytes.Buffer{}

	cmd.Stderr = stderr
	cmd.Stdout = stdout

	err = cmd.Run()
	if err != nil {
		return "", errors.Errorf("run: %v. stderr: %s", err, stderr)
	}

	_, v, ok := strings.Cut(strings.Split(stdout.String(), "\n")[0], "db version ")
	if !ok {
		return "", errors.Errorf("parse version from output %s", stdout.String())
	}

	if semver.Compare(majmin(needVersion), majmin(v)) != 0 {
		return "", errors.Errorf("backup's Mongo version (%s) is not compatible with mongod %s", needVersion, v)
	}

	return v, nil
}

// MarkFailed sets the restore and rs state as failed with the given message
func (r *PhysRestore) MarkFailed(meta *RestoreMeta, e error, markCluster bool) {
	var nerr nodeError
	if errors.As(e, &nerr) {
		e = nerr
		meta.Replsets = []RestoreReplset{{
			Name:   nerr.node,
			Status: defs.StatusError,
			Error:  nerr.msg,
		}}
	} else if len(meta.Replsets) > 0 {
		meta.Replsets[0].Status = defs.StatusError
		meta.Replsets[0].Error = e.Error()
	}

	err := util.RetryableWrite(r.stg,
		r.syncPathNode+"."+string(defs.StatusError), errStatus(e))
	if err != nil {
		r.log.Error("write error state `%v` to storage: %v", e, err)
	}

	// At some point, every node will try to set an rs and cluster state
	// (in `toState` method).
	// Here we are not aware of partlyDone etc so leave it to the `toState`.
	if r.nodeInfo.IsPrimary && markCluster {
		serr := util.RetryableWrite(r.stg,
			r.syncPathRS+"."+string(defs.StatusError), errStatus(e))
		if serr != nil {
			r.log.Error("MarkFailed: write replset error state `%v`: %v", e, serr)
		}
	}
	if r.nodeInfo.IsClusterLeader() && markCluster {
		serr := util.RetryableWrite(r.stg,
			r.syncPathCluster+"."+string(defs.StatusError), errStatus(e))
		if serr != nil {
			r.log.Error("MarkFailed: write cluster error state `%v`: %v", e, serr)
		}
	}
}

func removeAll(dir string, l log.LogEvent) error {
	d, err := os.Open(dir)
	if err != nil {
		return errors.Wrap(err, "open dir")
	}
	defer d.Close()

	names, err := d.Readdirnames(-1)
	if err != nil {
		return errors.Wrap(err, "read file names")
	}
	for _, n := range names {
		if n == internalMongodLog {
			continue
		}
		err = os.RemoveAll(filepath.Join(dir, n))
		if err != nil {
			return errors.Wrapf(err, "remove '%s'", n)
		}
		l.Debug("remove %s", filepath.Join(dir, n))
	}
	return nil
}

func majmin(v string) string {
	if len(v) == 0 {
		return v
	}

	if v[0] != 'v' {
		v = "v" + v
	}

	return semver.MajorMinor(v)
}
