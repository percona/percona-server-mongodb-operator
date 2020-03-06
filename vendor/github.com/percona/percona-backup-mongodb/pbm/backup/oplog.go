package backup

import (
	"context"
	"io"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm"
)

// Oplog is used for reading the Mongodb oplog
type Oplog struct {
	node *pbm.Node
}

// NewOplog creates a new Oplog instance
func NewOplog(node *pbm.Node) *Oplog {
	return &Oplog{
		node: node,
	}
}

// SliceTo writes the oplog slice between given timestamps into the given w
//
// To be sure we have read ALL records up to the specified cluster time.
// Specifically, to be sure that no operations from the past gonna came after we finished the slicing,
// we have to tail until some record with ts > toTS. And it might be a noop.
func (ot *Oplog) SliceTo(ctx context.Context, w io.Writer, from, to primitive.Timestamp) error {
	clName, err := ot.collectionName()
	if err != nil {
		return errors.Wrap(err, "determine oplog collection name")
	}
	cl := ot.node.Session().Database("local").Collection(clName)

	cur, err := cl.Find(ctx,
		bson.M{
			"ts": bson.M{"$gte": from},
		},
		options.Find().SetCursorType(options.Tailable),
	)
	if err != nil {
		return errors.Wrap(err, "get the oplog cursor")
	}
	defer cur.Close(ctx)

	opts := primitive.Timestamp{}
	var ok bool
	for cur.Next(ctx) {
		opts.T, opts.I, ok = cur.Current.Lookup("ts").TimestampOK()
		if !ok {
			return errors.Errorf("get the timestamp of record %v", cur.Current)
		}
		if primitive.CompareTimestamp(to, opts) == -1 {
			return nil
		}

		// skip noop operations
		if cur.Current.Lookup("op").String() == string(pbm.OperationNoop) {
			continue
		}

		_, err = w.Write([]byte(cur.Current))
		if err != nil {
			return errors.Wrap(err, "write to pipe")
		}
	}

	return cur.Err()
}

var errMongoTimestampNil = errors.New("timestamp is nil")

// LastWrite returns a timestamp of the last write operation readable by majority reads
func (ot *Oplog) LastWrite() (primitive.Timestamp, error) {
	isMaster, err := ot.node.GetIsMaster()
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "get isMaster data")
	}
	if isMaster.LastWrite.MajorityOpTime.TS.T == 0 {
		return primitive.Timestamp{}, errMongoTimestampNil
	}
	return isMaster.LastWrite.MajorityOpTime.TS, nil
}

func (ot *Oplog) collectionName() (string, error) {
	isMaster, err := ot.node.GetIsMaster()
	if err != nil {
		return "", errors.Wrap(err, "get isMaster document")
	}

	if len(isMaster.Hosts) > 0 {
		return "oplog.rs", nil
	}
	if !isMaster.IsMaster {
		return "", errors.New("not connected to master")
	}
	return "oplog.$main", nil
}
