package mongo

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// SetWiredTigerCacheSize resizes the WiredTiger cache of a running mongod.
func SetWiredTigerCacheSize(ctx context.Context, c Client, sizeGB float64) error {
	// WiredTiger config sizes are integers, so pass megabytes.
	cmd := bson.D{
		{Key: "setParameter", Value: 1},
		{Key: "wiredTigerEngineRuntimeConfig", Value: fmt.Sprintf("cache_size=%dM", int64(sizeGB*1024))},
	}

	res := c.Database("admin").RunCommand(ctx, cmd)
	if err := res.Err(); err != nil {
		return errors.Wrap(err, "setParameter wiredTigerEngineRuntimeConfig")
	}

	resp := OKResponse{}
	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "decode setParameter response")
	}
	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}
	return nil
}
