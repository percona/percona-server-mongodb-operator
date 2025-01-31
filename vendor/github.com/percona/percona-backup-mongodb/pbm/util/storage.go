package util

import (
	"bytes"
	"context"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/blackhole"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

// ErrStorageUndefined is an error for undefined storage
var ErrStorageUndefined = errors.New("storage undefined")

// StorageFromConfig creates and returns a storage object based on a given config and node name.
// Node name is used for fetching endpoint url from config for specific cluster member (node).
func StorageFromConfig(cfg *config.StorageConf, node string, l log.LogEvent) (storage.Storage, error) {
	switch cfg.Type {
	case storage.S3:
		return s3.New(cfg.S3, node, l)
	case storage.Azure:
		return azure.New(cfg.Azure, node, l)
	case storage.Filesystem:
		return fs.New(cfg.Filesystem)
	case storage.Blackhole:
		return blackhole.New(), nil
	case storage.Undefined:
		return nil, ErrStorageUndefined
	default:
		return nil, errors.Errorf("unknown storage type %s", cfg.Type)
	}
}

// GetStorage reads current storage config and creates and
// returns respective storage.Storage object.
func GetStorage(ctx context.Context, m connect.Client, node string, l log.LogEvent) (storage.Storage, error) {
	c, err := config.GetConfig(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "get config")
	}

	return StorageFromConfig(&c.Storage, node, l)
}

// Initialize write current PBM version to PBM init file.
//
// It does not handle "file already exists" error.
func Initialize(ctx context.Context, stg storage.Storage) error {
	err := RetryableWrite(stg, defs.StorInitFile, []byte(version.Current().Version))
	if err != nil {
		return errors.Wrap(err, "write init file")
	}

	return nil
}

// Reinitialize delete existing PBM init file and create new once with current PBM version.
//
// It expects that the file exists.
func Reinitialize(ctx context.Context, stg storage.Storage) error {
	err := stg.Delete(defs.StorInitFile)
	if err != nil {
		return errors.Wrap(err, "delete init file")
	}

	return Initialize(ctx, stg)
}

func RetryableWrite(stg storage.Storage, name string, data []byte) error {
	err := stg.Save(name, bytes.NewBuffer(data), int64(len(data)))
	if err != nil && stg.Type() == storage.Filesystem {
		if fs.IsRetryableError(err) {
			err = stg.Save(name, bytes.NewBuffer(data), int64(len(data)))
		}
	}

	return err
}
