package mongo

//go:generate mockgen -destination=mocks/sharder.go -source=sharder.go

import (
	"context"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

type Sharder interface {
	AddShard(ctx context.Context, rsName, host string) error
	RemoveShard(ctx context.Context, shard string) (ShardRemoveResp, error)
	ListShard(ctx context.Context) (ShardList, error)
	StopBalancer(ctx context.Context) error
	StartBalancer(ctx context.Context) error
	IsBalancerRunning(ctx context.Context) (bool, error)
}

func (c *client) AddShard(ctx context.Context, rsName, host string) error {
	resp := OKResponse{}

	res := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "addShard", Value: rsName + "/" + host}})
	if res.Err() != nil {
		return errors.Wrap(res.Err(), "add shard")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decode addShard response")
	}

	if resp.OK != 1 {
		return errors.Errorf("add shard: %s", resp.Errmsg)
	}

	return nil
}

func (c *client) RemoveShard(ctx context.Context, shard string) (ShardRemoveResp, error) {
	removeResp := ShardRemoveResp{}

	resp := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "removeShard", Value: shard}})
	if resp.Err() != nil {
		return removeResp, errors.Wrap(resp.Err(), "remove shard")
	}

	if err := resp.Decode(&removeResp); err != nil {
		return removeResp, errors.Wrap(err, "failed to decode shard list")
	}

	if removeResp.OK != 1 {
		return removeResp, errors.Errorf("mongo says: %s", removeResp.Errmsg)
	}

	return removeResp, nil
}

func (c *client) ListShard(ctx context.Context) (ShardList, error) {
	shardList := ShardList{}

	resp := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "listShards", Value: 1}})
	if resp.Err() != nil {
		return shardList, errors.Wrap(resp.Err(), "listShards")
	}

	if err := resp.Decode(&shardList); err != nil {
		return shardList, errors.Wrap(err, "failed to decode shard list")
	}

	if shardList.OK != 1 {
		return shardList, errors.Errorf("mongo says: %s", shardList.Errmsg)
	}

	return shardList, nil
}

func (c *client) IsBalancerRunning(ctx context.Context) (bool, error) {
	res := BalancerStatus{}

	resp := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "balancerStatus", Value: 1}})
	if resp.Err() != nil {
		return false, errors.Wrap(resp.Err(), "balancer status")
	}

	if err := resp.Decode(&res); err != nil {
		return false, errors.Wrap(err, "failed to decode balancer status response")
	}

	if res.OK != 1 {
		return false, errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return res.Mode == "full", nil
}

func (c *client) StartBalancer(ctx context.Context) error {
	return c.switchBalancer(ctx, "balancerStart")
}

func (c *client) StopBalancer(ctx context.Context) error {
	return c.switchBalancer(ctx, "balancerStop")
}

func (c *client) switchBalancer(ctx context.Context, command string) error {
	res := OKResponse{}

	resp := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: command, Value: 1}})
	if resp.Err() != nil {
		return errors.Wrap(resp.Err(), command)
	}

	if err := resp.Decode(&res); err != nil {
		return errors.Wrapf(err, "failed to decode %s response", command)
	}

	if res.OK != 1 {
		return errors.Errorf("mongo says: %s", res.Errmsg)
	}

	return nil
}
