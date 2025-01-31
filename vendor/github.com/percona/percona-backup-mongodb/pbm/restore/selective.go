package restore

import (
	"context"
	"io"
	"path"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/archive"
	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

const (
	databasesNS   = "databases"
	collectionsNS = "collections"
	chunksNS      = "chunks"
)

const maxBulkWriteCount = 500

// configsvrRestore restores for selected namespaces
func (r *Restore) configsvrRestore(
	ctx context.Context,
	bcp *backup.BackupMeta,
	nss []string,
	mapRS util.RSMapFunc,
) error {
	mapS := util.MakeRSMapFunc(r.sMap)
	available, err := fetchAvailability(bcp, r.bcpStg)
	if err != nil {
		return err
	}

	if available[databasesNS] {
		if err := r.configsvrRestoreDatabases(ctx, bcp, nss, mapRS, mapS); err != nil {
			return errors.Wrap(err, "restore config.databases")
		}
	}

	var chunkSelector util.ChunkSelector
	if available[collectionsNS] {
		var err error
		chunkSelector, err = r.configsvrRestoreCollections(ctx, bcp, nss, mapRS)
		if err != nil {
			return errors.Wrap(err, "restore config.collections")
		}
	}

	if available[chunksNS] {
		if err := r.configsvrRestoreChunks(ctx, bcp, chunkSelector, mapRS, mapS); err != nil {
			return errors.Wrap(err, "restore config.chunks")
		}
	}

	return nil
}

func fetchAvailability(bcp *backup.BackupMeta, stg storage.Storage) (map[string]bool, error) {
	var cfgRS *backup.BackupReplset
	for i := range bcp.Replsets {
		rs := &bcp.Replsets[i]
		if rs.IsConfigSvr != nil && *rs.IsConfigSvr {
			cfgRS = rs
			break
		}
	}
	if cfgRS == nil {
		return nil, errors.New("no configsvr replset metadata found")
	}

	nss, err := backup.ReadArchiveNamespaces(stg, cfgRS.DumpName)
	if err != nil {
		return nil, errors.Wrapf(err, "read archive namespaces %q", cfgRS.DumpName)
	}

	rv := make(map[string]bool)
	for _, ns := range nss {
		switch ns.Collection {
		case databasesNS, collectionsNS, chunksNS:
			rv[ns.Collection] = ns.Size != 0
		}
	}

	return rv, nil
}

func (r *Restore) getShardMapping(bcp *backup.BackupMeta) map[string]string {
	source := bcp.ShardRemap
	if source == nil {
		source = make(map[string]string)
	}

	mapRevRS := util.MakeReverseRSMapFunc(r.rsMap)
	rv := make(map[string]string)
	for _, s := range r.shards {
		sourceRS := mapRevRS(s.RS)
		sourceS := source[sourceRS]
		if sourceS == "" {
			sourceS = sourceRS
		}
		if sourceS != s.ID {
			rv[sourceS] = s.ID
		}
	}

	return rv
}

// configsvrRestoreDatabases upserts config.databases documents
// for selected databases
func (r *Restore) configsvrRestoreDatabases(
	ctx context.Context,
	bcp *backup.BackupMeta,
	nss []string,
	mapRS, mapS util.RSMapFunc,
) error {
	filepath := path.Join(bcp.Name, mapRS(r.brief.SetName), "config.databases"+bcp.Compression.Suffix())
	rdr, err := r.bcpStg.SourceReader(filepath)
	if err != nil {
		return err
	}
	rdr, err = compress.Decompress(rdr, bcp.Compression)
	if err != nil {
		return err
	}

	allowedDBs := make(map[string]bool)
	for _, ns := range nss {
		db, _, _ := strings.Cut(ns, ".")
		allowedDBs[db] = true
	}

	// go config.databases' docs and pick only selected databases docs
	// insert/replace in bulk
	models := []mongo.WriteModel{}
	buf := make([]byte, archive.MaxBSONSize)
	for {
		buf, err = archive.ReadBSONBuffer(rdr, buf[:cap(buf)])
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return err
		}

		db := bson.Raw(buf).Lookup("_id").StringValue()
		if !allowedDBs[db] {
			continue
		}

		doc := bson.D{}
		if err = bson.Unmarshal(buf, &doc); err != nil {
			return errors.Wrap(err, "unmarshal")
		}

		for i, a := range doc {
			if a.Key == "primary" {
				doc[i].Value = mapS(doc[i].Value.(string))
				break
			}
		}

		model := mongo.NewReplaceOneModel()
		model.SetFilter(bson.D{{"_id", db}})
		model.SetReplacement(doc)
		model.SetUpsert(true)
		models = append(models, model)
	}

	if len(models) == 0 {
		return nil
	}

	coll := r.leadConn.ConfigDatabase().Collection("databases")
	_, err = coll.BulkWrite(ctx, models)
	return errors.Wrap(err, "update config.databases")
}

// configsvrRestoreCollections upserts config.collections documents
// for selected namespaces
func (r *Restore) configsvrRestoreCollections(
	ctx context.Context,
	bcp *backup.BackupMeta,
	nss []string,
	mapRS util.RSMapFunc,
) (util.ChunkSelector, error) {
	ver, err := version.GetMongoVersion(ctx, r.nodeConn)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo version")
	}

	var chunkSelector util.ChunkSelector
	if ver.Major() >= 5 {
		chunkSelector = util.NewUUIDChunkSelector()
	} else {
		chunkSelector = util.NewNSChunkSelector()
	}

	filepath := path.Join(bcp.Name, mapRS(r.brief.SetName), "config.collections"+bcp.Compression.Suffix())
	rdr, err := r.bcpStg.SourceReader(filepath)
	if err != nil {
		return nil, err
	}
	rdr, err = compress.Decompress(rdr, bcp.Compression)
	if err != nil {
		return nil, err
	}

	selected := util.MakeSelectedPred(nss)

	models := []mongo.WriteModel{}
	buf := make([]byte, archive.MaxBSONSize)
	for {
		buf, err = archive.ReadBSONBuffer(rdr, buf[:cap(buf)])
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, err
		}

		ns := bson.Raw(buf).Lookup("_id").StringValue()
		if !selected(ns) {
			continue
		}

		chunkSelector.Add(bson.Raw(buf))

		doc := bson.D{}
		err = bson.Unmarshal(buf, &doc)
		if err != nil {
			return nil, errors.Wrap(err, "unmarshal")
		}

		model := mongo.NewReplaceOneModel()
		model.SetFilter(bson.D{{"_id", ns}})
		model.SetReplacement(doc)
		model.SetUpsert(true)
		models = append(models, model)
	}

	if len(models) == 0 {
		return chunkSelector, nil
	}

	coll := r.leadConn.ConfigDatabase().Collection("collections")
	if _, err = coll.BulkWrite(ctx, models); err != nil {
		return nil, errors.Wrap(err, "update config.collections")
	}

	return chunkSelector, nil
}

// configsvrRestoreChunks upserts config.chunks documents for selected namespaces
func (r *Restore) configsvrRestoreChunks(
	ctx context.Context,
	bcp *backup.BackupMeta,
	selector util.ChunkSelector,
	mapRS,
	mapS util.RSMapFunc,
) error {
	filepath := path.Join(bcp.Name, mapRS(r.brief.SetName), "config.chunks"+bcp.Compression.Suffix())
	rdr, err := r.bcpStg.SourceReader(filepath)
	if err != nil {
		return err
	}
	rdr, err = compress.Decompress(rdr, bcp.Compression)
	if err != nil {
		return err
	}

	coll := r.leadConn.ConfigDatabase().Collection("chunks")
	_, err = coll.DeleteMany(ctx, selector.BuildFilter())
	if err != nil {
		return err
	}

	models := []mongo.WriteModel{}
	buf := make([]byte, archive.MaxBSONSize)
	for done := false; !done; {
		// there could be thousands of chunks. write every maxBulkWriteCount docs
		// to limit memory usage
		for i := 0; i != maxBulkWriteCount; i++ {
			buf, err = archive.ReadBSONBuffer(rdr, buf[:cap(buf)])
			if err != nil {
				if errors.Is(err, io.EOF) {
					done = true
					break
				}

				return err
			}

			if !selector.Selected(bson.Raw(buf)) {
				continue
			}

			doc := bson.D{}
			if err := bson.Unmarshal(buf, &doc); err != nil {
				return errors.Wrap(err, "unmarshal")
			}

			for i, a := range doc {
				switch a.Key {
				case "shard":
					doc[i].Value = mapS(doc[i].Value.(string))
				case "history":
					history := doc[i].Value.(bson.A)
					for j, b := range history {
						c := b.(bson.D)
						for k, d := range c {
							if d.Key == "shard" {
								c[k].Value = mapS(d.Value.(string))
							}
						}
						history[j] = c
					}
					doc[i].Value = history
				}
			}

			models = append(models, mongo.NewInsertOneModel().SetDocument(doc))
		}

		if len(models) == 0 {
			return nil
		}

		_, err = coll.BulkWrite(ctx, models)
		if err != nil {
			return errors.Wrap(err, "update config.chunks")
		}

		models = models[:0]
	}

	return nil
}
