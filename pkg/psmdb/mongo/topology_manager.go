package mongo

//go:generate mockgen -destination=mocks/topology_manager.go -source=topology_manager.go

import (
	"context"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

type TopologyManager interface {
	IsMaster(ctx context.Context) (*IsMasterResp, error)
	StepDown(ctx context.Context, force bool) error
	RSStatus(ctx context.Context, initialSync bool) (ReplSetStatus, error)
	CollectionStats(ctx context.Context, collName string) (CollectionStats, error)
}

func (c *client) IsMaster(ctx context.Context) (*IsMasterResp, error) {
	cur := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "isMaster", Value: 1}})
	if cur.Err() != nil {
		return nil, errors.Wrap(cur.Err(), "run isMaster")
	}

	resp := IsMasterResp{}
	if err := cur.Decode(&resp); err != nil {
		return nil, errors.Wrap(err, "decode isMaster response")
	}

	if resp.OK != 1 {
		return nil, errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return &resp, nil
}

func (c *client) StepDown(ctx context.Context, force bool) error {
	resp := OKResponse{}

	res := c.conn.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetStepDown", Value: 60}, {Key: "force", Value: force}})
	err := res.Err()
	if err != nil {
		cErr, ok := err.(mongo.CommandError)
		if ok && cErr.HasErrorLabel("NetworkError") {
			// https://docs.mongodb.com/manual/reference/method/rs.stepDown/#client-connections
			return nil
		}
		return errors.Wrap(err, "replSetStepDown")
	}

	if err := res.Decode(&resp); err != nil {
		return errors.Wrap(err, "failed to decode response of replSetStepDown")
	}

	if resp.OK != 1 {
		return errors.Errorf("mongo says: %s", resp.Errmsg)
	}

	return nil
}

func (c *client) RSStatus(ctx context.Context, initialSync bool) (ReplSetStatus, error) {
	status := ReplSetStatus{}

	cmd := bson.D{{Key: "replSetGetStatus", Value: 1}}
	if initialSync {
		cmd = append(cmd, primitive.E{Key: "initialSync", Value: 1})
	}

	resp := c.conn.Database("admin").RunCommand(ctx, cmd)
	if resp.Err() != nil {
		return status, errors.Wrap(resp.Err(), "replSetGetStatus")
	}

	if err := resp.Decode(&status); err != nil {
		return status, errors.Wrap(err, "failed to decode rs status")
	}

	if status.OK != 1 {
		return status, errors.Errorf("mongo says: %s", status.Errmsg)
	}

	return status, nil
}

func (c *client) CollectionStats(ctx context.Context, collName string) (CollectionStats, error) {
	stats := CollectionStats{}

	res := c.conn.Database("local").RunCommand(ctx, bson.D{
		{Key: "collStats", Value: "oplog.rs"},
		{Key: "scale", Value: 1024 * 1024 * 1024}, // scale size to gigabytes
	})

	if res.Err() != nil {
		return stats, errors.Wrapf(res.Err(), "get collection stats for %s", collName)
	}
	if err := res.Decode(&stats); err != nil {
		return stats, errors.Wrapf(err, "decode collection stats for %s", collName)
	}
	if stats.Ok == 0 {
		return stats, errors.New(stats.Errmsg)
	}

	return stats, nil
}
