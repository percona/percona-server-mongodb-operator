package backup

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"

	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

type uploadChunkFunc func(ctx context.Context, w io.WriterTo, from, till primitive.Timestamp) (int64, error)

type stopSlicerFunc func() (primitive.Timestamp, int64, error)

func startOplogSlicer(
	ctx context.Context,
	m *mongo.Client,
	writeConcern *writeconcern.WriteConcern,
	interval time.Duration,
	startOpTime primitive.Timestamp,
	upload uploadChunkFunc,
) stopSlicerFunc {
	l := log.LogEventFromContext(ctx)
	var uploaded int64
	var err error

	stopSignalC := make(chan struct{})
	stoppedC := make(chan struct{})

	go func() {
		defer close(stoppedC)

		t := time.NewTicker(interval)
		defer t.Stop()

		oplog := oplog.NewOplogBackup(m)
		keepRunning := true
		for keepRunning {
			select {
			case <-t.C:
				// tick. do slicing
			case <-stopSignalC:
				// this is the last slice
				keepRunning = false
			case <-ctx.Done():
				err = ctx.Err()
				return
			}

			majority, e := topo.IsWriteMajorityRequested(ctx, m, writeConcern)
			if e != nil {
				l.Error("failed to inspect requested majority: %v", e)
			}
			currOpTime, e := topo.GetLastWrite(ctx, m, majority)
			if e != nil {
				if errors.Is(e, context.Canceled) {
					return
				}

				l.Error("failed to get last write: %v", e)
				continue
			}
			if !currOpTime.After(startOpTime) {
				continue
			}

			oplog.SetTailingSpan(startOpTime, currOpTime)

			var n int64
			n, err = upload(ctx, oplog, startOpTime, currOpTime)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}

				l.Error("failed to upload oplog: %v", err)
				continue
			}

			logm := fmt.Sprintf("created chunk %s - %s",
				time.Unix(int64(startOpTime.T), 0).UTC().Format("2006-01-02T15:04:05"),
				time.Unix(int64(currOpTime.T), 0).UTC().Format("2006-01-02T15:04:05"))
			if keepRunning {
				nextChunkT := time.Now().Add(interval)
				logm += fmt.Sprintf(". Next chunk creation scheduled to begin at ~%s", nextChunkT)
			}
			l.Info(logm)

			startOpTime = currOpTime
			uploaded += n
		}
	}()

	return func() (primitive.Timestamp, int64, error) {
		select {
		case <-stoppedC:
			// already closed
		default:
			close(stopSignalC)
			<-stoppedC
		}

		return startOpTime, uploaded, err
	}
}
