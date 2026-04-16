package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/sdk"
)

type showConfigProfileOptions struct {
	name string
}

type addConfigProfileOptions struct {
	name     string
	file     *os.File
	sync     bool
	wait     bool
	waitTime time.Duration
}

type removeConfigProfileOptions struct {
	name     string
	wait     bool
	waitTime time.Duration
}

type syncConfigProfileOptions struct {
	name     string
	all      bool
	clear    bool
	wait     bool
	waitTime time.Duration
}

type configProfileList struct {
	Profiles []config.Config `json:"profiles"`
}

func (l configProfileList) String() string {
	if len(l.Profiles) == 0 {
		return ""
	}

	sb := strings.Builder{}
	sb.WriteString(l.Profiles[0].String())
	for _, profile := range l.Profiles[1:] {
		sb.WriteString("---\n")
		sb.WriteString(profile.String())
	}

	return sb.String()
}

func handleListConfigProfiles(ctx context.Context, pbm *sdk.Client) (fmt.Stringer, error) {
	profiles, err := pbm.GetAllConfigProfiles(ctx)
	if err != nil {
		return nil, err
	}
	if profiles == nil {
		// (for JSON) to have {"profiles":[]} instead of {"profiles":null}
		profiles = []config.Config{}
	}

	return configProfileList{profiles}, nil
}

func handleShowConfigProfiles(
	ctx context.Context,
	pbm *sdk.Client,
	opts showConfigProfileOptions,
) (fmt.Stringer, error) {
	if opts.name == "" {
		return nil, errors.New("argument `profile-name` should not be empty")
	}

	profile, err := pbm.GetConfigProfile(ctx, opts.name)
	if err != nil {
		if errors.Is(err, config.ErrMissedConfigProfile) {
			err = errors.Errorf("profile %q is not found", opts.name)
		}
		return nil, err
	}

	return profile, nil
}

func handleAddConfigProfile(
	ctx context.Context,
	pbm *sdk.Client,
	opts addConfigProfileOptions,
) (fmt.Stringer, error) {
	if opts.name == "" {
		return nil, errors.New("argument `profile-name` should not be empty")
	}
	if err := checkForAnotherOperation(ctx, pbm); err != nil {
		return nil, err
	}

	_, err := pbm.GetConfig(ctx)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("PBM is not configured")
		}
		return nil, errors.Wrap(err, "get config")
	}

	cfg, err := config.Parse(opts.file)
	if err != nil {
		return nil, errors.Wrap(err, "parse config")
	}

	_, err = pbm.GetConfigProfile(ctx, opts.name)
	if err != nil {
		if !errors.Is(err, config.ErrMissedConfigProfile) {
			return nil, errors.Wrap(err, "find saved profile")
		}
	} else {
		cid, err := pbm.RemoveConfigProfile(ctx, opts.name)
		if err != nil {
			return nil, errors.Wrap(err, "clear profile list")
		}
		err = sdk.WaitForCommandWithErrorLog(ctx, pbm, cid)
		if err != nil {
			return nil, errors.Wrap(err, "wait")
		}
	}

	cid, err := pbm.AddConfigProfile(ctx, opts.name, cfg)
	if err != nil {
		return nil, errors.Wrap(err, "add config profile")
	}
	err = sdk.WaitForCommandWithErrorLog(ctx, pbm, cid)
	if err != nil {
		return nil, errors.Wrap(err, "wait")
	}

	if opts.sync {
		cid, err := pbm.SyncFromExternalStorage(ctx, opts.name)
		if err != nil {
			return nil, errors.Wrap(err, "sync")
		}

		if opts.wait {
			if err := waitForResyncWithTimeout(ctx, pbm, cid, opts.waitTime); err != nil {
				return nil, err
			}
		}
	}

	return &outMsg{"OK"}, nil
}

func handleRemoveConfigProfile(
	ctx context.Context,
	pbm *sdk.Client,
	opts removeConfigProfileOptions,
) (fmt.Stringer, error) {
	if opts.name == "" {
		return nil, errors.New("argument `profile-name` should not be empty")
	}
	if err := checkForAnotherOperation(ctx, pbm); err != nil {
		return nil, err
	}

	_, err := pbm.GetConfigProfile(ctx, opts.name)
	if err != nil {
		if errors.Is(err, config.ErrMissedConfigProfile) {
			err = errors.Errorf("profile %q is not found", opts.name)
		}
		return nil, err
	}

	cid, err := pbm.RemoveConfigProfile(ctx, opts.name)
	if err != nil {
		return nil, errors.Wrap(err, "sdk: remove config profile")
	}

	if opts.wait {
		if opts.waitTime > time.Second {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, opts.waitTime)
			defer cancel()
		}

		err = sdk.WaitForCommandWithErrorLog(ctx, pbm, cid)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				err = errWaitTimeout
			}

			return nil, errors.Wrap(err, "wait")
		}
	}

	return &outMsg{"OK"}, nil
}

func handleSyncConfigProfile(
	ctx context.Context,
	pbm *sdk.Client,
	opts syncConfigProfileOptions,
) (fmt.Stringer, error) {
	if !opts.all && opts.name == "" {
		return nil, errors.New("<profile-name> or --all must be provided")
	}
	if opts.all && opts.name != "" {
		return nil, errors.New("ambiguous: <profile-name> and --all are provided")
	}

	if err := checkForAnotherOperation(ctx, pbm); err != nil {
		return nil, err
	}

	var err error
	var cid sdk.CommandID

	if opts.clear {
		if opts.all {
			cid, err = pbm.ClearSyncFromAllExternalStorages(ctx)
		} else {
			cid, err = pbm.ClearSyncFromExternalStorage(ctx, opts.name)
		}
	} else {
		if opts.all {
			cid, err = pbm.SyncFromAllExternalStorages(ctx)
		} else {
			cid, err = pbm.SyncFromExternalStorage(ctx, opts.name)
		}
	}
	if err != nil {
		return nil, errors.Wrap(err, "sync from storage")
	}

	if opts.wait {
		if err := waitForResyncWithTimeout(ctx, pbm, cid, opts.waitTime); err != nil {
			return nil, err
		}
	}

	return &outMsg{"OK"}, nil
}
