package tests

import (
	"context"
	"math/rand"
	"runtime"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

// GenDBSpec describes a database to create
type GenDBSpec struct {
	Name        string
	Collections []GenCollSpec
}

// GenCollSpec describes a collection to create. If ShardingKey is set,
// enables sharding for the databases and shard the collection
type GenCollSpec struct {
	Name        string
	ShardingKey *ShardingOptions
}

// ShardingOptions describes sharding key.
// Directly passed to shardCollection command
type ShardingOptions struct {
	Key map[string]any
}

// Deploy creates databases and collections.
// Do sharding for each collection with ShardingKey set.
func Deploy(ctx context.Context, m *mongo.Client, dbs []GenDBSpec) error {
	ok, err := isMongos(ctx, m)
	if err != nil {
		return errors.Wrap(err, "ismongos")
	}
	if !ok {
		return errors.New("mongos connection required")
	}

	adm := m.Database("admin")

	for _, db := range dbs {
		sharded := false

		if err := m.Database(db.Name).Drop(ctx); err != nil {
			return errors.Wrapf(err, "drop database: %q", db.Name)
		}

		for _, coll := range db.Collections {
			if coll.ShardingKey == nil || len(coll.ShardingKey.Key) == 0 {
				continue
			}

			if !sharded {
				res := adm.RunCommand(ctx, bson.D{
					{"enableSharding", db.Name},
				})
				if err := res.Err(); err != nil {
					return err
				}
				sharded = true
			}

			res := adm.RunCommand(ctx, bson.D{
				{"shardCollection", db.Name + "." + coll.Name},
				{"key", coll.ShardingKey.Key},
				{"numInitialChunks", 11},
			})
			if err := res.Err(); err != nil {
				return err
			}
		}
	}

	return nil
}

// GenerateData generates data by dbs specs
func GenerateData(ctx context.Context, m *mongo.Client, dbs []GenDBSpec) error {
	ok, err := isMongos(ctx, m)
	if err != nil {
		return errors.Wrap(err, "ismongos")
	}
	if !ok {
		return errors.New("mongos connection required")
	}

	eg, egc := errgroup.WithContext(ctx)
	eg.SetLimit(runtime.NumCPU())

	for _, d := range dbs {
		db := m.Database(d.Name)
		for _, c := range d.Collections {
			coll := db.Collection(c.Name)
			docs := make([]any, rand.Int()%400+100)

			eg.Go(func() error {
				r := rand.New(rand.NewSource(time.Now().Unix()))
				for i := range docs {
					docs[i] = bson.M{"i": i, "r": r.Int(), "t": time.Now()}
				}

				_, err := coll.InsertMany(egc, docs)
				return err
			})
		}
	}

	return eg.Wait()
}
