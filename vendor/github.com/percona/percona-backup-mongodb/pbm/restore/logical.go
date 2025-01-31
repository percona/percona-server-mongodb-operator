package restore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/mongodb/mongo-tools/common/bsonutil"
	"github.com/mongodb/mongo-tools/common/db"
	"github.com/mongodb/mongo-tools/common/idx"
	"github.com/mongodb/mongo-tools/mongorestore"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/archive"
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
	"github.com/percona/percona-backup-mongodb/pbm/restore/phys"
	"github.com/percona/percona-backup-mongodb/pbm/snapshot"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type Restore struct {
	name     string
	leadConn connect.Client
	nodeConn *mongo.Client
	brief    topo.NodeBrief
	stopHB   chan struct{}
	nodeInfo *topo.NodeInfo
	cfg      *config.Config
	bcpStg   storage.Storage
	oplogStg storage.Storage

	numParallelColls          int
	numInsertionWorkersPerCol int
	// Shards to participate in restore. Num of shards in bcp could
	// be less than in the cluster and this is ok. Only these shards
	// would be expected to run restore (distributed transactions sync,
	//  status checks etc.)
	//
	// Only the restore leader would have this info.
	shards []topo.Shard
	// rsMap is mapping between old and new replset names. used for data restore.
	// empty if all replset names are the same
	rsMap map[string]string
	// sMap is mapping between old and new shard names. used for router config update.
	// empty if all shard names are the same
	sMap map[string]string

	log  log.LogEvent
	opid string

	indexCatalog *idx.IndexCatalog
}

type oplogRange struct {
	chunks []oplog.OplogChunk

	storage storage.Storage
}

// PBM restore from temp collections (pbmRUsers/pbmRRoles)should be used
type restoreUsersAndRolesOption bool

// New creates a new restore object
func New(
	leadConn connect.Client,
	nodeConn *mongo.Client,
	brief topo.NodeBrief,
	cfg *config.Config,
	rsMap map[string]string,
	numParallelColls,
	numInsertionWorkersPerCol int,
) *Restore {
	if rsMap == nil {
		rsMap = make(map[string]string)
	}

	return &Restore{
		leadConn: leadConn,
		nodeConn: nodeConn,
		brief:    brief,
		rsMap:    rsMap,

		cfg: cfg,

		numParallelColls:          numParallelColls,
		numInsertionWorkersPerCol: numInsertionWorkersPerCol,
		indexCatalog:              idx.NewIndexCatalog(),
	}
}

// Close releases object resources.
// Should be run to avoid leaks.
func (r *Restore) Close() {
	if r.stopHB != nil {
		close(r.stopHB)
	}
}

func (r *Restore) exit(ctx context.Context, err error) {
	if err != nil && !errors.Is(err, ErrNoDataForShard) {
		ferr := r.MarkFailed(ctx, err)
		if ferr != nil {
			log.LogEventFromContext(ctx).
				Error("mark restore as failed `%v`: %v", err, ferr)
		}
	}

	r.Close()
}

// resolveNamespace resolves final namespace(s) based on the backup namespace,
// restore namespace, cloning options and option whether we should restore users&roles
func resolveNamespace(nssBackup, nssRestore []string, cloneNS snapshot.CloneNS, usingUsersAndRoles bool) []string {
	if cloneNS.IsSpecified() {
		return []string{cloneNS.FromNS}
	}
	if util.IsSelective(nssRestore) {
		if usingUsersAndRoles {
			var nss []string
			nss = append(nss, nssRestore...)
			nss = append(nss,
				defs.DB+"."+defs.TmpUsersCollection,
				defs.DB+"."+defs.TmpRolesCollection,
			)
			return nss
		}

		return nssRestore
	}
	if util.IsSelective(nssBackup) {
		return nssBackup
	}

	return nssBackup
}

// shouldRestoreUsersAndRoles determines whether user&roles should be restored from the backup
func shouldRestoreUsersAndRoles(
	nssBackup, nssRestore []string,
	cloneNS snapshot.CloneNS,
	usingUsersAndRoles bool,
) restoreUsersAndRolesOption {
	if cloneNS.IsSpecified() {
		return false
	}
	if util.IsSelective(nssBackup) {
		return false
	}
	if util.IsSelective(nssRestore) {
		return restoreUsersAndRolesOption(usingUsersAndRoles)
	}

	return true
}

// Snapshot do the snapshot's (mongo dump) restore
//
//nolint:nonamedreturns
func (r *Restore) Snapshot(
	ctx context.Context,
	cmd *ctrl.RestoreCmd,
	opid ctrl.OPID,
	bcp *backup.BackupMeta,
) (err error) {
	l := log.LogEventFromContext(ctx)

	defer func() { r.exit(log.Copy(context.Background(), ctx), err) }()

	err = r.init(ctx, cmd.Name, opid, l)
	if err != nil {
		return err
	}

	r.bcpStg, err = util.StorageFromConfig(&bcp.Store.StorageConf, r.brief.Me, r.log)
	if err != nil {
		return errors.Wrap(err, "get backup storage")
	}

	cloneNS := snapshot.CloneNS{FromNS: cmd.NamespaceFrom, ToNS: cmd.NamespaceTo}
	if r.brief.Sharded && cloneNS.IsSpecified() {
		return errors.New("Namespace cloning is not supported in sharded cluster")
	}

	nss := resolveNamespace(
		bcp.Namespaces,
		cmd.Namespaces,
		cloneNS,
		cmd.UsersAndRoles)
	usersAndRolesOpt := shouldRestoreUsersAndRoles(
		bcp.Namespaces,
		cmd.Namespaces,
		cloneNS,
		cmd.UsersAndRoles)

	err = setRestoreBackup(ctx, r.leadConn, r.name, cmd.BackupName, nss)
	if err != nil {
		return errors.Wrap(err, "set backup name")
	}

	err = r.checkSnapshot(ctx, bcp, nss)
	if err != nil {
		return err
	}

	err = r.setShards(ctx, bcp)
	if err != nil {
		return err
	}

	if r.nodeInfo.IsConfigSrv() {
		r.sMap = r.getShardMapping(bcp)
	}

	dump, chunks, err := r.snapshotObjects(bcp)
	if err != nil {
		return err
	}

	err = r.checkForCompatibility(ctx, util.MakeReverseRSMapFunc(r.rsMap)(r.brief.SetName), bcp)
	if err != nil {
		return err
	}

	err = r.toState(ctx, defs.StatusRunning, &defs.WaitActionStart)
	if err != nil {
		return err
	}

	err = r.RunSnapshot(ctx, dump, bcp, nss, cloneNS, usersAndRolesOpt)
	if err != nil {
		return err
	}

	err = r.toState(ctx, defs.StatusDumpDone, nil)
	if err != nil {
		return err
	}

	oplogRanges := []oplogRange{
		{chunks: chunks, storage: r.bcpStg},
	}
	oplogOption := &applyOplogOption{
		end:     &bcp.LastWriteTS,
		nss:     nss,
		cloudNS: cloneNS,
	}
	if r.nodeInfo.IsConfigSrv() && util.IsSelective(nss) {
		oplogOption.nss = []string{"config.databases"}
		oplogOption.filter = newConfigsvrOpFilter(nss)
	}

	err = r.applyOplog(ctx, oplogRanges, oplogOption)
	if err != nil {
		return err
	}

	if cloneNS.IsSpecified() {
		err = r.restoreIndexes(ctx, []string{cloneNS.ToNS})
	} else {
		err = r.restoreIndexes(ctx, oplogOption.nss)
	}
	if err != nil {
		return errors.Wrap(err, "restore indexes")
	}

	if err = r.updateRouterConfig(ctx); err != nil {
		return errors.Wrap(err, "update router config")
	}

	return r.Done(ctx)
}

// newConfigsvrOpFilter filters out not needed ops during selective backup on configsvr
func newConfigsvrOpFilter(nss []string) oplog.OpFilter {
	selected := util.MakeSelectedPred(nss)

	return func(r *oplog.Record) bool {
		if r.Namespace != "config.databases" {
			return false
		}

		// create/drop database and movePrimary ops contain o2._id with the database name
		for _, e := range r.Query {
			if e.Key != "_id" {
				continue
			}

			db, _ := e.Value.(string)
			return selected(db)
		}

		return false
	}
}

// PITR do the Point-in-Time Recovery
//
//nolint:nonamedreturns
func (r *Restore) PITR(
	ctx context.Context,
	cmd *ctrl.RestoreCmd,
	opid ctrl.OPID,
	bcp *backup.BackupMeta,
) (err error) {
	l := log.LogEventFromContext(ctx)

	defer func() { r.exit(log.Copy(context.Background(), ctx), err) }()

	err = r.init(ctx, cmd.Name, opid, l)
	if err != nil {
		return err
	}

	if bcp.LastWriteTS.Compare(cmd.OplogTS) >= 0 {
		return errors.New("snapshot's last write is later than the target time. " +
			"Try to set an earlier snapshot. Or leave the snapshot empty so PBM will choose one.")
	}

	r.bcpStg, err = util.StorageFromConfig(&bcp.Store.StorageConf, r.brief.Me, r.log)
	if err != nil {
		return errors.Wrap(err, "get backup storage")
	}
	r.oplogStg, err = util.GetStorage(ctx, r.leadConn, r.nodeInfo.Me, log.LogEventFromContext(ctx))
	if err != nil {
		return errors.Wrap(err, "get oplog storage")
	}

	cloneNS := snapshot.CloneNS{FromNS: cmd.NamespaceFrom, ToNS: cmd.NamespaceTo}
	if r.brief.Sharded && cloneNS.IsSpecified() {
		return errors.New("Namespace cloning is not supported in sharded cluster")
	}

	nss := resolveNamespace(
		bcp.Namespaces,
		cmd.Namespaces,
		cloneNS,
		cmd.UsersAndRoles)
	usersAndRolesOpt := shouldRestoreUsersAndRoles(
		bcp.Namespaces,
		cmd.Namespaces,
		cloneNS,
		cmd.UsersAndRoles)

	if r.nodeInfo.IsLeader() {
		err = SetOplogTimestamps(ctx, r.leadConn, r.name, 0, int64(cmd.OplogTS.T))
		if err != nil {
			return errors.Wrap(err, "set PITR timestamp")
		}
	}

	err = setRestoreBackup(ctx, r.leadConn, r.name, bcp.Name, nss)
	if err != nil {
		return errors.Wrap(err, "set backup name")
	}

	err = r.checkSnapshot(ctx, bcp, nss)
	if err != nil {
		return err
	}

	err = r.setShards(ctx, bcp)
	if err != nil {
		return err
	}

	bcpShards := make([]string, len(bcp.Replsets))
	for i := range bcp.Replsets {
		bcpShards[i] = bcp.Replsets[i].Name
	}

	if !slices.Contains(bcpShards, util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)) {
		return r.Done(ctx) // skip. no backup for current rs
	}

	if r.nodeInfo.IsConfigSrv() {
		r.sMap = r.getShardMapping(bcp)
	}

	chunks, err := r.chunks(ctx, bcp.LastWriteTS, cmd.OplogTS)
	if err != nil {
		return err
	}

	dump, bcpChunks, err := r.snapshotObjects(bcp)
	if err != nil {
		return err
	}

	err = r.checkForCompatibility(ctx, util.MakeReverseRSMapFunc(r.rsMap)(r.brief.SetName), bcp)
	if err != nil {
		return err
	}

	err = r.toState(ctx, defs.StatusRunning, &defs.WaitActionStart)
	if err != nil {
		return err
	}

	err = r.RunSnapshot(ctx, dump, bcp, nss, cloneNS, usersAndRolesOpt)
	if err != nil {
		return err
	}

	err = r.toState(ctx, defs.StatusDumpDone, nil)
	if err != nil {
		return err
	}

	oplogRanges := []oplogRange{
		{chunks: bcpChunks, storage: r.bcpStg},
		{chunks: chunks, storage: r.oplogStg},
	}
	oplogOption := applyOplogOption{
		end:     &cmd.OplogTS,
		nss:     nss,
		cloudNS: cloneNS,
	}
	if r.nodeInfo.IsConfigSrv() && util.IsSelective(nss) {
		oplogOption.nss = []string{"config.databases"}
		oplogOption.filter = newConfigsvrOpFilter(nss)
	}
	err = r.applyOplog(ctx, oplogRanges, &oplogOption)
	if err != nil {
		return err
	}

	if cloneNS.IsSpecified() {
		err = r.restoreIndexes(ctx, []string{cloneNS.ToNS})
	} else {
		err = r.restoreIndexes(ctx, oplogOption.nss)
	}
	if err != nil {
		return errors.Wrap(err, "restore indexes")
	}

	if err = r.updateRouterConfig(ctx); err != nil {
		return errors.Wrap(err, "update router config")
	}

	return r.Done(ctx)
}

//nolint:nonamedreturns
func (r *Restore) ReplayOplog(ctx context.Context, cmd *ctrl.ReplayCmd, opid ctrl.OPID, l log.LogEvent) (err error) {
	defer func() { r.exit(log.Copy(context.Background(), ctx), err) }()

	if err = r.init(ctx, cmd.Name, opid, l); err != nil {
		return errors.Wrap(err, "init")
	}

	if !r.nodeInfo.IsPrimary {
		return errors.Errorf("%q is not primary", r.nodeInfo.SetName)
	}

	r.shards, err = topo.ClusterMembers(ctx, r.leadConn.MongoClient())
	if err != nil {
		return errors.Wrap(err, "get cluster members")
	}

	if r.nodeInfo.IsLeader() {
		err := SetOplogTimestamps(ctx, r.leadConn, r.name, int64(cmd.Start.T), int64(cmd.End.T))
		if err != nil {
			return errors.Wrap(err, "set oplog timestamps")
		}
	}

	oplogShards, err := oplog.AllOplogRSNames(ctx, r.leadConn, cmd.Start, cmd.End)
	if err != nil {
		return err
	}

	err = r.checkTopologyForOplog(r.shards, oplogShards)
	if err != nil {
		return errors.Wrap(err, "topology")
	}

	if !slices.Contains(oplogShards, util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)) {
		return r.Done(ctx) // skip. no oplog for current rs
	}

	r.oplogStg, err = util.GetStorage(ctx, r.leadConn, r.nodeInfo.Me, log.LogEventFromContext(ctx))
	if err != nil {
		return errors.Wrapf(err, "get oplog storage")
	}

	opChunks, err := r.chunks(ctx, cmd.Start, cmd.End)
	if err != nil {
		return err
	}

	err = r.toState(ctx, defs.StatusRunning, &defs.WaitActionStart)
	if err != nil {
		return err
	}

	oplogRanges := []oplogRange{
		{chunks: opChunks, storage: r.oplogStg},
	}
	oplogOption := applyOplogOption{
		start:  &cmd.Start,
		end:    &cmd.End,
		unsafe: true,
	}
	err = r.applyOplog(ctx, oplogRanges, &oplogOption)
	if err != nil {
		return err
	}

	return r.Done(ctx)
}

func (r *Restore) init(ctx context.Context, name string, opid ctrl.OPID, l log.LogEvent) error {
	r.log = l

	var err error
	r.nodeInfo, err = topo.GetNodeInfoExt(ctx, r.nodeConn)
	if err != nil {
		return errors.Wrap(err, "get node data")
	}
	if r.nodeInfo.SetName == "" {
		return errors.Wrap(err, "unable to define replica set")
	}

	r.name = name
	r.opid = opid.String()
	if r.nodeInfo.IsLeader() {
		ts, err := topo.GetClusterTime(ctx, r.leadConn)
		if err != nil {
			return errors.Wrap(err, "init restore meta, read cluster time")
		}

		meta := &RestoreMeta{
			Type:     defs.LogicalBackup,
			OPID:     r.opid,
			Name:     r.name,
			StartTS:  time.Now().Unix(),
			Status:   defs.StatusStarting,
			Replsets: []RestoreReplset{},
			Hb:       ts,
		}
		err = SetRestoreMeta(ctx, r.leadConn, meta)
		if err != nil {
			return errors.Wrap(err, "write backup meta to db")
		}

		r.stopHB = make(chan struct{})
		go func() {
			tk := time.NewTicker(time.Second * 5)
			defer tk.Stop()

			for {
				select {
				case <-tk.C:
					err := RestoreHB(ctx, r.leadConn, r.name)
					if err != nil {
						l.Error("send heartbeat: %v", err)
					}
				case <-r.stopHB:
					return
				}
			}
		}()
	}

	// Waiting for StatusStarting to move further.
	// In case some preparations has to be done before the restore.
	err = r.waitForStatus(ctx, defs.StatusStarting)
	if err != nil {
		return errors.Wrap(err, "waiting for start")
	}

	rsMeta := RestoreReplset{
		Name:       r.nodeInfo.SetName,
		StartTS:    time.Now().UTC().Unix(),
		Status:     defs.StatusStarting,
		Conditions: Conditions{},
	}

	err = AddRestoreRSMeta(ctx, r.leadConn, r.name, rsMeta)
	if err != nil {
		return errors.Wrap(err, "add shard's metadata")
	}

	return nil
}

func (r *Restore) checkTopologyForOplog(currShards []topo.Shard, oplogShards []string) error {
	mapRS, mapRevRS := util.MakeRSMapFunc(r.rsMap), util.MakeReverseRSMapFunc(r.rsMap)

	shards := make(map[string]struct{}, len(currShards))
	for i := range r.shards {
		shards[r.shards[i].RS] = struct{}{}
	}

	var missed []string
	for _, rs := range oplogShards {
		name := mapRS(rs)
		if _, ok := shards[name]; !ok {
			missed = append(missed, name)
		} else if mapRevRS(name) != rs {
			missed = append(missed, name)
		}
	}

	if len(missed) != 0 {
		return errors.Errorf("missed replset %s", strings.Join(missed, ", "))
	}

	return nil
}

// chunks defines chunks of oplog slice in given range, ensures its integrity (timeline
// is contiguous - there are no gaps), checks for respective files on storage and returns
// chunks list if all checks passed
func (r *Restore) chunks(ctx context.Context, from, to primitive.Timestamp) ([]oplog.OplogChunk, error) {
	return chunks(ctx, r.leadConn, r.oplogStg, from, to, r.nodeInfo.SetName, r.rsMap)
}

// LookupBackupMeta fetches backup metadata.
//
// It tries to find the metadata in database. If there is no such metadata in
// database, it tries to fetch from the main storage.
func LookupBackupMeta(
	ctx context.Context,
	conn connect.Client,
	backupName string,
	node string,
) (*backup.BackupMeta, error) {
	bcp, err := backup.NewDBManager(conn).GetBackupByName(ctx, backupName)
	if err == nil {
		return bcp, nil
	}
	if !errors.Is(err, errors.ErrNotFound) {
		return nil, errors.Wrap(err, "get backup metadata from db")
	}

	var stg storage.Storage
	stg, err = util.GetStorage(ctx, conn, node, log.LogEventFromContext(ctx))
	if err != nil {
		return nil, errors.Wrap(err, "get storage")
	}

	bcp, err = GetMetaFromStore(stg, backupName)
	if err != nil {
		return nil, errors.Wrap(err, "get backup metadata from storage")
	}

	return bcp, nil
}

// setShards defines and set shards participating in the restore
// cluster migth have more shards then the backup and it's ok. But all
// backup's shards must have respective destination on the target cluster.
func (r *Restore) setShards(ctx context.Context, bcp *backup.BackupMeta) error {
	s, err := topo.ClusterMembers(ctx, r.leadConn.MongoClient())
	if err != nil {
		return errors.Wrap(err, "get cluster members")
	}

	fl := make(map[string]topo.Shard, len(s))
	for _, rs := range s {
		fl[rs.RS] = rs
	}

	mapRS, mapRevRS := util.MakeRSMapFunc(r.rsMap), util.MakeReverseRSMapFunc(r.rsMap)

	var nors []string
	for _, sh := range bcp.Replsets {
		name := mapRS(sh.Name)
		rs, ok := fl[name]
		if !ok {
			nors = append(nors, name)
			continue
		} else if mapRevRS(name) != sh.Name {
			nors = append(nors, name)
			continue
		}

		r.shards = append(r.shards, rs)
	}

	if r.nodeInfo.IsLeader() && len(nors) > 0 {
		return errors.Errorf("extra/unknown replica set found in the backup: %s", strings.Join(nors, ", "))
	}

	return nil
}

var (
	ErrNoDataForShard     = errors.New("no data for shard")
	ErrNoDataForConfigsvr = errors.New("no data for the config server or sole rs in backup")
)

func (r *Restore) snapshotObjects(bcp *backup.BackupMeta) (string, []oplog.OplogChunk, error) {
	var ok bool
	var rsMeta *backup.BackupReplset
	revRSName := util.MakeReverseRSMapFunc(r.rsMap)(r.nodeInfo.SetName)
	for i := range bcp.Replsets {
		r := &bcp.Replsets[i]
		if r.Name == revRSName {
			rsMeta = r
			ok = true
			break
		}
	}
	if !ok {
		if r.nodeInfo.IsLeader() {
			return "", nil, ErrNoDataForConfigsvr
		}
		return "", nil, ErrNoDataForShard
	}

	if _, err := r.bcpStg.FileStat(rsMeta.DumpName); err != nil {
		return "", nil, errors.Wrapf(err, "failed to ensure snapshot file %s", rsMeta.DumpName)
	}
	if version.IsLegacyBackupOplog(bcp.PBMVersion) {
		if _, err := r.bcpStg.FileStat(rsMeta.OplogName); err != nil {
			return "", nil, errors.Errorf("failed to ensure oplog file %s: %v", rsMeta.OplogName, err)
		}

		chunks := []oplog.OplogChunk{{
			RS:          r.nodeInfo.SetName,
			FName:       rsMeta.OplogName,
			Compression: bcp.Compression,
			StartTS:     bcp.FirstWriteTS,
			EndTS:       bcp.LastWriteTS,
		}}
		return rsMeta.DumpName, chunks, nil
	}

	files, err := r.bcpStg.List(rsMeta.OplogName, "")
	if err != nil {
		return "", nil, errors.Wrap(err, "failed to list oplog files")
	}

	chunks := make([]oplog.OplogChunk, len(files))
	for i := range files {
		file := &files[i]
		chunk := &chunks[i]

		chunk.RS = rsMeta.Name
		chunk.FName = rsMeta.OplogName + "/" + file.Name
		chunk.Size = file.Size

		chunk.StartTS, chunk.EndTS, chunk.Compression, err = backup.ParseChunkName(file.Name)
		if err != nil {
			return "", nil, errors.Wrap(err, "failed to parse oplog filenames")
		}
	}

	return rsMeta.DumpName, chunks, nil
}

func (r *Restore) checkSnapshot(ctx context.Context, bcp *backup.BackupMeta, nss []string) error {
	if bcp.Status != defs.StatusDone {
		return errors.Errorf("backup wasn't successful: status: %s, error: %s",
			bcp.Status, bcp.Error())
	}

	if !version.CompatibleWith(version.Current().Version, version.BreakingChangesMap[bcp.Type]) {
		return errors.Errorf("backup PBM v%s is incompatible with the running PBM v%s",
			bcp.PBMVersion, version.Current().Version)
	}

	if bcp.FCV != "" {
		fcv, err := version.GetFCV(ctx, r.nodeConn)
		if err != nil {
			return errors.Wrap(err, "get featureCompatibilityVersion")
		}

		if bcp.FCV != fcv {
			r.log.Warning("backup FCV %q is incompatible with the running mongo FCV %q",
				bcp.FCV, fcv)
			return nil
		}
	} else {
		ver, err := version.GetMongoVersion(ctx, r.nodeConn)
		if err != nil {
			return errors.Wrap(err, "get mongo version")
		}

		if majmin(bcp.MongoVersion) != majmin(ver.VersionString) {
			r.log.Warning(
				"backup mongo version %q is incompatible with the running mongo version %q",
				bcp.MongoVersion, ver.VersionString)
			return nil
		}

		if r.brief.Sharded && ver.IsConfigShardSupported() && util.IsSelective(nss) {
			hasConfigShard, err := topo.HasConfigShard(ctx, r.leadConn)
			if err != nil {
				return errors.Wrap(err, "check for Config Shard")
			}
			if hasConfigShard {
				return errors.New("selective restore is not supported with Config Shard")
			}
		}
	}

	return nil
}

func (r *Restore) toState(ctx context.Context, status defs.Status, wait *time.Duration) error {
	r.log.Info("moving to state %s", status)
	return toState(ctx, r.leadConn, status, r.name, r.nodeInfo, r.reconcileStatus, wait)
}

func (r *Restore) RunSnapshot(
	ctx context.Context,
	dump string,
	bcp *backup.BackupMeta,
	nss []string,
	cloneNS snapshot.CloneNS,
	usersAndRolesOpt restoreUsersAndRolesOption,
) error {
	if version.IsLegacyArchive(bcp.PBMVersion) {
		if util.IsSelective(bcp.Namespaces) || util.IsSelective(nss) {
			return errors.New("selective restore is not supported from legacy backup")
		}

		return r.restoreLegacyArchive(ctx, dump, bcp)
	}

	if !util.IsSelective(nss) {
		nss = bcp.Namespaces
	}
	if !util.IsSelective(nss) {
		nss = []string{"*.*"}
	}

	mapRS := util.MakeReverseRSMapFunc(r.rsMap)

	r.log.Debug("restoring up to %d collections in parallel", r.numParallelColls)

	rdr, err := snapshot.DownloadDump(
		func(ns string) (io.ReadCloser, error) {
			stg, err := util.StorageFromConfig(&bcp.Store.StorageConf, r.brief.Me, r.log)
			if err != nil {
				return nil, errors.Wrap(err, "get storage")
			}
			// while importing backup made by RS with another name
			// that current RS we can't use our r.node.RS() to point files
			// we have to use mapping passed by --replset-mapping option
			rdr, err := stg.SourceReader(path.Join(bcp.Name, mapRS(r.brief.SetName), ns))
			if err != nil {
				return nil, err
			}

			if ns == archive.MetaFile {
				data, err := io.ReadAll(rdr)
				if err != nil {
					return nil, err
				}

				err = r.loadIndexesFrom(bytes.NewReader(data), cloneNS)
				if err != nil {
					return nil, errors.Wrap(err, "load indexes")
				}

				rdr = io.NopCloser(bytes.NewReader(data))
			}

			return rdr, nil
		},
		bcp.Compression,
		util.MakeSelectedPred(nss),
		r.numParallelColls)
	if err != nil {
		return err
	}
	defer rdr.Close()

	if r.nodeInfo.IsConfigSrv() && util.IsSelective(nss) {
		err = r.snapshot(rdr, cloneNS, true)
		if err != nil {
			return errors.Wrap(err, "mongorestore")
		}

		// restore cluster specific configs only
		if err := r.configsvrRestore(ctx, bcp, nss, mapRS); err != nil {
			return err
		}
	} else {
		err = r.snapshot(rdr, cloneNS, false)
		if err != nil {
			return errors.Wrap(err, "mongorestore")
		}
	}

	if usersAndRolesOpt {
		if err := r.restoreUsersAndRoles(ctx, nss); err != nil {
			return errors.Wrap(err, "restoring users and roles")
		}
	}

	return nil
}

func (r *Restore) restoreLegacyArchive(
	ctx context.Context,
	dump string,
	bcp *backup.BackupMeta,
) error {
	sr, err := r.bcpStg.SourceReader(dump)
	if err != nil {
		return errors.Wrapf(err, "get object %s for the storage", dump)
	}
	defer sr.Close()

	rdr, err := compress.Decompress(sr, bcp.Compression)
	if err != nil {
		return errors.Wrapf(err, "decompress object %s", dump)
	}
	defer rdr.Close()

	// Restore snapshot (mongorestore)
	err = r.snapshot(rdr, snapshot.CloneNS{}, false)
	if err != nil {
		return errors.Wrap(err, "mongorestore")
	}

	if err := r.restoreUsersAndRoles(ctx, nil); err != nil {
		return errors.Wrap(err, "restoring users and roles")
	}

	return nil
}

func (r *Restore) checkForCompatibility(
	ctx context.Context,
	name string,
	bcp *backup.BackupMeta,
) error {
	var rs *backup.BackupReplset
	for i := range bcp.Replsets {
		if bcp.Replsets[i].Name == name {
			rs = &bcp.Replsets[i]
			break
		}
	}
	if rs == nil {
		// it is ok if target cluster has more shards then source.
		// however, data for configsvr is required
		if !r.brief.ConfigSvr {
			return ErrNoDataForConfigsvr
		}

		return ErrNoDataForShard
	}

	isRSConfigsvr := rs.IsConfigSvr != nil && *rs.IsConfigSvr
	if isRSConfigsvr && !r.brief.ConfigSvr {
		return errors.New("cannot restore configsvr data to non-configsvr")
	}
	if !isRSConfigsvr && r.brief.ConfigSvr {
		return errors.New("cannot restore non-configsvr data to configsvr")
	}

	isConfigShard := false
	if r.brief.ConfigSvr && r.brief.Version.IsConfigShardSupported() {
		var err error
		isConfigShard, err = topo.HasConfigShard(ctx, r.leadConn)
		if err != nil {
			return errors.Wrap(err, "has configshard")
		}
	}

	isRSConfigShard := rs.IsConfigShard != nil && *rs.IsConfigShard
	if isRSConfigShard && !isConfigShard {
		return errors.New("cannot restore configshard data to non-configshard")
	}
	if !isRSConfigShard && isConfigShard {
		return errors.New("cannot restore non-configshard data to configshard")
	}

	return nil
}

func (r *Restore) restoreUsersAndRoles(ctx context.Context, nss []string) error {
	r.log.Info("restoring users and roles")
	cusr, err := topo.CurrentUser(ctx, r.nodeConn)
	if err != nil {
		return errors.Wrap(err, "get current user")
	}

	err = r.swapUsers(ctx, cusr, nss)
	if err != nil {
		return errors.Wrap(err, "swap users 'n' roles")
	}

	err = util.DropTMPcoll(ctx, r.nodeConn)
	if err != nil {
		r.log.Warning("drop tmp collections: %v", err)
	}

	return nil
}

func (r *Restore) loadIndexesFrom(rdr io.Reader, cloneNS snapshot.CloneNS) error {
	meta, err := archive.ReadMetadata(rdr)
	if err != nil {
		return errors.Wrap(err, "read metadata")
	}

	fromDB, fromColl := cloneNS.SplitFromNS()
	toDB, toColl := cloneNS.SplitToNS()

	for _, ns := range meta.Namespaces {
		var md mongorestore.Metadata
		err := bson.UnmarshalExtJSON([]byte(ns.Metadata), true, &md)
		if err != nil {
			return errors.Wrapf(err, "unmarshal %s.%s metadata",
				ns.Database, ns.Collection)
		}

		if cloneNS.IsSpecified() && ns.Database == fromDB && ns.Collection == fromColl {
			r.indexCatalog.AddIndexes(toDB, toColl, md.Indexes)
		} else {
			r.indexCatalog.AddIndexes(ns.Database, ns.Collection, md.Indexes)
		}

		simple := true
		if md.Options != nil {
			collation, ok := bson.D{}, false
			for _, item := range md.Options {
				if item.Key == "collation" {
					collation = item.Value.(bson.D)
					ok = true
					break
				}
			}
			if ok {
				locale, _ := bsonutil.FindValueByKey("locale", &collation)
				if locale != "" && locale != "simple" {
					simple = false
				}
			}
		}
		if simple {
			r.indexCatalog.SetCollation(ns.Database, ns.Collection, simple)
		}
	}

	return nil
}

func (r *Restore) restoreIndexes(ctx context.Context, nss []string) error {
	r.log.Debug("building indexes up")

	isSelected := util.MakeSelectedPred(nss)
	for _, ns := range r.indexCatalog.Namespaces() {
		if ns := archive.NSify(ns.DB, ns.Collection); !isSelected(ns) {
			r.log.Debug("skip restore indexes for %q", ns)
			continue
		}

		indexes := r.indexCatalog.GetIndexes(ns.DB, ns.Collection)
		for i, index := range indexes {
			if len(index.Key) == 1 && index.Key[0].Key == "_id" {
				// The _id index is already created with the collection
				indexes = append(indexes[:i], indexes[i+1:]...)
				break
			}
		}

		if len(indexes) == 0 {
			r.log.Debug("no indexes for %s.%s", ns.DB, ns.Collection)
			continue
		}

		var indexNames []string
		for _, index := range indexes {
			index.Options["ns"] = ns.DB + "." + ns.Collection
			indexNames = append(indexNames, index.Options["name"].(string))
			// remove the index version, forcing an update
			delete(index.Options, "v")
		}

		rawCommand := bson.D{
			{"createIndexes", ns.Collection},
			{"indexes", indexes},
			{"ignoreUnknownIndexOptions", true},
		}

		r.log.Info("restoring indexes for %s.%s: %s",
			ns.DB, ns.Collection, strings.Join(indexNames, ", "))
		err := r.nodeConn.Database(ns.DB).RunCommand(ctx, rawCommand).Err()
		if err != nil {
			return errors.Wrapf(err, "createIndexes for %s.%s", ns.DB, ns.Collection)
		}
	}

	return nil
}

func (r *Restore) updateRouterConfig(ctx context.Context) error {
	if len(r.sMap) == 0 || !r.nodeInfo.IsSharded() {
		return nil
	}

	if r.nodeInfo.IsConfigSrv() {
		r.log.Debug("updating router config")
		if err := updateRouterTables(ctx, r.leadConn, r.sMap); err != nil {
			return err
		}
	}

	res := r.leadConn.AdminCommand(ctx, bson.D{{"flushRouterConfig", 1}})
	return errors.Wrap(res.Err(), "flushRouterConfig")
}

func updateRouterTables(ctx context.Context, m connect.Client, sMap map[string]string) error {
	if err := updateDatabasesRouterTable(ctx, m, sMap); err != nil {
		return errors.Wrap(err, "databases")
	}

	if err := updateChunksRouterTable(ctx, m, sMap); err != nil {
		return errors.Wrap(err, "chunks")
	}

	return nil
}

func updateDatabasesRouterTable(ctx context.Context, m connect.Client, sMap map[string]string) error {
	coll := m.ConfigDatabase().Collection("databases")

	oldNames := make(primitive.A, 0, len(sMap))
	for k := range sMap {
		oldNames = append(oldNames, k)
	}

	q := primitive.M{"primary": primitive.M{"$in": oldNames}}
	cur, err := coll.Find(ctx, q)
	if err != nil {
		return errors.Wrap(err, "query")
	}

	models := make([]mongo.WriteModel, 0)
	for cur.Next(ctx) {
		var doc struct {
			ID      string `bson:"_id"`
			Primary string `bson:"primary"`
		}
		if err := cur.Decode(&doc); err != nil {
			return errors.Wrap(err, "decode")
		}

		m := mongo.NewUpdateOneModel()
		m.SetFilter(primitive.M{"_id": doc.ID})
		m.SetUpdate(primitive.M{"$set": primitive.M{"primary": sMap[doc.Primary]}})

		models = append(models, m)
	}
	if err := cur.Err(); err != nil {
		return errors.Wrap(err, "cursor")
	}
	if len(models) == 0 {
		return nil
	}

	_, err = coll.BulkWrite(ctx, models)
	return errors.Wrap(err, "bulk write")
}

func updateChunksRouterTable(ctx context.Context, m connect.Client, sMap map[string]string) error {
	coll := m.ConfigDatabase().Collection("chunks")

	oldNames := make(primitive.A, 0, len(sMap))
	for k := range sMap {
		oldNames = append(oldNames, k)
	}

	q := primitive.M{"history.shard": primitive.M{"$in": oldNames}}
	cur, err := coll.Find(ctx, q)
	if err != nil {
		return errors.Wrap(err, "query")
	}

	models := make([]mongo.WriteModel, 0)
	for cur.Next(ctx) {
		var doc struct {
			ID      any    `bson:"_id"`
			Shard   string `bson:"shard"`
			History []struct {
				Shard string `bson:"shard"`
			} `bson:"history"`
		}
		if err := cur.Decode(&doc); err != nil {
			return errors.Wrap(err, "decode")
		}

		updates := primitive.M{}
		if n, ok := sMap[doc.Shard]; ok {
			updates["shard"] = n
		}

		for i, h := range doc.History {
			if n, ok := sMap[h.Shard]; ok {
				updates[fmt.Sprintf("history.%d.shard", i)] = n
			}
		}

		m := mongo.NewUpdateOneModel()
		m.SetFilter(primitive.M{"_id": doc.ID})
		m.SetUpdate(primitive.M{"$set": updates})
		models = append(models, m)
	}
	if err := cur.Err(); err != nil {
		return errors.Wrap(err, "cursor")
	}
	if len(models) == 0 {
		return nil
	}

	_, err = coll.BulkWrite(ctx, models)
	return errors.Wrap(err, "bulk write")
}

func (r *Restore) setcommittedTxn(ctx context.Context, txn []phys.RestoreTxn) error {
	return RestoreSetRSTxn(ctx, r.leadConn, r.name, r.nodeInfo.SetName, txn)
}

func (r *Restore) getcommittedTxn(ctx context.Context) (map[string]primitive.Timestamp, error) {
	txn := make(map[string]primitive.Timestamp)

	shards := make(map[string]struct{})
	for _, s := range r.shards {
		shards[s.RS] = struct{}{}
	}

	for len(shards) > 0 {
		bmeta, err := GetRestoreMeta(ctx, r.leadConn, r.name)
		if err != nil {
			return nil, errors.Wrap(err, "get restore metadata")
		}

		clusterTime, err := topo.GetClusterTime(ctx, r.leadConn)
		if err != nil {
			return nil, errors.Wrap(err, "read cluster time")
		}

		// not going directly thru bmeta.Replsets to be sure we've heard back
		// from all participated in the restore shards.
		for _, shard := range bmeta.Replsets {
			if _, ok := shards[shard.Name]; !ok {
				continue
			}
			// check if node alive
			lck, err := lock.GetLockData(ctx, r.leadConn, &lock.LockHeader{
				Type:    ctrl.CmdRestore,
				OPID:    r.opid,
				Replset: shard.Name,
			})

			// nodes are cleaning its locks moving to the done status
			// so no lock is ok, and no need to check the heartbeats
			if !errors.Is(err, mongo.ErrNoDocuments) {
				if err != nil {
					return nil, errors.Wrapf(err, "unable to read lock for shard %s", shard.Name)
				}
				if lck.Heartbeat.T+defs.StaleFrameSec < clusterTime.T {
					return nil, errors.Errorf("lost shard %s, last beat ts: %d", shard.Name, lck.Heartbeat.T)
				}
			}

			if shard.Status == defs.StatusError {
				return nil, errors.Errorf("shard %s failed with: %v", shard.Name, shard.Error)
			}

			if shard.CommittedTxnSet {
				for _, t := range shard.CommittedTxn {
					if t.State == phys.TxnCommit {
						txn[t.ID] = t.Ctime
					}
				}
				delete(shards, shard.Name)
			}
		}
		time.Sleep(time.Second * 1)
	}

	return txn, nil
}

func (r *Restore) applyOplog(ctx context.Context, ranges []oplogRange, options *applyOplogOption) error {
	mgoV, err := version.GetMongoVersion(ctx, r.nodeConn)
	if err != nil || len(mgoV.Version) < 1 {
		return errors.Wrap(err, "define mongo version")
	}
	stat := phys.RestoreShardStat{}
	partial, err := applyOplog(ctx,
		r.nodeConn,
		ranges,
		options,
		r.nodeInfo.IsSharded(),
		r.indexCatalog,
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

		err = RestoreSetRSPartTxn(ctx, r.leadConn, r.name, r.nodeInfo.SetName, tops)
		if err != nil {
			return errors.Wrap(err, "set partial transactions")
		}
	}

	err = RestoreSetRSStat(ctx, r.leadConn, r.name, r.nodeInfo.SetName, stat)
	if err != nil {
		r.log.Warning("applyOplog: failed to set stat: %v", err)
	}

	return nil
}

func (r *Restore) snapshot(input io.Reader, cloneNS snapshot.CloneNS, excludeRouterCollections bool) error {
	rf, err := snapshot.NewRestore(
		r.brief.URI,
		r.cfg, cloneNS,
		r.numParallelColls,
		r.numInsertionWorkersPerCol,
		excludeRouterCollections)
	if err != nil {
		return err
	}

	_, err = rf.ReadFrom(input)
	return err
}

// Done waits for the replicas to finish the job
// and marks restore as done
func (r *Restore) Done(ctx context.Context) error {
	err := ChangeRestoreRSState(ctx, r.leadConn, r.name, r.nodeInfo.SetName, defs.StatusDone, "")
	if err != nil {
		return errors.Wrap(err, "set shard's StatusDone")
	}

	if r.nodeInfo.IsLeader() {
		err = r.reconcileStatus(ctx, defs.StatusDone, nil)
		if err != nil {
			return errors.Wrap(err, "check cluster for the restore done")
		}

		m, err := GetRestoreMeta(ctx, r.leadConn, r.name)
		if err != nil {
			return errors.Wrap(err, "update stat: get restore meta")
		}
		if m == nil {
			return nil
		}

		stat := make(map[string]map[string]phys.RestoreRSMetrics)

		for _, rs := range m.Replsets {
			stat[rs.Name] = map[string]phys.RestoreRSMetrics{
				"_primary": {DistTxn: phys.DistTxnStat{
					Partial:          rs.Stat.Txn.Partial,
					ShardUncommitted: rs.Stat.Txn.ShardUncommitted,
					LeftUncommitted:  rs.Stat.Txn.LeftUncommitted,
				}},
			}
		}

		err = RestoreSetStat(ctx, r.leadConn, r.name, phys.RestoreStat{RS: stat})
		if err != nil {
			return errors.Wrap(err, "set restore stat")
		}
	}

	return nil
}

func (r *Restore) swapUsers(ctx context.Context, exclude *topo.AuthInfo, nss []string) error {
	dbs := []string{}
	for _, ns := range nss {
		// ns can be "*.*" or "admin.pbmRUsers" or "admin.pbmRRoles"
		db, _ := util.ParseNS(ns)
		if len(db) == 0 || strings.HasPrefix(db, defs.DB) {
			continue
		}
		dbs = append(dbs, db)
	}

	rolesC := r.nodeConn.Database("admin").Collection("system.roles")

	eroles := []string{}
	for _, r := range exclude.UserRoles {
		eroles = append(eroles, r.DB+"."+r.Role)
	}

	rolesFilter := bson.M{
		"_id": bson.M{"$nin": eroles},
	}
	if len(dbs) > 0 {
		rolesFilter["db"] = bson.M{"$in": dbs}
	}

	curr, err := r.nodeConn.Database(defs.DB).Collection(defs.TmpRolesCollection).
		Find(ctx, rolesFilter)
	if err != nil {
		return errors.Wrap(err, "create cursor for tmpRoles")
	}
	defer curr.Close(ctx)

	_, err = rolesC.DeleteMany(ctx, rolesFilter)
	if err != nil {
		return errors.Wrap(err, "delete current roles")
	}

	for curr.Next(ctx) {
		var rl any
		err := curr.Decode(&rl)
		if err != nil {
			return errors.Wrap(err, "decode role")
		}
		_, err = rolesC.InsertOne(ctx, rl)
		if err != nil {
			return errors.Wrap(err, "insert role")
		}
	}

	user := ""
	if len(exclude.Users) > 0 {
		user = exclude.Users[0].DB + "." + exclude.Users[0].User
	}
	filterUsers := bson.M{
		"_id": bson.M{"$ne": user},
	}
	if len(dbs) > 0 {
		filterUsers["db"] = bson.M{"$in": dbs}
	}
	cur, err := r.nodeConn.Database(defs.DB).Collection(defs.TmpUsersCollection).
		Find(ctx, filterUsers)
	if err != nil {
		return errors.Wrap(err, "create cursor for tmpUsers")
	}
	defer cur.Close(ctx)

	usersC := r.nodeConn.Database("admin").Collection("system.users")
	_, err = usersC.DeleteMany(ctx, filterUsers)
	if err != nil {
		return errors.Wrap(err, "delete current users")
	}

	for cur.Next(ctx) {
		var u any
		err := cur.Decode(&u)
		if err != nil {
			return errors.Wrap(err, "decode user")
		}
		_, err = usersC.InsertOne(ctx, u)
		if err != nil {
			return errors.Wrap(err, "insert user")
		}
	}

	return nil
}

func (r *Restore) reconcileStatus(ctx context.Context, status defs.Status, timeout *time.Duration) error {
	if timeout != nil {
		err := convergeClusterWithTimeout(ctx, r.leadConn, r.name, r.opid, r.shards, status, *timeout)
		return errors.Wrap(err, "convergeClusterWithTimeout")
	}
	err := convergeCluster(ctx, r.leadConn, r.name, r.opid, r.shards, status)
	return errors.Wrap(err, "convergeCluster")
}

func (r *Restore) waitForStatus(ctx context.Context, status defs.Status) error {
	r.log.Debug("waiting for '%s' status", status)
	return waitForStatus(ctx, r.leadConn, r.name, status)
}

// MarkFailed sets the restore and rs state as failed with the given message
func (r *Restore) MarkFailed(ctx context.Context, e error) error {
	err := ChangeRestoreState(ctx, r.leadConn, r.name, defs.StatusError, e.Error())
	if err != nil {
		return errors.Wrap(err, "set restore state")
	}
	err = ChangeRestoreRSState(ctx, r.leadConn, r.name, r.nodeInfo.SetName, defs.StatusError, e.Error())
	return errors.Wrap(err, "set replset state")
}
