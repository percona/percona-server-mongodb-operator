package ctrl

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

func SendDeleteBackupByName(ctx context.Context, m connect.Client, name string) (OPID, error) {
	cmd := Cmd{
		Cmd: CmdDeleteBackup,
		Delete: &DeleteBackupCmd{
			Backup: name,
		},
	}
	return sendCommand(ctx, m, cmd)
}

func SendDeleteBackupBefore(
	ctx context.Context,
	m connect.Client,
	before primitive.Timestamp,
	type_ defs.BackupType,
) (OPID, error) {
	cmd := Cmd{
		Cmd: CmdDeleteBackup,
		Delete: &DeleteBackupCmd{
			OlderThan: int64(before.T),
			Type:      type_,
		},
	}
	return sendCommand(ctx, m, cmd)
}

func SendDeleteOplogRangeBefore(
	ctx context.Context,
	m connect.Client,
	before primitive.Timestamp,
) (OPID, error) {
	cmd := Cmd{
		Cmd: CmdDeletePITR,
		DeletePITR: &DeletePITRCmd{
			OlderThan: int64(before.T),
		},
	}
	return sendCommand(ctx, m, cmd)
}

func SendCleanup(
	ctx context.Context,
	m connect.Client,
	before primitive.Timestamp,
) (OPID, error) {
	cmd := Cmd{
		Cmd: CmdCleanup,
		Cleanup: &CleanupCmd{
			OlderThan: before,
		},
	}
	return sendCommand(ctx, m, cmd)
}

func SendAddConfigProfile(
	ctx context.Context,
	m connect.Client,
	name string,
	storage config.StorageConf,
) (OPID, error) {
	cmd := Cmd{
		Cmd: CmdAddConfigProfile,
		Profile: &ProfileCmd{
			Name:      name,
			IsProfile: true,
			Storage:   storage,
		},
	}
	return sendCommand(ctx, m, cmd)
}

func SendRemoveConfigProfile(ctx context.Context, m connect.Client, name string) (OPID, error) {
	cmd := Cmd{
		Cmd: CmdRemoveConfigProfile,
		Profile: &ProfileCmd{
			Name: name,
		},
	}
	return sendCommand(ctx, m, cmd)
}

func SendResync(ctx context.Context, m connect.Client) (OPID, error) {
	return sendCommand(ctx, m, Cmd{Cmd: CmdResync})
}

func SendSyncMetaFrom(ctx context.Context, m connect.Client, name string) (OPID, error) {
	opts := &ResyncCmd{}
	if name != "" {
		opts.Name = name
	} else {
		opts.All = true
	}

	cmd := Cmd{
		Cmd:    CmdResync,
		Resync: opts,
	}
	return sendCommand(ctx, m, cmd)
}

func SendClearMetaFrom(ctx context.Context, m connect.Client, name string) (OPID, error) {
	opts := &ResyncCmd{Clear: true}
	if name != "" {
		opts.Name = name
	} else {
		opts.All = true
	}

	cmd := Cmd{
		Cmd:    CmdResync,
		Resync: opts,
	}
	return sendCommand(ctx, m, cmd)
}

func SendCancelBackup(ctx context.Context, m connect.Client) (OPID, error) {
	return sendCommand(ctx, m, Cmd{Cmd: CmdCancelBackup})
}

func sendCommand(ctx context.Context, m connect.Client, cmd Cmd) (OPID, error) {
	cmd.TS = time.Now().UTC().Unix()
	res, err := m.CmdStreamCollection().InsertOne(ctx, cmd)
	if err != nil {
		return NilOPID, err
	}

	opid, ok := res.InsertedID.(primitive.ObjectID)
	if !ok {
		return NilOPID, errors.New("unexpected opid type")
	}

	return OPID(opid), nil
}
