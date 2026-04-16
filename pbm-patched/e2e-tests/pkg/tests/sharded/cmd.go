package sharded

import (
	"context"
	"log"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

func (c *Cluster) stopBalancer(ctx context.Context, conn *mongo.Client) {
	err := conn.Database("admin").RunCommand(
		ctx,
		bson.D{{"balancerStop", 1}},
	).Err()
	if err != nil {
		log.Fatalf("ERROR: stopping balancer: %v", err)
	}
}

func (c *Cluster) moveChunk(ctx context.Context, db, col string, idx int, to string) {
	log.Println("move chunk", idx, "to", to)
	err := c.mongos.Conn().Database("admin").RunCommand(
		ctx,
		bson.D{
			{"moveChunk", db + "." + col},
			{"find", bson.M{"idx": idx}},
			{"to", to},
		},
	).Err()
	if err != nil {
		log.Printf("ERROR: moveChunk %s.%s/idx:2000: %v", db, col, err)
	}
}
