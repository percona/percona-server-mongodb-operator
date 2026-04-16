package config

import (
	"context"

	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

func GetProfiledConfig(ctx context.Context, conn connect.Client, profile string) (*Config, error) {
	cfg, err := GetConfig(ctx, conn)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err = ErrMissedConfig
		}
		return nil, errors.Wrap(err, "get main config")
	}

	if profile != "" {
		custom, err := GetProfile(ctx, conn, profile)
		if err != nil {
			if errors.Is(err, mongo.ErrNoDocuments) {
				err = ErrMissedConfigProfile
			}
			return nil, errors.Wrap(err, "get config profile")
		}
		if err := custom.Storage.Cast(); err != nil {
			return nil, errors.Wrap(err, "storage cast")
		}

		// use storage config only
		cfg.Storage = custom.Storage
		cfg.Name = custom.Name
		cfg.IsProfile = true
	}

	if storage.ParseType(string(cfg.Storage.Type)) == storage.Undefined {
		return nil, errors.New(
			"backups cannot be saved because PBM storage configuration hasn't been set yet")
	}

	return cfg, nil
}
