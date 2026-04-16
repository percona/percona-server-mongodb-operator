package sharded

import (
	"context"
	"log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
)

func (c *Cluster) DistributedCommit(restoreTo primitive.Timestamp) {
	// c.PITRestoreCT(primitive.Timestamp{1644243375, 7})
	c.PITRestoreCT(restoreTo)

	exp := map[int]bool{
		0:   true,
		1:   true,
		2:   true,
		3:   true,
		4:   true,
		5:   true,
		24:  true,
		25:  true,
		34:  true,
		35:  true,
		195: true,
		196: true,
		295: true,
		296: true,
	}

	c.checkTxrExact(exp, "test")
}

func (c *Cluster) checkTxrExact(expected map[int]bool, col string) {
	ctx := context.Background()
	cur, err := c.mongos.Conn().Database(trxdb).Collection(col).Find(
		ctx,
		bson.M{},
	)
	if err != nil {
		log.Fatalf("ERROR: create cursor failed: %v", err)
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		t := struct {
			ID  interface{} `bson:"_id"`
			IDX int         `bson:"idx"`
		}{}
		err := cur.Decode(&t)
		if err != nil {
			log.Fatalf("ERROR: decode data: %v", err)
		}

		if _, ok := expected[t.IDX]; !ok {
			log.Fatalf("ERROR: unexpected counter in the db: %d. %v", t.IDX, t)
		}

		delete(expected, t.IDX)
	}

	if len(expected) > 0 {
		log.Fatalf("ERROR: missing counters: %v", expected)
	}
}
