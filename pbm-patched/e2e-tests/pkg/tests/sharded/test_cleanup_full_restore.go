package sharded

import (
	"context"
	"fmt"
	"log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (c *Cluster) CleanupFullRestore() {
	ctx := context.Background()
	db, col := "cleanupdb", "cleanupcol"
	var numOfDocs int64 = 2000

	conn := c.mongos.Conn()
	c.stopBalancer(ctx, conn)
	c.setupShardedCollection(ctx, conn, db, col)

	err := c.mongos.GenData(db, col, 0, int64(numOfDocs))
	if err != nil {
		log.Fatalf("error: generate data: %v", err)
	}

	bcpName := c.LogicalBackup()
	c.BackupWaitDone(context.Background(), bcpName)

	c.splitChunkAt(ctx, conn, db, col, 1000)
	c.moveChunk(ctx, db, col, 500, "rsx")

	c.LogicalRestore(context.Background(), bcpName)
	c.flushRouterConfig(ctx)

	cnt, err := conn.Database(db).
		Collection(col).
		CountDocuments(ctx, bson.D{})
	if err != nil {
		log.Fatalf("ERROR: querying count: %v", err)
	} else if cnt != numOfDocs {
		log.Fatalf("ERROR: wrong number of docs: want=%d, got=%d", numOfDocs, cnt)
	}
}

func (c *Cluster) setupShardedCollection(
	ctx context.Context,
	conn *mongo.Client,
	db, col string,
) {
	err := conn.Database(db).
		Collection(col).
		Drop(ctx)
	if err != nil {
		log.Fatalf("ERROR: droping %s.%s collection: %v", db, col, err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{
			{"enableSharding", db},
			{"primaryShard", "rs1"},
		},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: enableSharding on for %s primary shard rs: %v", db, err)
	}

	ns := fmt.Sprintf("%s.%s", db, col)
	err = conn.Database(db).RunCommand(
		ctx,
		bson.D{{"create", col}},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: create %s collection: %v", ns, err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{
			{"shardCollection", ns},
			{"key", bson.M{"idx": 1}},
		},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: shard %s collection: %v", ns, err)
	}
}

func (c *Cluster) splitChunkAt(
	ctx context.Context,
	conn *mongo.Client,
	db, col string,
	id int,
) {
	ns := fmt.Sprintf("%s.%s", db, col)
	err := conn.Database("admin").RunCommand(
		ctx,
		bson.D{
			{"split", ns},
			{"find", bson.M{"idx": id}},
		},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: split %s collection: %v", ns, err)
	}
}
