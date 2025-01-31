package oplog

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type Timeline struct {
	Start uint32 `json:"start"`
	End   uint32 `json:"end"`
	Size  int64  `json:"-"`
}

func (t Timeline) String() string {
	const tlTimeFormat = "2006-01-02T15:04:05"
	ts := time.Unix(int64(t.Start), 0).UTC()
	te := time.Unix(int64(t.End), 0).UTC()
	return fmt.Sprintf("%s - %s", ts.Format(tlTimeFormat), te.Format(tlTimeFormat))
}

// OplogBackup is used for reading the Mongodb oplog
type OplogBackup struct {
	cl    *mongo.Client
	mu    sync.Mutex
	stopC chan struct{}
	start primitive.Timestamp
	end   primitive.Timestamp
}

// NewOplogBackup creates a new Oplog instance
func NewOplogBackup(m *mongo.Client) *OplogBackup {
	return &OplogBackup{cl: m}
}

// SetTailingSpan sets oplog tailing window
func (ot *OplogBackup) SetTailingSpan(start, end primitive.Timestamp) {
	ot.start = start
	ot.end = end
}

type InsuffRangeError struct {
	primitive.Timestamp
}

func (e InsuffRangeError) Error() string {
	return fmt.Sprintf(
		"oplog has insufficient range, some records since the last saved ts %v are missing. "+
			"Run `pbm backup` to create a valid starting point for the PITR",
		e.Timestamp)
}

// WriteTo writes an oplog slice between start and end timestamps into the given io.Writer
//
// To be sure we have read ALL records up to the specified cluster time.
// Specifically, to be sure that no operations from the past gonna came after we finished the slicing,
// we have to tail until some record with ts > endTS. And it might be a noop.
func (ot *OplogBackup) WriteTo(w io.Writer) (int64, error) {
	if ot.start.T == 0 || ot.end.T == 0 {
		return 0, errors.Errorf("oplog TailingSpan should be set, have start: %v, end: %v", ot.start, ot.end)
	}

	ot.mu.Lock()
	ot.stopC = make(chan struct{})
	ot.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		select {
		case <-ctx.Done():
		case <-ot.stopC:
			cancel()
		}

		ot.mu.Lock()
		ot.stopC = nil
		ot.mu.Unlock()
	}()

	cur, err := ot.cl.Database("local").Collection("oplog.rs").Find(ctx,
		bson.M{
			"ts": bson.M{"$gte": ot.start},
		},
		options.Find().SetCursorType(options.Tailable),
	)
	if err != nil {
		return 0, errors.Wrap(err, "get the oplog cursor")
	}
	defer cur.Close(ctx)

	opts := primitive.Timestamp{}
	var ok, rcheck bool
	var written int64
	for cur.Next(ctx) {
		opts.T, opts.I, ok = cur.Current.Lookup("ts").TimestampOK()
		if !ok {
			return written, errors.Errorf("get the timestamp of record %v", cur.Current)
		}
		// Before processing the first oplog record we check if oplog has sufficient range,
		// i.e. if there are no gaps between the ts of the last backup or slice and
		// the first record of the current slice. Whereas the request is ">= last_saved_ts"
		// we don't know if the returned oldest record is the first since last_saved_ts or
		// there were other records that are now removed because of oplog collection capacity.
		// So after we retrieved the first record of the current slice we check if there is
		// at least one preceding record (basically if there is still record(s) from the previous set).
		// If so, we can be sure we have a contiguous history with respect to the last_saved_slice.
		//
		// We should do this check only after we retrieved the first record of the set. Otherwise,
		// there is a possibility some records would be erased in a time span between the check and
		// the first record retrieval due to ongoing write traffic (i.e. oplog append).
		// There's a chance of false-negative though.
		if !rcheck {
			ok, err := ot.IsSufficient(ot.start)
			if err != nil {
				return 0, errors.Wrap(err, "check oplog sufficiency")
			}
			if !ok {
				return 0, InsuffRangeError{ot.start}
			}
			rcheck = true
		}

		if ot.end.Compare(opts) == -1 {
			return written, nil
		}

		// skip noop operations
		if cur.Current.Lookup("op").String() == string(defs.OperationNoop) {
			continue
		}

		n, err := w.Write(cur.Current)
		if err != nil {
			return written, errors.Wrap(err, "write to pipe")
		}
		written += int64(n)
	}

	return written, cur.Err()
}

func (ot *OplogBackup) Cancel() {
	ot.mu.Lock()
	defer ot.mu.Unlock()

	if c := ot.stopC; c != nil {
		select {
		case _, ok := <-c:
			if ok {
				close(c)
			}
		default:
		}
	}
}

// IsSufficient check is oplog is sufficient back from the given date
func (ot *OplogBackup) IsSufficient(from primitive.Timestamp) (bool, error) {
	c, err := ot.cl.Database("local").Collection("oplog.rs").
		CountDocuments(context.Background(),
			bson.M{"ts": bson.M{"$lte": from}},
			options.Count().SetLimit(1))
	if err != nil {
		return false, err
	}

	return c != 0, nil
}
