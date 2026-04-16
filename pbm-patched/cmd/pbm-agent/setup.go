package main

import (
	"context"
	"strings"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

const (
	cmdCollectionSizeBytes      = 1 << 20  // 1Mb
	pbmOplogCollectionSizeBytes = 10 << 20 // 10Mb
	logsCollectionSizeBytes     = 50 << 20 // 50Mb
)

// setup a new DB for PBM
func setupNewDB(ctx context.Context, conn connect.Client) error {
	err := conn.AdminCommand(
		ctx,
		bson.D{{"create", defs.CmdStreamCollection}, {"capped", true}, {"size", cmdCollectionSizeBytes}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure cmd collection")
	}

	err = conn.AdminCommand(
		ctx,
		bson.D{{"create", defs.LogCollection}, {"capped", true}, {"size", logsCollectionSizeBytes}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure log collection")
	}

	err = conn.AdminCommand(
		ctx,
		bson.D{{"create", defs.LockCollection}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure lock collection")
	}

	// create indexes for the lock collections
	_, err = conn.LockCollection().Indexes().CreateOne(
		ctx,
		mongo.IndexModel{
			Keys: bson.D{{"replset", 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true),
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrapf(err, "ensure lock index on %s", defs.LockCollection)
	}
	_, err = conn.LockOpCollection().Indexes().CreateOne(
		ctx,
		mongo.IndexModel{
			Keys: bson.D{{"replset", 1}, {"type", 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true),
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrapf(err, "ensure lock index on %s", defs.LockOpCollection)
	}

	err = conn.AdminCommand(
		ctx,
		bson.D{{"create", defs.PBMOpLogCollection}, {"capped", true}, {"size", pbmOplogCollectionSizeBytes}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure log collection")
	}
	_, err = conn.PBMOpLogCollection().Indexes().CreateOne(
		ctx,
		mongo.IndexModel{
			Keys: bson.D{{"opid", 1}, {"replset", 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true),
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrapf(err, "ensure lock index on %s", defs.LockOpCollection)
	}

	// create indexs for the pitr chunks
	_, err = conn.PITRChunksCollection().Indexes().CreateMany(
		ctx,
		[]mongo.IndexModel{
			{
				Keys: bson.D{{"rs", 1}, {"start_ts", 1}, {"end_ts", 1}},
				Options: options.Index().
					SetUnique(true).
					SetSparse(true),
			},
			{
				Keys: bson.D{{"start_ts", 1}, {"end_ts", 1}},
			},
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure pitr chunks index")
	}

	err = conn.AdminCommand(
		ctx,
		bson.D{{"create", defs.PITRCollection}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure pitr collection")
	}

	_, err = conn.BcpCollection().Indexes().CreateMany(
		ctx,
		[]mongo.IndexModel{
			{
				Keys: bson.D{{"name", 1}},
				Options: options.Index().
					SetUnique(true).
					SetSparse(true),
			},
			{
				Keys: bson.D{{"start_ts", 1}, {"status", 1}},
			},
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure backup collection index")
	}

	err = conn.AdminCommand(
		ctx,
		bson.D{{"create", defs.RestoresCollection}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure restore collection")
	}

	err = conn.AdminCommand(
		ctx,
		bson.D{{"create", defs.AgentsStatusCollection}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure agent status collection")
	}

	return nil
}
