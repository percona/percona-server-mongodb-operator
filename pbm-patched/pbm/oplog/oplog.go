package oplog

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

var errNoTransaction = errors.New("no transaction found")

func GetOplogStartTime(ctx context.Context, m *mongo.Client) (primitive.Timestamp, error) {
	ts, err := findTransactionStartTime(ctx, m)
	if errors.Is(err, errNoTransaction) {
		ts, err = findLastOplogTS(ctx, m)
	}

	return ts, err
}

func findTransactionStartTime(ctx context.Context, m *mongo.Client) (primitive.Timestamp, error) {
	coll := m.Database("config").Collection("transactions", options.Collection().SetReadConcern(readconcern.Local()))
	f := bson.D{{"state", bson.D{{"$in", bson.A{"prepared", "inProgress"}}}}}
	o := options.FindOne().SetSort(bson.D{{"startOpTime", 1}})
	doc, err := coll.FindOne(ctx, f, o).Raw()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return primitive.Timestamp{}, errNoTransaction
		}
		return primitive.Timestamp{}, errors.Wrap(err, "query transactions")
	}

	rawTS, err := doc.LookupErr("startOpTime", "ts")
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "lookup timestamp")
	}

	t, i, ok := rawTS.TimestampOK()
	if !ok {
		return primitive.Timestamp{}, errors.Wrap(err, "parse timestamp")
	}

	return primitive.Timestamp{T: t, I: i}, nil
}

func findLastOplogTS(ctx context.Context, m *mongo.Client) (primitive.Timestamp, error) {
	coll := m.Database("local").Collection("oplog.rs")
	o := options.FindOne().SetSort(bson.M{"$natural": -1})
	doc, err := coll.FindOne(ctx, bson.D{}, o).Raw()
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "query oplog")
	}

	rawTS, err := doc.LookupErr("ts")
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "lookup oplog ts")
	}

	t, i, ok := rawTS.TimestampOK()
	if !ok {
		return primitive.Timestamp{}, errors.Wrap(err, "parse oplog ts")
	}

	return primitive.Timestamp{T: t, I: i}, nil
}

// IsOplogSlicing checks if PITR slicing is running. It looks for PITR locks
// and returns true if there is at least one not stale.
func IsOplogSlicing(ctx context.Context, conn connect.Client) (bool, error) {
	locks, err := lock.GetOpLocks(ctx, conn, &lock.LockHeader{Type: ctrl.CmdPITR})
	if err != nil {
		return false, errors.Wrap(err, "get locks")
	}
	if len(locks) == 0 {
		return false, nil
	}

	ct, err := topo.GetClusterTime(ctx, conn)
	if err != nil {
		return false, errors.Wrap(err, "get cluster time")
	}

	for i := range locks {
		if locks[i].Heartbeat.T+defs.StaleFrameSec >= ct.T {
			return true, nil
		}
	}

	return false, nil
}

// FetchSlicersWithActiveLocks fetches the list of slicers (agents)
// that are holding active OpLock.
func FetchSlicersWithActiveLocks(ctx context.Context, conn connect.Client) ([]string, error) {
	res := []string{}

	locks, err := lock.GetOpLocks(ctx, conn, &lock.LockHeader{Type: ctrl.CmdPITR})
	if err != nil {
		return res, errors.Wrap(err, "get locks")
	}
	if len(locks) == 0 {
		return res, nil
	}

	ct, err := topo.GetClusterTime(ctx, conn)
	if err != nil {
		return res, errors.Wrap(err, "get cluster time")
	}

	for _, lock := range locks {
		if lock.Heartbeat.T+defs.StaleFrameSec >= ct.T {
			res = append(res, fmt.Sprintf("%s/%s", lock.Replset, lock.Node))
		}
	}

	return res, nil
}
