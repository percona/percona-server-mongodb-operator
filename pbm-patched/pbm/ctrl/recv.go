package ctrl

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type CursorClosedError struct {
	Err error
}

func (c CursorClosedError) Error() string {
	return "cursor was closed with:" + c.Err.Error()
}

func (c CursorClosedError) Is(err error) bool {
	if err == nil {
		return false
	}

	_, ok := err.(CursorClosedError) //nolint:errorlint
	return ok
}

func (c CursorClosedError) Unwrap() error {
	return c.Err
}

func ListenCmd(ctx context.Context, m connect.Client, cl <-chan struct{}) (<-chan Cmd, <-chan error) {
	cmd := make(chan Cmd)
	errc := make(chan error)

	go func() {
		defer close(cmd)
		defer close(errc)

		ts := time.Now().UTC().Unix()
		var lastTS int64
		cmdBuckets := make(map[int64][]Cmd)
		for {
			select {
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			case <-cl:
				return
			default:
			}
			cur, err := m.CmdStreamCollection().Find(
				ctx,
				bson.M{"ts": bson.M{"$gte": ts}},
			)
			if err != nil {
				errc <- errors.Wrap(err, "watch the cmd stream")
				continue
			}

			for cur.Next(ctx) {
				c := Cmd{}
				err := cur.Decode(&c)
				if err != nil {
					errc <- errors.Wrap(err, "message decode")
					continue
				}

				if checkDuplicateCmd(cmdBuckets, c) {
					continue
				}

				opid, ok := cur.Current.Lookup("_id").ObjectIDOK()
				if !ok {
					errc <- errors.New("unable to get operation ID")
					continue
				}

				c.OPID = OPID(opid)

				cmdBuckets[c.TS] = append(cmdBuckets[c.TS], c)
				lastTS = c.TS
				cmd <- c
				ts = time.Now().UTC().Unix()
			}
			if err := cur.Err(); err != nil {
				errc <- CursorClosedError{err}
				cur.Close(ctx)
				return
			}

			cleanupOldCmdBuckets(cmdBuckets, lastTS)

			cur.Close(ctx)
			time.Sleep(time.Second * 1)
		}
	}()

	return cmd, errc
}

// checkDuplicateCmd returns true if the command already exists in the bucket for its timestamp.
func checkDuplicateCmd(cmdBuckets map[int64][]Cmd, c Cmd) bool {
	cmds, ok := cmdBuckets[c.TS]

	if !ok {
		return false
	}

	for _, cmd := range cmds {
		if cmd.Cmd == c.Cmd && cmd.TS == c.TS {
			return true
		}
	}

	return false
}

// cleanupOldCmdBuckets deletes buckets older than the lastTS.
func cleanupOldCmdBuckets(cmdBuckets map[int64][]Cmd, lastTS int64) {
	for ts := range cmdBuckets {
		if ts < lastTS {
			delete(cmdBuckets, ts)
		}
	}
}
