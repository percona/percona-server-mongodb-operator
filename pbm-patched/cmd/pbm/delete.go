package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/oplog"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/sdk"
)

type deleteBcpOpts struct {
	name      string
	olderThan string
	bcpType   string
	dryRun    bool
	yes       bool
}

func deleteBackup(
	ctx context.Context,
	conn connect.Client,
	pbm *sdk.Client,
	d *deleteBcpOpts,
) (fmt.Stringer, error) {
	if d.name == "" && d.olderThan == "" {
		return nil, errors.New("either --name or --older-than should be set")
	}
	if d.name != "" && d.olderThan != "" {
		return nil, errors.New("cannot use --name and --older-than at the same command")
	}
	if d.bcpType != "" && d.olderThan == "" {
		return nil, errors.New("cannot use --type without --older-than")
	}
	if !d.dryRun {
		err := checkForAnotherOperation(ctx, pbm)
		if err != nil {
			return nil, err
		}
	}

	var cid sdk.CommandID
	var err error
	if d.name != "" {
		cid, err = deleteBackupByName(ctx, pbm, d)
	} else {
		cid, err = deleteManyBackup(ctx, pbm, d)
	}
	if err != nil {
		if errors.Is(err, errUserCanceled) {
			return outMsg{err.Error()}, nil
		}
		return nil, err
	}

	if d.dryRun {
		return &outMsg{""}, nil
	}

	return waitForDelete(ctx, conn, pbm, cid)
}

func deleteBackupByName(ctx context.Context, pbm *sdk.Client, d *deleteBcpOpts) (sdk.CommandID, error) {
	opts := sdk.GetBackupByNameOptions{FetchIncrements: true}
	bcp, err := pbm.GetBackupByName(ctx, d.name, opts)
	if err != nil {
		if errors.Is(err, sdk.ErrNotBaseIncrement) {
			err = errors.New("Removing a single incremental backup is not allowed; " +
				"the entire chain must be removed instead.")
			return sdk.NoOpID, err
		}
		return sdk.NoOpID, errors.Wrap(err, "get backup metadata")
	}
	if bcp.Type == defs.IncrementalBackup {
		err = sdk.CanDeleteIncrementalBackup(ctx, pbm, bcp, bcp.Increments)
	} else {
		err = sdk.CanDeleteBackup(ctx, pbm, bcp)
	}
	if err != nil {
		if errors.Is(err, sdk.ErrNotBaseIncrement) || errors.Is(err, sdk.ErrIncrementalBackup) {
			err = errors.New("Removing a single incremental backup is not allowed; " +
				"the entire chain must be removed instead.")
			return sdk.NoOpID, err
		}
		return sdk.NoOpID, errors.Wrap(err, "backup cannot be deleted")
	}

	allBackups := []sdk.BackupMetadata{*bcp}
	for _, increments := range bcp.Increments {
		for _, inc := range increments {
			allBackups = append(allBackups, *inc)
		}
	}
	printDeleteInfoTo(os.Stdout, allBackups, nil)

	if d.dryRun {
		return sdk.NoOpID, nil
	}
	if !d.yes {
		err := askConfirmation("Are you sure you want to delete backup?")
		if err != nil {
			return sdk.NoOpID, err
		}
	}

	cid, err := pbm.DeleteBackupByName(ctx, d.name)
	return cid, errors.Wrap(err, "schedule delete")
}

func deleteManyBackup(ctx context.Context, pbm *sdk.Client, d *deleteBcpOpts) (sdk.CommandID, error) {
	var ts primitive.Timestamp
	ts, err := parseOlderThan(d.olderThan)
	if err != nil {
		return sdk.NoOpID, errors.Wrap(err, "parse --older-than")
	}
	if n := time.Now().UTC(); ts.T > uint32(n.Unix()) {
		providedTime := time.Unix(int64(ts.T), 0).UTC().Format(time.RFC3339)
		realTime := n.Format(time.RFC3339)
		return sdk.NoOpID, errors.Errorf("--older-than %q is after now %q", providedTime, realTime)
	}

	bcpType, err := backup.ParseDeleteBackupType(d.bcpType)
	if err != nil {
		return sdk.NoOpID, errors.Wrap(err, "parse --type")
	}
	backups, err := sdk.ListDeleteBackupBefore(ctx, pbm, ts, bcpType)
	if err != nil {
		return sdk.NoOpID, errors.Wrap(err, "fetch backup list")
	}

	printDeleteInfoTo(os.Stdout, backups, nil)

	if d.dryRun {
		return sdk.NoOpID, nil
	}
	if !d.yes {
		if err := askConfirmation("Are you sure you want to delete backups?"); err != nil {
			return sdk.NoOpID, err
		}
	}

	cid, err := pbm.DeleteBackupBefore(ctx, ts, sdk.DeleteBackupBeforeOptions{Type: bcpType})
	return cid, errors.Wrap(err, "schedule delete")
}

type deletePitrOpts struct {
	olderThan string
	yes       bool
	all       bool
	wait      bool
	waitTime  time.Duration
	dryRun    bool
}

func deletePITR(
	ctx context.Context,
	conn connect.Client,
	pbm *sdk.Client,
	d *deletePitrOpts,
) (fmt.Stringer, error) {
	if d.olderThan == "" && !d.all {
		return nil, errors.New("either --older-than or --all should be set")
	}
	if d.olderThan != "" && d.all {
		return nil, errors.New("cannot use --older-than and --all at the same command")
	}
	if !d.dryRun {
		err := checkForAnotherOperation(ctx, pbm)
		if err != nil {
			return nil, err
		}
	}

	var until primitive.Timestamp
	if d.all {
		until = primitive.Timestamp{T: uint32(time.Now().UTC().Unix())}
	} else {
		var err error
		until, err = parseOlderThan(d.olderThan)
		if err != nil {
			return nil, errors.Wrap(err, "parse --older-than")
		}

		now := time.Now().UTC()
		if until.T > uint32(now.Unix()) {
			providedTime := time.Unix(int64(until.T), 0).UTC().Format(time.RFC3339)
			realTime := now.Format(time.RFC3339)
			return nil, errors.Errorf("--older-than %q is after now %q", providedTime, realTime)
		}
	}

	chunks, err := sdk.ListDeleteChunksBefore(ctx, pbm, until)
	if err != nil {
		return nil, errors.Wrap(err, "list chunks")
	}
	if len(chunks) == 0 {
		return outMsg{"nothing to delete"}, nil
	}

	printDeleteInfoTo(os.Stdout, nil, chunks)

	if d.dryRun {
		return &outMsg{""}, nil
	}
	if !d.yes {
		q := "Are you sure you want to delete chunks?"
		if d.all {
			q = "Are you sure you want to delete ALL chunks?"
		}
		if err := askConfirmation(q); err != nil {
			if errors.Is(err, errUserCanceled) {
				return outMsg{err.Error()}, nil
			}
			return nil, err
		}
	}

	cid, err := pbm.DeleteOplogRange(ctx, until)
	if err != nil {
		return nil, errors.Wrap(err, "schedule pitr delete")
	}

	if !d.wait {
		return outMsg{"Processing by agents. Please check status later"}, nil
	}

	if d.waitTime > time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.waitTime)
		defer cancel()
	}

	rv, err := waitForDelete(ctx, conn, pbm, cid)
	if errors.Is(err, context.DeadlineExceeded) {
		err = errWaitTimeout
	}
	return rv, err
}

type cleanupOptions struct {
	olderThan string
	yes       bool
	wait      bool
	waitTime  time.Duration
	dryRun    bool
}

func doCleanup(ctx context.Context, conn connect.Client, pbm *sdk.Client, d *cleanupOptions) (fmt.Stringer, error) {
	ts, err := parseOlderThan(d.olderThan)
	if err != nil {
		return nil, errors.Wrap(err, "parse --older-than")
	}
	if n := time.Now().UTC(); ts.T > uint32(n.Unix()) {
		providedTime := time.Unix(int64(ts.T), 0).UTC().Format(time.RFC3339)
		realTime := n.Format(time.RFC3339)
		return nil, errors.Errorf("--older-than %q is after now %q", providedTime, realTime)
	}
	if !d.dryRun {
		err := checkForAnotherOperation(ctx, pbm)
		if err != nil {
			return nil, err
		}
	}

	info, err := pbm.CleanupReport(ctx, ts)
	if err != nil {
		return nil, errors.Wrap(err, "make cleanup report")
	}
	if len(info.Backups) == 0 && len(info.Chunks) == 0 {
		return outMsg{"nothing to delete"}, nil
	}

	printDeleteInfoTo(os.Stdout, info.Backups, info.Chunks)

	if d.dryRun {
		return &outMsg{""}, nil
	}
	if !d.yes {
		if err := askConfirmation("Are you sure you want to delete?"); err != nil {
			if errors.Is(err, errUserCanceled) {
				return outMsg{err.Error()}, nil
			}
			return nil, err
		}
	}

	cid, err := pbm.RunCleanup(ctx, ts)
	if err != nil {
		return nil, errors.Wrap(err, "send command")
	}

	if !d.wait {
		return outMsg{"Processing by agents. Please check status later"}, nil
	}

	if d.waitTime > time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, d.waitTime)
		defer cancel()
	}

	rv, err := waitForDelete(ctx, conn, pbm, cid)
	if errors.Is(err, context.DeadlineExceeded) {
		err = errWaitTimeout
	}
	return rv, err
}

func parseOlderThan(s string) (primitive.Timestamp, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return primitive.Timestamp{}, errInvalidFormat
	}

	ts, err := parseTS(s)
	if !errors.Is(err, errInvalidFormat) {
		return ts, err
	}

	dur, err := parseDuration(s)
	if err != nil {
		if errors.Is(err, errInvalidDuration) {
			err = errInvalidFormat
		}
		return primitive.Timestamp{}, err
	}

	unix := time.Now().UTC().Add(-dur).Unix()
	return primitive.Timestamp{T: uint32(unix), I: 0}, nil
}

var errInvalidDuration = errors.New("invalid duration")

func parseDuration(s string) (time.Duration, error) {
	d, c := int64(0), ""
	_, err := fmt.Sscanf(s, "%d%s", &d, &c)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return 0, errInvalidDuration
		}
		return 0, err
	}
	if c != "d" {
		return 0, errInvalidDuration
	}

	return time.Duration(d * 24 * int64(time.Hour)), nil
}

func printDeleteInfoTo(w io.Writer, backups []backup.BackupMeta, chunks []oplog.OplogChunk) {
	if len(backups) != 0 {
		fmt.Fprintln(w, "Snapshots:")
		for i := range backups {
			bcp := &backups[i]

			t := string(bcp.Type)
			if bcp.Type == sdk.LogicalBackup && len(bcp.Namespaces) != 0 {
				t += ", selective"
			} else if bcp.Type == defs.IncrementalBackup && bcp.SrcBackup == "" {
				t += ", base"
			}

			restoreTime := time.Unix(int64(bcp.LastWriteTS.T), 0).UTC().Format(time.RFC3339)
			fmt.Fprintf(w, " - %q [size: %s type: <%s>, restore time: %s]\n",
				bcp.Name, storage.PrettySize(bcp.Size), t, restoreTime)
		}
	}

	if len(chunks) == 0 {
		return
	}

	type oplogRange struct {
		Start, End primitive.Timestamp
	}

	oplogRanges := make(map[string][]oplogRange)
	for _, c := range chunks {
		rs := oplogRanges[c.RS]
		if rs == nil {
			oplogRanges[c.RS] = []oplogRange{{c.StartTS, c.EndTS}}
			continue
		}

		lastWrite := &rs[len(rs)-1].End
		if lastWrite.Compare(c.StartTS) == -1 {
			oplogRanges[c.RS] = append(rs, oplogRange{c.StartTS, c.EndTS})
			continue
		}
		if lastWrite.Compare(c.EndTS) == -1 {
			*lastWrite = c.EndTS
		}
	}

	fmt.Fprintln(w, "PITR chunks (by replset name):")
	for rs, ops := range oplogRanges {
		fmt.Fprintf(w, " %s:\n", rs)

		for _, r := range ops {
			fmt.Fprintf(w, " - %s - %s\n", fmtTS(int64(r.Start.T)), fmtTS(int64(r.End.T)))
		}
	}
}

var errUserCanceled = errors.New("canceled")

func askConfirmation(question string) error {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return errors.Wrap(err, "stat stdin")
	}
	if (fi.Mode() & os.ModeCharDevice) == 0 {
		return errors.New("no tty")
	}

	fmt.Printf("%s [y/N] ", question)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	if err := scanner.Err(); err != nil {
		return errors.Wrap(err, "read stdin")
	}

	switch strings.TrimSpace(scanner.Text()) {
	case "yes", "Yes", "YES", "Y", "y":
		return nil
	}

	return errUserCanceled
}

func waitForDelete(
	ctx context.Context,
	conn connect.Client,
	pbm *sdk.Client,
	cid sdk.CommandID,
) (fmt.Stringer, error) {
	commandCtx, stopProgress := context.WithCancel(ctx)
	defer stopProgress()

	go func() {
		fmt.Print("Waiting for delete to be done ")

		for tick := time.NewTicker(time.Second); ; {
			select {
			case <-tick.C:
				fmt.Print(".")
			case <-commandCtx.Done():
				return
			}
		}
	}()

	cmd, err := pbm.CommandInfo(commandCtx, cid)
	if err != nil {
		return nil, errors.Wrap(err, "get command info")
	}

	var waitFn func(ctx context.Context, client *sdk.Client) error
	switch cmd.Cmd {
	case ctrl.CmdCleanup:
		waitFn = sdk.WaitForCleanup
	case ctrl.CmdDeleteBackup:
		waitFn = sdk.WaitForDeleteBackup
	case ctrl.CmdDeletePITR:
		waitFn = sdk.WaitForDeleteOplogRange
	default:
		return nil, errors.New("wrong command")
	}

	err = waitFn(commandCtx, pbm)
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}

		msg, err := sdk.WaitForErrorLog(ctx, pbm, cmd)
		if err != nil {
			return nil, errors.Wrap(err, "read agents log")
		}
		if msg != "" {
			return nil, errors.New(msg)
		}

		return outMsg{"Operation is still in progress, please check status in a while"}, nil
	}

	stopProgress()
	fmt.Println("[done]")
	return runList(ctx, conn, pbm, &listOpts{})
}
