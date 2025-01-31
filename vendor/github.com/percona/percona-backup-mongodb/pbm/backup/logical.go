package backup

import (
	"context"
	"io"
	"path"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/archive"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/snapshot"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

func (b *Backup) doLogical(
	ctx context.Context,
	bcp *ctrl.BackupCmd,
	opid ctrl.OPID,
	rsMeta *BackupReplset,
	inf *topo.NodeInfo,
	stg storage.Storage,
	l log.LogEvent,
) error {
	if b.brief.ConfigSvr {
		if err := b.checkForTimeseries(ctx, bcp.Namespaces); err != nil {
			return errors.Wrap(err, "check for timeseries")
		}
	}
	nssSize, err := getNamespacesSize(ctx, b.nodeConn, bcp.Namespaces)
	if err != nil {
		return errors.Wrap(err, "get namespaces size")
	}
	if bcp.Compression == compress.CompressionTypeNone {
		for n := range nssSize {
			nssSize[n] *= 4
		}
	}

	rsMeta.Status = defs.StatusRunning
	rsMeta.OplogName = path.Join(bcp.Name, rsMeta.Name, "oplog")
	rsMeta.DumpName = path.Join(bcp.Name, rsMeta.Name, archive.MetaFile)
	err = AddRSMeta(ctx, b.leadConn, bcp.Name, *rsMeta)
	if err != nil {
		return errors.Wrap(err, "add shard's metadata")
	}

	if inf.IsLeader() {
		err := b.reconcileStatus(ctx,
			bcp.Name, opid.String(), defs.StatusRunning, util.Ref(b.timeouts.StartingStatus()))
		if err != nil {
			if errors.Is(err, errConvergeTimeOut) {
				return errors.Wrap(err, "couldn't get response from all shards")
			}
			return errors.Wrap(err, "check cluster for backup started")
		}

		// TODO(improve): do setClusterFirstWrite between
		// all replsets status are StatusRunning and setting the global status
		err = b.setClusterFirstWrite(ctx, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "set cluster first write ts")
		}
	} else {
		// Waiting for cluster's StatusRunning to move further.
		err = b.waitForStatus(ctx, bcp.Name, defs.StatusRunning, nil)
		if err != nil {
			return errors.Wrap(err, "waiting for running")
		}
	}

	stopOplogSlicer := startOplogSlicer(ctx,
		b.nodeConn,
		b.leadConn.MongoOptions().WriteConcern,
		b.SlicerInterval(),
		rsMeta.FirstWriteTS,
		func(ctx context.Context, w io.WriterTo, from, till primitive.Timestamp) (int64, error) {
			filename := rsMeta.OplogName + "/" + FormatChunkName(from, till, bcp.Compression)
			return storage.Upload(ctx, w, stg, bcp.Compression, bcp.CompressionLevel, filename, -1)
		})
	// ensure slicer is stopped in any case (done, error or canceled)
	defer stopOplogSlicer() //nolint:errcheck

	if !util.IsSelective(bcp.Namespaces) {
		// Save users and roles to the tmp collections so the restore would copy that data
		// to the system collections. Have to do this because of issues with the restore and preserverUUID.
		// see: https://jira.percona.com/browse/PBM-636 and comments
		lw, err := copyUsersNRolles(ctx, b.brief.URI)
		if err != nil {
			return errors.Wrap(err, "copy users and roles for the restore")
		}

		defer func() {
			l.Info("dropping tmp collections")
			if err := dropTMPcoll(context.Background(), b.brief.URI); err != nil {
				l.Warning("drop tmp users and roles: %v", err)
			}
		}()

		// before proceeding any further we have to be sure that tmp users and roles
		// have replicated to the node we're about to take a backup from
		// *copying made on a primary but backup does a secondary node
		l.Debug("wait for tmp users %v", lw)
		err = waitForWrite(ctx, b.nodeConn, lw)
		if err != nil {
			return errors.Wrap(err, "wait for tmp users and roles replication")
		}
	}

	numParallelColls := b.numParallelColls
	if bcp.NumParallelColls != nil {
		if *bcp.NumParallelColls > 0 {
			numParallelColls = int(*bcp.NumParallelColls)
		} else {
			l.Warning("invalid value of NumParallelCollections (%v). fallback to %v",
				numParallelColls, b.numParallelColls)
		}
	}
	l.Debug("dumping up to %d collections in parallel", numParallelColls)

	nsFilter := archive.DefaultNSFilter
	docFilter := archive.DefaultDocFilter
	if util.IsSelective(bcp.Namespaces) {
		if inf.IsConfigSrv() {
			chunkSelector, err := createBackupChunkSelector(ctx, b.leadConn, bcp.Namespaces)
			if err != nil {
				return errors.Wrap(err, "fetch uuids")
			}

			docFilter = makeConfigsvrDocFilter(bcp.Namespaces, chunkSelector)
			nsFilter = makeConfigsvrNSFilter(bcp.Namespaces)

		} else {
			nsFilter = util.MakeSelectedPred(bcp.Namespaces)
		}
	}

	snapshotSize, err := snapshot.UploadDump(ctx,
		func(newFile archive.NewWriter) error {
			bcp, err := archive.NewBackup(ctx, archive.BackupOptions{
				Client:        b.nodeConn,
				NewFile:       newFile,
				NSFilter:      nsFilter,
				DocFilter:     docFilter,
				ParallelColls: numParallelColls,
			})
			if err != nil {
				return errors.Wrap(err, "new backup")
			}

			return bcp.Run(ctx)
		},
		func(ns, ext string, r io.Reader) error {
			stg, err := util.StorageFromConfig(&b.config.Storage, b.brief.Me, l)
			if err != nil {
				return errors.Wrap(err, "get storage")
			}
			filepath := path.Join(bcp.Name, rsMeta.Name, ns+ext)
			return stg.Save(filepath, r, nssSize[ns])
		},
		bcp.Compression,
		bcp.CompressionLevel)
	if err != nil {
		return errors.Wrap(err, "dump")
	}

	err = archive.GenerateV1FromV2(ctx, stg, bcp.Name, rsMeta.Name)
	if err != nil {
		return errors.Wrap(err, "generate archive meta v1")
	}

	l.Info("dump finished, waiting for the oplog")

	err = ChangeRSState(b.leadConn, bcp.Name, rsMeta.Name, defs.StatusDumpDone, "")
	if err != nil {
		return errors.Wrap(err, "set shard's StatusDumpDone")
	}

	if inf.IsLeader() {
		err := b.reconcileStatus(ctx, bcp.Name, opid.String(), defs.StatusDumpDone, nil)
		if err != nil {
			return errors.Wrap(err, "check cluster for dump done")
		}
	} else {
		err = b.waitForStatus(ctx, bcp.Name, defs.StatusDumpDone, nil)
		if err != nil {
			return errors.Wrap(err, "waiting for dump done")
		}
	}

	lastSavedTS, oplogSize, err := stopOplogSlicer()
	if err != nil {
		return errors.Wrap(err, "oplog")
	}

	err = SetRSLastWrite(b.leadConn, bcp.Name, rsMeta.Name, lastSavedTS)
	if err != nil {
		return errors.Wrap(err, "set shard's last write ts")
	}

	if inf.IsLeader() {
		err = b.setClusterLastWrite(ctx, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "set cluster last write ts")
		}
	}

	err = IncBackupSize(ctx, b.leadConn, bcp.Name, snapshotSize+oplogSize)
	if err != nil {
		return errors.Wrap(err, "inc backup size")
	}

	return nil
}

func dropTMPcoll(ctx context.Context, uri string) error {
	m, err := connect.MongoConnect(ctx, uri)
	if err != nil {
		return errors.Wrap(err, "connect to primary")
	}
	defer m.Disconnect(ctx) //nolint:errcheck

	err = util.DropTMPcoll(ctx, m)
	if err != nil {
		return err
	}

	return nil
}

func waitForWrite(ctx context.Context, m *mongo.Client, ts primitive.Timestamp) error {
	var lw primitive.Timestamp
	var err error

	for i := 0; i < 21; i++ {
		lw, err = topo.GetLastWrite(ctx, m, false)
		if err == nil && lw.Compare(ts) >= 0 {
			return nil
		}
		time.Sleep(time.Second * 1)
	}

	if err != nil {
		return err
	}

	return errors.New("run out of time")
}

//nolint:nonamedreturns
func copyUsersNRolles(ctx context.Context, uri string) (lastWrite primitive.Timestamp, err error) {
	cn, err := connect.MongoConnect(ctx, uri)
	if err != nil {
		return lastWrite, errors.Wrap(err, "connect to primary")
	}
	defer cn.Disconnect(ctx) //nolint:errcheck

	err = util.DropTMPcoll(ctx, cn)
	if err != nil {
		return lastWrite, errors.Wrap(err, "drop tmp collections before copy")
	}

	err = copyColl(ctx,
		cn.Database("admin").Collection("system.roles"),
		cn.Database(defs.DB).Collection(defs.TmpRolesCollection),
		bson.M{},
	)
	if err != nil {
		return lastWrite, errors.Wrap(err, "copy admin.system.roles")
	}
	err = copyColl(ctx,
		cn.Database("admin").Collection("system.users"),
		cn.Database(defs.DB).Collection(defs.TmpUsersCollection),
		bson.M{},
	)
	if err != nil {
		return lastWrite, errors.Wrap(err, "copy admin.system.users")
	}

	return topo.GetLastWrite(ctx, cn, false)
}

// copyColl copy documents matching the given filter and return number of copied documents
func copyColl(ctx context.Context, from, to *mongo.Collection, filter any) error {
	cur, err := from.Find(ctx, filter)
	if err != nil {
		return errors.Wrap(err, "create cursor")
	}
	defer cur.Close(ctx)

	n := 0
	for cur.Next(ctx) {
		_, err = to.InsertOne(ctx, cur.Current)
		if err != nil {
			return errors.Wrap(err, "insert document")
		}
		n++
	}

	return nil
}

func createBackupChunkSelector(ctx context.Context, m connect.Client, nss []string) (util.ChunkSelector, error) {
	ver, err := version.GetMongoVersion(ctx, m.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "get mongo version")
	}

	var chunkSelector util.ChunkSelector
	if ver.Major() >= 5 {
		chunkSelector = util.NewUUIDChunkSelector()
	} else {
		chunkSelector = util.NewNSChunkSelector()
	}

	cur, err := m.ConfigDatabase().Collection("collections").Find(ctx, bson.D{})
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}
	defer cur.Close(ctx)

	selected := util.MakeSelectedPred(nss)
	for cur.Next(ctx) {
		ns := cur.Current.Lookup("_id").StringValue()
		if selected(ns) {
			chunkSelector.Add(cur.Current)
		}
	}
	if err := cur.Err(); err != nil {
		return nil, errors.Wrap(err, "cursor")
	}

	return chunkSelector, nil
}

func makeConfigsvrNSFilter(nss []string) archive.NSFilterFn {
	selected := make([]string, len(nss)+len(archive.RouterConfigCollections))
	n := copy(selected, nss)

	// list of required namespaces for further selective restore
	for i, name := range archive.RouterConfigCollections {
		selected[n+i] = "config." + name
	}

	return util.MakeSelectedPred(selected)
}

func makeConfigsvrDocFilter(nss []string, selector util.ChunkSelector) archive.DocFilterFn {
	selectedNS := util.MakeSelectedPred(nss)
	allowedDBs := make(map[string]bool)
	for _, ns := range nss {
		db, _, _ := strings.Cut(ns, ".")
		allowedDBs[db] = true
	}

	return func(ns string, doc bson.Raw) bool {
		switch ns {
		case "config.databases":
			db, ok := doc.Lookup("_id").StringValueOK()
			return ok && allowedDBs[db]
		case "config.collections":
			ns, ok := doc.Lookup("_id").StringValueOK()
			return ok && selectedNS(ns)
		case "config.chunks":
			return selector.Selected(doc)
		}

		return false
	}
}

func getNamespacesSize(ctx context.Context, m *mongo.Client, nss []string) (map[string]int64, error) {
	rv := make(map[string]int64)

	dbs, err := m.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, errors.Wrap(err, "list databases")
	}
	if len(dbs) == 0 {
		return rv, nil
	}

	isSelected := util.MakeSelectedPred(nss)

	mu := sync.Mutex{}
	eg, ctx := errgroup.WithContext(ctx)

	for _, db := range dbs {
		db := db

		eg.Go(func() error {
			res, err := m.Database(db).ListCollectionSpecifications(ctx, bson.D{})
			if err != nil {
				return errors.Wrapf(err, "list collections for %q", db)
			}
			if len(res) == 0 {
				return nil
			}

			eg, ctx := errgroup.WithContext(ctx)

			for _, coll := range res {
				if coll.Type == "view" || strings.HasPrefix(coll.Name, "system.buckets.") {
					continue
				}

				ns := db + "." + coll.Name
				if !isSelected(ns) {
					continue
				}

				eg.Go(func() error {
					res := m.Database(db).RunCommand(ctx, bson.D{{"collStats", coll.Name}})
					if err := res.Err(); err != nil {
						return errors.Wrapf(err, "collStats %q", ns)
					}

					var doc struct {
						StorageSize int64 `bson:"storageSize"`
					}

					if err := res.Decode(&doc); err != nil {
						return errors.Wrapf(err, "decode %q", ns)
					}

					mu.Lock()
					rv[ns] = doc.StorageSize
					mu.Unlock()

					return nil
				})
			}

			return eg.Wait()
		})
	}

	err = eg.Wait()
	return rv, err
}

func (b *Backup) checkForTimeseries(ctx context.Context, nss []string) error {
	if !b.brief.Version.IsShardedTimeseriesSupported() || !b.brief.Sharded {
		return nil
	}

	tss, err := topo.ListShardedTimeseries(ctx, b.leadConn)
	if err != nil {
		return errors.Wrap(err, "list sharded timeseries")
	}

	if util.IsSelective(nss) {
		selected := util.MakeSelectedPred(nss)
		origTSs := tss
		tss = make([]string, 0, len(tss))
		for _, ts := range origTSs {
			if selected(ts) {
				tss = append(tss, ts)
			}
		}
	}

	if len(tss) != 0 {
		return errors.Errorf("cannot backup following sharded timeseries: %s",
			strings.Join(tss, ", "))
	}

	return nil
}
