package main

import (
	"context"
	"fmt"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/restore"
	"github.com/percona/percona-backup-mongodb/pbm/util"
	"github.com/percona/percona-backup-mongodb/sdk"
)

type replayOptions struct {
	start    string
	end      string
	wait     bool
	waitTime time.Duration
	rsMap    string
}

type oplogReplayResult struct {
	Name string `json:"name"`
	done bool
	err  string
}

func (r oplogReplayResult) HasError() bool {
	return r.err != ""
}

func (r oplogReplayResult) String() string {
	if r.done {
		return "\nOplog replay successfully finished!\n"
	}
	if r.err != "" {
		return "\n Error: " + r.err
	}
	return fmt.Sprintf("Oplog replay %q has started", r.Name)
}

func replayOplog(
	ctx context.Context,
	conn connect.Client,
	pbm *sdk.Client,
	o replayOptions,
	node string,
	outf outFormat,
) (fmt.Stringer, error) {
	rsMap, err := parseRSNamesMapping(o.rsMap)
	if err != nil {
		return nil, errors.Wrap(err, "cannot parse replset mapping")
	}

	startTS, err := parseTS(o.start)
	if err != nil {
		return nil, errors.Wrap(err, "parse start time")
	}
	endTS, err := parseTS(o.end)
	if err != nil {
		return nil, errors.Wrap(err, "parse end time")
	}

	if err := checkForAnotherOperation(ctx, pbm); err != nil {
		return nil, err
	}

	ep, err := config.GetEpoch(ctx, conn)
	if err != nil {
		return nil, errors.Wrap(err, "get epoch")
	}
	l := log.FromContext(ctx).NewEvent(string(ctrl.CmdRestore), "", "", ep.TS())

	stg, err := util.GetStorage(ctx, conn, node, l)
	if err != nil {
		return nil, errors.Wrap(err, "get storage")
	}

	name := time.Now().UTC().Format(time.RFC3339Nano)
	cmd := ctrl.Cmd{
		Cmd: ctrl.CmdReplay,
		Replay: &ctrl.ReplayCmd{
			Name:  name,
			Start: startTS,
			End:   endTS,
			RSMap: rsMap,
		},
	}
	if err := sendCmd(ctx, conn, cmd); err != nil {
		return nil, errors.Wrap(err, "send command")
	}

	if outf != outText {
		return oplogReplayResult{Name: name}, nil
	}

	fmt.Printf("Starting oplog replay '%s - %s'", o.start, o.end)

	startCtx, cancel := context.WithTimeout(ctx, defs.WaitActionStart)
	defer cancel()

	m, err := waitForRestoreStatus(startCtx, conn, name, restore.GetRestoreMeta)
	if err != nil {
		return nil, err
	}

	if !o.wait || m == nil {
		return oplogReplayResult{Name: name}, nil
	}

	if o.waitTime > time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, o.waitTime)
		defer cancel()
	}

	fmt.Print("Started.\nWaiting to finish")
	err = waitRestore(ctx, conn, stg, l, m, defs.StatusDone, 0)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = errWaitTimeout
		}

		return oplogReplayResult{err: err.Error()}, nil
	}

	return oplogReplayResult{Name: name, done: true}, nil
}
