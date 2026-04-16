package tests

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"log"
	"reflect"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type (
	ShardName = string
	DBName    = string
	CollName  = string
	NSName    = string
)

// clusterState describes a sharded cluster state
type clusterState struct {
	// State of configsvr
	Config map[DBName]configDBState

	// State of shards
	Shards map[ShardName]shardState

	// Amounts of document from mongos for each namespace in cluster.
	Counts map[NSName]int64
}

// configDBState describes a configsvr state
type configDBState struct {
	// Database state
	Spec *dbSpec

	// Collections is sharded collections state
	Collections map[CollName]*configCollState
}

// dbSpec describes a database state. It is a config.databases document
type dbSpec struct {
	ID      string `bson:"_id"`
	Primary string `bson:"primary"`
	Version struct {
		UUID      primitive.Binary    `bson:"uuid"`
		Timestamp primitive.Timestamp `bson:"timestamp"`
		LastMod   int32               `bson:"lastMod,omitempty"` // since v5.0
	} `bson:"version"`
}

// configCollState describes a sharded collection
type configCollState struct {
	// Spec is a config.collections document
	Spec *collSpec

	// Chunk state grouped by shard name
	Chunks chunksState
}

// collSpec describes a sharded collection. It is a config.collections document
type collSpec struct {
	ID           string              `bson:"_id"`
	LastmodEpoch primitive.ObjectID  `bson:"lastmodEpoch"`
	LastMod      primitive.DateTime  `bson:"lastMod"`
	Timestamp    primitive.Timestamp `bson:"timestamp"`
	UUID         *primitive.Binary   `bson:"uuid,omitempty"` // since v5.0
	Key          map[string]any      `bson:"key"`
	Unique       bool                `bson:"unique"`
	ChunksSplit  bool                `bson:"chunksAlreadySplitForDowngrade"`
	NoBalance    bool                `bson:"noBalance"`
}

// chunksState is chunks state for a ns for a shard
type chunksState struct {
	// Computed MD5 value for config.chunks documents for a ns for a shard
	MD5 string

	// Number of config.chunks documents (grouped by shard)
	Counts map[ShardName]int64
}

// shardState describes shards state grouped by shard/replset name.
// It contains all sharded and unsharded collections for each shard in the cluster
type shardState map[NSName]*shardCollState

// shardCollState describes a collection state on a shard
type shardCollState struct {
	// Spec is value of listCollections command
	Spec *mongo.CollectionSpecification

	// Hash is dbHash value on a shard
	Hash string
}

type Credentials struct {
	Username, Password string
}

func ExtractCredentionals(s string) *Credentials {
	auth, _, _ := strings.Cut(strings.TrimPrefix(s, "mongodb://"), "@")
	usr, pwd, _ := strings.Cut(auth, ":")
	if usr == "" {
		return nil
	}

	return &Credentials{usr, pwd}
}

// ClusterState collect cluster state
func ClusterState(ctx context.Context, mongos *mongo.Client, creds *Credentials) (*clusterState, error) {
	ok, err := isMongos(ctx, mongos)
	if err != nil {
		return nil, errors.Wrap(err, "ismongos")
	}
	if !ok {
		return nil, errors.New("mongos connection required")
	}

	// get list of shards and configsvr URIs
	res := mongos.Database("admin").RunCommand(ctx, bson.D{{"getShardMap", 1}})
	if err := res.Err(); err != nil {
		return nil, errors.Wrap(err, "getShardMap: query")
	}

	var shardMap struct{ Map map[ShardName]string }
	if err := res.Decode(&shardMap); err != nil {
		return nil, errors.Wrap(err, "getShardMap: decode")
	}

	rv := &clusterState{
		Shards: make(map[ShardName]shardState),
		Counts: make(map[NSName]int64),
	}

	eg, egc := errgroup.WithContext(ctx)
	eg.Go(func() error {
		var err error
		rv.Counts, err = countDocuments(egc, mongos)
		return errors.Wrap(err, "count documents")
	})

	mu := sync.Mutex{}
	for rs, uri := range shardMap.Map {
		rs := rs
		_, uri, _ := strings.Cut(uri, "/")
		if creds != nil {
			uri = fmt.Sprintf("%s:%s@%s", creds.Username, creds.Password, uri)
		}
		uri = "mongodb://" + uri

		eg.Go(func() error {
			m, err := connect.Connect(ctx, uri, "pbm")
			if err != nil {
				return errors.Wrapf(err, "connect: %q", uri)
			}

			if rs == "config" {
				rv.Config, err = getConfigState(egc, m.MongoClient())
				return errors.Wrapf(err, "config state: %q", uri)
			}

			state, err := getShardState(egc, m.MongoClient())
			if err != nil {
				return errors.Wrapf(err, "shard state: %q", uri)
			}

			mu.Lock()
			defer mu.Unlock()

			rv.Shards[rs] = state
			return nil
		})
	}

	err = eg.Wait()
	return rv, err
}

func Compare(before, after *clusterState, nss []string) bool {
	ok := true
	allowedDBs := make(map[DBName]bool)
	for _, ns := range nss {
		db, _, _ := strings.Cut(ns, ".")
		if db == "*" {
			db = ""
		}
		allowedDBs[db] = true
	}
	selected := util.MakeSelectedPred(nss)

	for db, beforeDBState := range before.Config {
		if !allowedDBs[""] && !allowedDBs[db] {
			continue
		}

		afterDBState := after.Config[db]
		if !reflect.DeepEqual(beforeDBState, afterDBState) {
			log.Printf("config: diff database spec: %q", db)
			ok = false
		}

		for coll, beforeCollState := range beforeDBState.Collections {
			if !selected(db + "." + coll) {
				continue
			}

			afterCollState := afterDBState.Collections[coll]
			if !reflect.DeepEqual(beforeCollState, afterCollState) {
				log.Printf("config: diff namespace spec: %q", coll)
				ok = false
			}
		}
	}

	for shard, beforeShardState := range before.Shards {
		afterShardState, kk := after.Shards[shard]
		if !kk {
			log.Printf("shard: not found: %q", shard)
			ok = false
			continue
		}

		for ns, beforeNSState := range beforeShardState {
			if !selected(ns) {
				continue
			}

			if !reflect.DeepEqual(beforeNSState, afterShardState[ns]) {
				log.Printf("shard: diff namespace spec: %q", ns)
				ok = false
			}
		}
	}

	for ns, beforeCount := range before.Counts {
		if !selected(ns) {
			continue
		}

		if beforeCount != after.Counts[ns] {
			log.Printf("mongos: diff documents count: %q", ns)
			ok = false
		}
	}

	return ok
}

func isMongos(ctx context.Context, m *mongo.Client) (bool, error) {
	res := m.Database("admin").RunCommand(ctx, bson.D{{"hello", 1}})
	if err := res.Err(); err != nil {
		return false, errors.Wrap(err, "query")
	}

	var r struct{ Msg string }
	err := res.Decode(&r)
	return r.Msg == "isdbgrid", errors.Wrap(err, "decode")
}

func countDocuments(ctx context.Context, mongos *mongo.Client) (map[string]int64, error) {
	f := bson.D{{"name", bson.M{"$nin": bson.A{"admin", "config", "local"}}}}
	dbs, err := mongos.ListDatabaseNames(ctx, f)
	if err != nil {
		return nil, errors.Wrap(err, "list databases")
	}

	rv := make(map[NSName]int64)

	for _, d := range dbs {
		db := mongos.Database(d)

		colls, err := db.ListCollectionNames(ctx, bson.D{})
		if err != nil {
			return nil, errors.Wrapf(err, "list collections: %q", d)
		}

		for _, c := range colls {
			count, err := db.Collection(c).CountDocuments(ctx, bson.D{})
			if err != nil {
				return nil, errors.Wrapf(err, "count: %q", d+"."+c)
			}

			rv[d+"."+c] = count
		}
	}

	return rv, nil
}

func getConfigDatabases(ctx context.Context, m *mongo.Client) ([]*dbSpec, error) {
	cur, err := m.Database("config").Collection("databases").Find(ctx, bson.D{})
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}

	rv := []*dbSpec{}
	err = cur.All(ctx, &rv)
	return rv, errors.Wrap(err, "cursor: all")
}

func getConfigCollections(ctx context.Context, m *mongo.Client) ([]*collSpec, error) {
	f := bson.D{{"_id", bson.M{"$regex": `^(?!(config|system)\.)`}}}
	cur, err := m.Database("config").Collection("collections").Find(ctx, f)
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}

	rv := []*collSpec{}
	err = cur.All(ctx, &rv)
	return rv, errors.Wrap(err, "cursor: all")
}

func getConfigChunkHashes(
	ctx context.Context,
	m *mongo.Client,
	selection map[string]CollName,
	useUUID bool,
) (map[NSName]chunksState, error) {
	hashes := make(map[string]hash.Hash)
	counts := make(map[string]map[ShardName]int64)

	var f bson.D
	if useUUID {
		in := make([]primitive.Binary, 0, len(selection))
		for uuid := range selection {
			hashes[uuid] = md5.New()
			counts[uuid] = make(map[ShardName]int64)
			data, _ := hex.DecodeString(uuid)
			in = append(in, primitive.Binary{Subtype: 0x4, Data: data})
		}

		f = bson.D{{"uuid", bson.M{"$in": in}}}
	} else {
		in := make([]string, 0, len(selection))
		for ns := range selection {
			hashes[ns] = md5.New()
			counts[ns] = make(map[ShardName]int64)
			in = append(in, ns)
		}

		f = bson.D{{"ns", bson.M{"$in": in}}}
	}

	cur, err := m.Database("config").Collection("chunks").Find(ctx, f)
	if err != nil {
		return nil, errors.Wrap(err, "query")
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		var id string
		if useUUID {
			_, data := cur.Current.Lookup("uuid").Binary()
			id = hex.EncodeToString(data)
		} else {
			id = cur.Current.Lookup("ns").StringValue()
		}
		hashes[id].Write(cur.Current)
		counts[id][cur.Current.Lookup("shard").StringValue()]++
	}
	if err := cur.Err(); err != nil {
		return nil, errors.Wrap(err, "cursor")
	}

	rv := make(map[NSName]chunksState, len(hashes))
	for k, v := range hashes {
		rv[selection[k]] = chunksState{
			MD5:    hex.EncodeToString(v.Sum(nil)),
			Counts: counts[k],
		}
	}

	return rv, nil
}

func getConfigState(ctx context.Context, m *mongo.Client) (map[DBName]configDBState, error) {
	ver, err := version.GetMongoVersion(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "get mongo version")
	}
	useUUID := ver.Major() >= 5 // since v5.0

	dbs, err := getConfigDatabases(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "databases")
	}

	colls, err := getConfigCollections(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "collections")
	}

	u2c := make(map[string]CollName, len(colls))
	for _, coll := range colls {
		var id string
		if useUUID {
			id = hex.EncodeToString(coll.UUID.Data)
		} else {
			id = coll.ID
		}
		u2c[id] = coll.ID
	}

	chunks, err := getConfigChunkHashes(ctx, m, u2c, useUUID)
	if err != nil {
		return nil, errors.Wrap(err, "config chunk hashes")
	}

	rv := make(map[string]configDBState, len(dbs))
	for _, spec := range dbs {
		rv[spec.ID] = configDBState{
			Spec:        spec,
			Collections: make(map[CollName]*configCollState),
		}
	}
	for _, spec := range colls {
		d, c, _ := strings.Cut(spec.ID, ".")
		rv[d].Collections[c] = &configCollState{
			Spec:   spec,
			Chunks: chunks[spec.ID],
		}
	}

	return rv, nil
}

func getShardState(ctx context.Context, m *mongo.Client) (shardState, error) {
	f := bson.D{{"name", bson.M{"$nin": bson.A{"admin", "config", "local"}}}}
	res, err := m.ListDatabases(ctx, f)
	if err != nil {
		return nil, errors.Wrap(err, "list databases")
	}

	rv := make(map[NSName]*shardCollState, len(res.Databases))
	for i := range res.Databases {
		name := res.Databases[i].Name
		db := m.Database(name)

		colls, err := db.ListCollectionSpecifications(ctx, bson.D{})
		if err != nil {
			return nil, errors.Wrapf(err, "list collections: %q", name)
		}

		res := db.RunCommand(ctx, bson.D{{"dbHash", 1}})
		if err := res.Err(); err != nil {
			return nil, errors.Wrapf(err, "dbHash: %q: query", name)
		}

		dbHash := struct{ Collections map[CollName]string }{}
		if err := res.Decode(&dbHash); err != nil {
			return nil, errors.Wrapf(err, "dbHash: %q: decode", name)
		}

		for _, coll := range colls {
			rv[name+"."+coll.Name] = &shardCollState{
				Spec: coll,
				Hash: dbHash.Collections[coll.Name],
			}
		}
	}

	return rv, nil
}
