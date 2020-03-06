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

	"github.com/percona/percona-backup-mongodb/e2e-tests/pkg/pbm"
)

type shard struct {
	name string
	cn   *pbm.Mongo
}

type trxData struct {
	Country string
	UID     int
}

func (c *Cluster) DistributedTransactions() {
	ctx := context.Background()

	c.setupTrxCollection(ctx)

	err := c.mongos.GenData("trx", "test", 5000)
	if err != nil {
		log.Fatalln("ERROR: GenData:", err)
	}

	conn := c.mongos.Conn()
	sess, err := conn.StartSession(
		options.Session().
			SetDefaultReadPreference(readpref.Primary()).
			SetCausalConsistency(true).
			SetDefaultReadConcern(readconcern.Majority()).
			SetDefaultWriteConcern(writeconcern.New(writeconcern.WMajority())),
	)
	if err != nil {
		log.Fatalln("ERROR: start session:", err)
	}
	defer sess.EndSession(ctx)

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{
			{"moveChunk", "trx.test"},
			{"find", bson.M{"idx": 2000}},
			{"to", "rs2"},
		},
	).Err()
	if err != nil {
		log.Println("ERROR: moveChunk trx.test/idx:2000:", err)
	}

	c.printBalancerStatus(ctx)

	log.Println("Starting a backup")
	bcpDone := make(chan struct{})
	var bcpName string
	go func() {
		bcpName = c.Backup()
		c.BackupWaitDone(bcpName)
		time.Sleep(time.Second * 1)
		bcpDone <- struct{}{}
	}()

	err = mongo.WithSession(ctx, sess, func(sc mongo.SessionContext) error {
		var err error
		defer func() {
			if err != nil {
				sess.AbortTransaction(sc)
				log.Fatalln("ERROR: transaction:", err)
			}
		}()

		log.Println("Starting a transaction")
		_, err = conn.Database("trx").Collection("test").UpdateOne(sc, bson.M{"idx": 0}, bson.D{{"$set", bson.M{"changed": 1}}})
		if err != nil {
			log.Fatalln("ERROR: update in transaction trx0:", err)
		}

		log.Println("Waiting for the backup to done")
		<-bcpDone
		log.Println("Backup done")
		c.printBalancerStatus(ctx)

		_, err = conn.Database("trx").Collection("test").UpdateOne(sc, bson.M{"idx": 199}, bson.D{{"$set", bson.M{"changed": 1}}})
		if err != nil {
			log.Fatalln("ERROR: update in transaction trx:", err)
		}

		_, err = conn.Database("trx").Collection("test").UpdateOne(sc, bson.M{"idx": 2001}, bson.D{{"$set", bson.M{"changed": 1}}})
		if err != nil {
			log.Fatalln("ERROR: update in transaction trx:", err)
		}

		log.Println("Commiting the transaction")
		return sess.CommitTransaction(sc)
	})
	sess.EndSession(ctx)

	c.printBalancerStatus(ctx)

	c.checkTrxCollection(ctx, bcpName)
}

func (c *Cluster) printBalancerStatus(ctx context.Context) {
	br := c.mongos.Conn().Database("admin").RunCommand(
		ctx,
		bson.D{{"balancerStatus", 1}},
	)

	state, err := br.DecodeBytes()
	if err != nil {
		log.Fatalln("ERROR: balancerStatus:", err)
	}
	log.Println("Ballancer status:", state)
}

func (c *Cluster) deleteTrxData(ctx context.Context, tout time.Duration) bool {
	log.Println("Deleting trx.test data")
	timer := time.NewTimer(tout)
	defer timer.Stop()
	tk := time.NewTicker(time.Second * 1)
	defer tk.Stop()
	for {
		select {
		case <-timer.C:
			log.Printf("Warning: unable to drop trx.test. %v timeout exceeded", tout)
			return false
		case <-tk.C:
			err := c.mongos.Conn().Database("trx").Collection("test").Drop(ctx)
			if err != nil && !strings.Contains(err.Error(), "LockBusy") {
				log.Fatalln("ERROR: drop trx.test collections:", err)
			}
			return true
		}
	}
}

func (c *Cluster) setupTrxCollection(ctx context.Context) {
	conn := c.mongos.Conn()

	err := conn.Database("trx").Collection("test").Drop(ctx)
	if err != nil {
		log.Fatalln("ERROR: drop old trx.test collections:", err)
	}

	log.Println("Creating a sharded collection")
	err = conn.Database("trx").RunCommand(
		ctx,
		bson.D{{"create", "test"}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: create trx.test collections:", err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"enableSharding", "trx"}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: enableSharding on trx db:", err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"shardCollection", "trx.test"}, {"key", bson.M{"idx": 1}}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: shardCollection trx.test:", err)
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
		bson.D{{"addShardToZone", "rs2"}, {"zone", "R2"}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: addShardToZone rs2:", err)
	}

	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"updateZoneKeyRange", "trx.test"}, {"min", bson.M{"idx": 0}}, {"max", bson.M{"idx": 151}}, {"zone", "R1"}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: updateZoneKeyRange trx.test/R1:", err)
	}
	err = conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"updateZoneKeyRange", "trx.test"}, {"min", bson.M{"idx": 151}}, {"max", bson.M{"idx": 1000}}, {"zone", "R2"}},
	).Err()
	if err != nil {
		log.Fatalln("ERROR: updateZoneKeyRange trx.test/R2:", err)
	}

}

func (c *Cluster) checkTrxCollection(ctx context.Context, bcpName string) {
	c.DeleteBallast()
	if ok := c.deleteTrxData(ctx, time.Minute*1); !ok {
		c.zeroTrxDoc(ctx, 0)
		c.zeroTrxDoc(ctx, 199)
		c.zeroTrxDoc(ctx, 2001)
	}

	c.Restore(bcpName)

	c.checkTrxDoc(ctx, 0, 1)
	c.checkTrxDoc(ctx, 199, -1)
	c.checkTrxDoc(ctx, 2001, -1)
}

func (c *Cluster) zeroTrxDoc(ctx context.Context, id int) {
	_, err := c.mongos.Conn().Database("trx").Collection("test").UpdateOne(ctx, bson.M{"idx": id}, bson.D{{"$set", bson.M{"changed": 0}}})
	if err != nil {
		log.Fatalf("ERROR: update idx %v: %v", id, err)
	}
}

func (c *Cluster) checkTrxDoc(ctx context.Context, id, expect int) {
	r1 := pbm.TestData{}
	err := c.mongos.Conn().Database("trx").Collection("test").FindOne(ctx, bson.M{"idx": id}).Decode(&r1)
	if err != nil {
		log.Fatalf("ERROR: get trx.test record `idx %v`: %v", id, err)
	}
	if r1.C != expect {
		log.Fatalf("ERROR: wrong trx.test record `idx %v`. got: %v, expect: %v\n", id, r1.C, expect)
	}
}
