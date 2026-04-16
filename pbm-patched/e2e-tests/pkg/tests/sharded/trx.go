package sharded

import (
	"context"
	"log"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"

	pbmt "github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
)

const trxdb = "trx"

func (c *Cluster) DistributedTransactions(bcp Backuper, col string) {
	const trxLimitT = 300

	dbcol := trxdb + "." + col

	ctx := context.Background()
	conn := c.mongos.Conn()

	log.Println("Updating transactionLifetimeLimitSeconds to", trxLimitT)
	err := c.mongopbm.Conn().AdminCommand(
		ctx,
		bson.D{{"setParameter", 1}, {"transactionLifetimeLimitSeconds", trxLimitT}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: update transactionLifetimeLimitSeconds:", err)
	}
	for sname, cn := range c.shards {
		log.Printf("Updating transactionLifetimeLimitSeconds for %s to %d", sname, trxLimitT)
		err := cn.Conn().Database("admin").RunCommand(
			ctx,
			bson.D{{"setParameter", 1}, {"transactionLifetimeLimitSeconds", trxLimitT}},
		).Err()
		if err != nil {
			log.Fatalf("ERROR: update transactionLifetimeLimitSeconds for shard %s: %v", sname, err)
		}
	}

	c.setupTrxCollection(ctx, col)

	c.moveChunk(ctx, trxdb, col, 0, "rs1")
	c.moveChunk(ctx, trxdb, col, 30, "rs1")
	c.moveChunk(ctx, trxdb, col, 89, "rs1")
	c.moveChunk(ctx, trxdb, col, 99, "rs1")
	c.moveChunk(ctx, trxdb, col, 110, "rs1")
	c.moveChunk(ctx, trxdb, col, 130, "rs1")
	c.moveChunk(ctx, trxdb, col, 131, "rs1")
	c.moveChunk(ctx, trxdb, col, 630, "rsx")
	c.moveChunk(ctx, trxdb, col, 530, "rsx")
	c.moveChunk(ctx, trxdb, col, 631, "rsx")
	c.moveChunk(ctx, trxdb, col, 730, "rsx")
	c.moveChunk(ctx, trxdb, col, 3000, "rsx")
	c.moveChunk(ctx, trxdb, col, 3001, "rsx")
	c.moveChunk(ctx, trxdb, col, 180, "rsx")
	c.moveChunk(ctx, trxdb, col, 199, "rsx")
	c.moveChunk(ctx, trxdb, col, 2001, "rsx")

	_, err = conn.Database(trxdb).Collection(col).DeleteMany(ctx, bson.M{})
	if err != nil {
		log.Fatalf("ERROR: delete data from %s: %v", dbcol, err)
	}

	err = c.mongos.GenData(trxdb, col, 0, 5000)
	if err != nil {
		log.Fatalln("ERROR: GenData:", err)
	}

	sess, err := conn.StartSession(
		options.Session().
			SetDefaultReadPreference(readpref.Primary()).
			SetCausalConsistency(true).
			SetDefaultReadConcern(readconcern.Majority()).
			SetDefaultWriteConcern(writeconcern.Majority()))
	if err != nil {
		log.Fatalln("ERROR: start session:", err)
	}
	defer sess.EndSession(ctx)

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{
			{"moveChunk", dbcol},
			{"find", bson.M{"idx": 2000}},
			{"to", "rsx"},
		},
	).Err()
	if err != nil {
		log.Printf("ERROR: moveChunk %s/idx:2000: %v", dbcol, err)
	}

	c.printBalancerStatus(ctx)

	log.Println("Starting a backup")
	go bcp.Backup()

	// distributed transaction that commits before the backup ends
	// should be visible after restore
	log.Println("Run trx1")
	_, _ = sess.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		c.trxSet(sc, 30, col)
		c.trxSet(sc, 530, col)

		bcp.WaitStarted()

		c.trxSet(sc, 130, col)
		c.trxSet(sc, 131, col)
		c.trxSet(sc, 630, col)
		c.trxSet(sc, 631, col)

		bcp.WaitSnapshot()

		c.trxSet(sc, 110, col)
		c.trxSet(sc, 730, col)
		c.trxSet(sc, 3000, col)
		c.trxSet(sc, 3001, col)

		return nil, nil //nolint:nilnil
	})

	log.Println("Run trx2")
	// distributed transaction that commits after the backup ends
	// should NOT be visible after the restore
	_ = mongo.WithSession(ctx, sess, func(sc mongo.SessionContext) error {
		err := sess.StartTransaction()
		if err != nil {
			log.Fatalln("ERROR: start transaction:", err)
		}
		defer func() {
			if err != nil {
				_ = sess.AbortTransaction(sc)
				log.Fatalln("ERROR: transaction:", err)
			}
		}()

		c.trxSet(sc, 0, col)
		c.trxSet(sc, 89, col)
		c.trxSet(sc, 180, col)

		c.printBalancerStatus(ctx)

		log.Println("Waiting for the backup to done")
		bcp.WaitDone()
		log.Println("Backup done")

		c.printBalancerStatus(ctx)

		c.trxSet(sc, 99, col)
		c.trxSet(sc, 199, col)
		c.trxSet(sc, 2001, col)

		log.Println("Committing the transaction")
		err = sess.CommitTransaction(sc)
		if err != nil {
			log.Fatalln("ERROR: commit in transaction:", err)
		}

		return nil
	})
	sess.EndSession(ctx)

	c.printBalancerStatus(ctx)

	c.checkTrxCollection(ctx, col, bcp)
}

func (c *Cluster) trxSet(ctx mongo.SessionContext, id int, col string) {
	log.Print("\ttrx", id)
	err := c.updateTrxRetry(ctx, col, bson.M{"idx": id}, bson.D{{"$set", bson.M{"changed": 1}}})
	if err != nil {
		log.Fatalf("ERROR: update in transaction trx%d: %v", id, err)
	}
}

// updateTrxRetry tries to run an update operation and in case of the StaleConfig error
// it run flushRouterConfig and tries again
func (c *Cluster) updateTrxRetry(ctx mongo.SessionContext, col string, filter, update interface{}) error {
	var err error
	conn := c.mongos.Conn()
	for i := 0; i < 3; i++ {
		c.flushRouterConfig(context.Background())
		_, err = conn.Database(trxdb).Collection(col).UpdateOne(ctx, filter, update)
		if err == nil {
			return nil
		}
		if !strings.Contains(err.Error(), "StaleConfig") {
			return err
		}
	}
	return err
}

func (c *Cluster) printBalancerStatus(ctx context.Context) {
	br := c.mongos.Conn().Database("admin").RunCommand(
		ctx,
		bson.D{{"balancerStatus", 1}},
	)

	state, err := br.Raw()
	if err != nil {
		log.Fatalln("ERROR: balancerStatus:", err)
	}
	log.Println("Ballancer status:", state)
}

func (c *Cluster) flushRouterConfig(ctx context.Context) {
	log.Println("flushRouterConfig")
	err := c.mongos.Conn().Database("admin").RunCommand(
		ctx,
		bson.D{{"flushRouterConfig", 1}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: flushRouterConfig:", err)
	}
}

func (c *Cluster) deleteTrxData(ctx context.Context, col string, tout time.Duration) bool {
	log.Printf("Deleting %s.%s data", trxdb, col)
	timer := time.NewTimer(tout)
	defer timer.Stop()
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()
	for {
		select {
		case <-timer.C:
			log.Printf("Warning: unable to drop %s.%s. %v timeout exceeded", trxdb, col, tout)
			return false
		case <-tk.C:
			err := c.mongos.Conn().Database(trxdb).Collection(col).Drop(ctx)
			if err == nil {
				return true
			}
			if err != nil && !strings.Contains(err.Error(), "LockBusy") {
				log.Fatalf("ERROR: drop %s.%s collections: %v", trxdb, col, err)
			}
		}
	}
}

func (c *Cluster) setupTrxCollection(ctx context.Context, col string) {
	conn := c.mongos.Conn()

	if ok := c.deleteTrxData(ctx, col, time.Minute*5); !ok {
		_, err := conn.Database(trxdb).Collection(col).DeleteMany(ctx, bson.D{})
		if err != nil {
			log.Fatalf("ERROR: delete data from the old %s.%s collection: %v", trxdb, col, err)
		}
	}

	log.Println("Creating a sharded collection")
	err := conn.Database(trxdb).RunCommand(
		ctx,
		bson.D{{"create", col}},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: create %s.%s collections: %v", trxdb, col, err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"enableSharding", trxdb}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: enableSharding on trx db:", err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"shardCollection", trxdb + "." + col}, {"key", bson.M{"idx": 1}}},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: shardCollection %s.%s: %v", trxdb, col, err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"addShardToZone", "rs1"}, {"zone", "R1"}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: addShardToZone rs1:", err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"addShardToZone", "rsx"}, {"zone", "R2"}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: addShardToZone rsx:", err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{
			{"updateZoneKeyRange", trxdb + "." + col},
			{"min", bson.M{"idx": 0}},
			{"max", bson.M{"idx": 151}},
			{"zone", "R1"},
		},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: updateZoneKeyRange %s.%s./R1: %v", trxdb, col, err)
	}
	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{
			{"updateZoneKeyRange", trxdb + "." + col},
			{"min", bson.M{"idx": 151}},
			{"max", bson.M{"idx": 1000}},
			{"zone", "R2"},
		},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: updateZoneKeyRange %s.%s./R2: %v", trxdb, col, err)
	}
}

func (c *Cluster) checkTrxCollection(ctx context.Context, col string, bcp Backuper) {
	log.Println("Checking restored data")

	c.DeleteBallast()
	if ok := c.deleteTrxData(ctx, col, time.Minute*1); !ok {
		c.zeroTrxDoc(ctx, col, 30)
		c.zeroTrxDoc(ctx, col, 530)
		c.zeroTrxDoc(ctx, col, 130)
		c.zeroTrxDoc(ctx, col, 131)
		c.zeroTrxDoc(ctx, col, 630)
		c.zeroTrxDoc(ctx, col, 631)
		c.zeroTrxDoc(ctx, col, 110)
		c.zeroTrxDoc(ctx, col, 730)
		c.zeroTrxDoc(ctx, col, 3000)
		c.zeroTrxDoc(ctx, col, 3001)
		c.zeroTrxDoc(ctx, col, 0)
		c.zeroTrxDoc(ctx, col, 89)
		c.zeroTrxDoc(ctx, col, 99)
		c.zeroTrxDoc(ctx, col, 180)
		c.zeroTrxDoc(ctx, col, 199)
		c.zeroTrxDoc(ctx, col, 2001)
	}
	c.flushRouterConfig(ctx)

	time.Sleep(time.Second * 60)
	bcp.Restore()

	log.Println("check committed transaction")
	c.checkTrxDoc(ctx, col, 30, 1)
	c.checkTrxDoc(ctx, col, 530, 1)
	c.checkTrxDoc(ctx, col, 130, 1)
	c.checkTrxDoc(ctx, col, 131, 1)
	c.checkTrxDoc(ctx, col, 630, 1)
	c.checkTrxDoc(ctx, col, 631, 1)
	c.checkTrxDoc(ctx, col, 110, 1)
	c.checkTrxDoc(ctx, col, 730, 1)
	c.checkTrxDoc(ctx, col, 3000, 1)
	c.checkTrxDoc(ctx, col, 3001, 1)

	log.Println("check uncommitted (commit wasn't dropped to backup) transaction")
	c.checkTrxDoc(ctx, col, 0, -1)
	c.checkTrxDoc(ctx, col, 89, -1)
	c.checkTrxDoc(ctx, col, 99, -1)
	c.checkTrxDoc(ctx, col, 180, -1)
	c.checkTrxDoc(ctx, col, 199, -1)
	c.checkTrxDoc(ctx, col, 2001, -1)

	log.Println("check data that wasn't touched by transactions")
	c.checkTrxDoc(ctx, col, 10, -1)
	c.checkTrxDoc(ctx, col, 2000, -1)
}

func (c *Cluster) zeroTrxDoc(ctx context.Context, col string, id int) {
	_, err := c.mongos.Conn().Database(trxdb).Collection(col).
		UpdateOne(ctx, bson.M{"idx": id}, bson.D{{"$set", bson.M{"changed": 0}}})
	if err != nil {
		log.Fatalf("ERROR: update idx %v: %v", id, err)
	}
}

func (c *Cluster) checkTrxDoc(ctx context.Context, col string, id, expect int) {
	log.Println("\tcheck", id, expect)
	r1 := pbmt.TestData{}
	err := c.mongos.Conn().Database(trxdb).Collection(col).
		FindOne(ctx, bson.M{"idx": id}).
		Decode(&r1)
	if err != nil {
		log.Fatalf("ERROR: get %s.%s record `idx %v`: %v", trxdb, col, id, err)
	}
	if r1.C != expect {
		log.Fatalf("ERROR: wrong %s.%s record `idx %v`. got: %v, expect: %v\n", trxdb, col, id, r1.C, expect)
	}
}
