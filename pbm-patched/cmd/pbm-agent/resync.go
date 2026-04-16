package main

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"
	"golang.org/x/sync/errgroup"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/resync"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

// Resync uploads a backup list from the remote store
func (a *Agent) Resync(ctx context.Context, cmd *ctrl.ResyncCmd, opid ctrl.OPID, ep config.Epoch) {
	if cmd == nil {
		cmd = &ctrl.ResyncCmd{}
	}

	logger := log.FromContext(ctx)
	l := logger.NewEvent(string(ctrl.CmdResync), "", opid.String(), ep.TS())
	ctx = log.SetLogEventToContext(ctx, l)

	a.HbResume()
	logger.ResumeMgo()

	nodeInfo, err := topo.GetNodeInfoExt(ctx, a.nodeConn)
	if err != nil {
		l.Error("get node info data: %v", err)
		return
	}

	if !nodeInfo.IsLeader() {
		l.Info("not a member of the leader rs")
		return
	}

	lock := lock.NewLock(a.leadConn, lock.LockHeader{
		Type:    ctrl.CmdResync,
		Replset: nodeInfo.SetName,
		Node:    nodeInfo.Me,
		OPID:    opid.String(),
		Epoch:   util.Ref(ep.TS()),
	})

	got, err := a.acquireLock(ctx, lock, l)
	if err != nil {
		l.Error("acquiring lock: %v", err)
		return
	}
	if !got {
		l.Debug("lock not acquired")
		return
	}

	defer func() {
		if err := lock.Release(); err != nil {
			l.Error("release lock %v: %v", lock, err)
		}
	}()

	l.Info("started")

	if cmd.All {
		err = a.handleSyncAllProfiles(ctx, cmd.Clear)
	} else if cmd.Name != "" {
		err = a.handleSyncProfile(ctx, cmd.Name, cmd.Clear)
	} else {
		err = a.handleSyncMainStorage(ctx, cmd.IncludeRestores)
	}
	if err != nil {
		l.Error(err.Error())
		return
	}

	l.Info("succeed")
}

func (a *Agent) handleSyncAllProfiles(ctx context.Context, clearProfile bool) error {
	profiles, err := config.ListProfiles(ctx, a.leadConn)
	if err != nil {
		return errors.Wrap(err, "get config profiles")
	}

	eg, ctx := errgroup.WithContext(ctx)
	if clearProfile {
		for i := range profiles {
			eg.Go(func() error {
				return a.helpClearProfileBackups(ctx, profiles[i].Name)
			})
		}
	} else {
		for i := range profiles {
			eg.Go(func() error {
				return a.helpSyncProfileBackups(ctx, &profiles[i])
			})
		}
	}

	return eg.Wait()
}

func (a *Agent) handleSyncProfile(ctx context.Context, name string, clearProfile bool) error {
	profile, err := config.GetProfile(ctx, a.leadConn, name)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err = errors.Errorf("profile %q not found", name)
		}

		return errors.Wrap(err, "get config profile")
	}

	if clearProfile {
		err = a.helpClearProfileBackups(ctx, profile.Name)
	} else {
		err = a.helpSyncProfileBackups(ctx, profile)
	}

	return err
}

func (a *Agent) helpClearProfileBackups(ctx context.Context, profileName string) error {
	err := resync.ClearBackupList(ctx, a.leadConn, profileName)
	return errors.Wrapf(err, "clear backup list for %q", profileName)
}

func (a *Agent) helpSyncProfileBackups(ctx context.Context, profile *config.Config) error {
	err := resync.SyncBackupList(ctx, a.leadConn, &profile.Storage, profile.Name, a.brief.Me)
	return errors.Wrapf(err, "sync backup list for %q", profile.Name)
}

func (a *Agent) handleSyncMainStorage(ctx context.Context, includeRestores bool) error {
	cfg, err := config.GetConfig(ctx, a.leadConn)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	err = resync.Resync(ctx, a.leadConn, &cfg.Storage, a.brief.Me, includeRestores)
	if err != nil {
		return errors.Wrap(err, "resync")
	}

	epch, err := config.ResetEpoch(ctx, a.leadConn)
	if err != nil {
		return errors.Wrap(err, "reset epoch")
	}
	log.LogEventFromContext(ctx).
		Debug("epoch set to %v", epch)

	return nil
}
