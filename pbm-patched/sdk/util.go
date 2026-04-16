package sdk

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/lock"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

type (
	ReplsetInfo = topo.Shard
	AgentStatus = topo.AgentStat
)

var (
	ErrMissedClusterTime       = errors.New("missed cluster time")
	ErrInvalidDeleteBackupType = backup.ErrInvalidDeleteBackupType
)

func IsHeartbeatStale(clusterTime, other Timestamp) bool {
	return clusterTime.T >= other.T+defs.StaleFrameSec
}

func ClusterTime(ctx context.Context, client *Client) (Timestamp, error) {
	info, err := topo.GetNodeInfo(ctx, client.conn.MongoClient())
	if err != nil {
		return primitive.Timestamp{}, err
	}
	if info.ClusterTime == nil {
		return primitive.Timestamp{}, ErrMissedClusterTime
	}

	return info.ClusterTime.ClusterTime, nil
}

// ClusterMembers returns list of replsets in the cluster.
//
// For sharded cluster: the configsvr (with ID `config`) and all shards.
// For non-sharded cluster: the replset.
func ClusterMembers(ctx context.Context, client *Client) ([]ReplsetInfo, error) {
	shards, err := topo.ClusterMembers(ctx, client.conn.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "topo")
	}
	return shards, nil
}

// AgentStatuses returns list of all PBM Agents statuses.
func AgentStatuses(ctx context.Context, client *Client) ([]AgentStatus, error) {
	return topo.ListAgents(ctx, client.conn)
}

func WaitForResync(ctx context.Context, c *Client, cid CommandID) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	r := &log.LogRequest{
		LogKeys: log.LogKeys{
			Event:    string(ctrl.CmdResync),
			OPID:     string(cid),
			Severity: log.Info,
		},
	}

	outC, errC := log.Follow(ctx, c.conn, r, false)

	for {
		select {
		case entry := <-outC:
			if entry == nil {
				continue
			}
			if entry.Msg == "succeed" {
				return nil
			}
			if entry.Severity == log.Error {
				return errors.New(entry.Msg)
			}
		case err := <-errC:
			return err
		}
	}
}

func FindCommandIDByName(ctx context.Context, c *Client, name string) (CommandID, error) {
	res := c.conn.CmdStreamCollection().FindOne(ctx,
		bson.D{{"$or", bson.A{
			bson.M{"backup.name": name},
			bson.M{"restore.name": name},
		}}},
		options.FindOne().SetProjection(bson.D{{"_id", 1}}))
	raw, err := res.Raw()
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return NoOpID, ErrNotFound
		}
		return NoOpID, err
	}

	return CommandID(ctrl.OPID(raw.Lookup("_id").ObjectID()).String()), nil
}

type DiagnosticReport struct {
	OPID          string              `json:"opid" bson:"opid"`
	ClusterTime   primitive.Timestamp `json:"cluster_time" bson:"cluster_time"`
	ServerVersion string              `json:"server_version" bson:"server_version"`
	FCV           string              `json:"fcv" bson:"fcv"`
	Command       *Command            `json:"command" bson:"command"`
	Members       []topo.Shard        `json:"replsets" bson:"replsets"`
	Agents        []AgentStatus       `json:"agents" bson:"agents"`
	Locks         []lock.LockData     `json:"locks,omitempty" bson:"locks,omitempty"`
	OpLocks       []lock.LockData     `json:"op_locks,omitempty" bson:"op_locks,omitempty"`
}

func Diagnostic(ctx context.Context, c *Client, cid CommandID) (*DiagnosticReport, error) {
	var err error
	rv := &DiagnosticReport{OPID: string(cid)}

	rv.ClusterTime, err = topo.GetClusterTime(ctx, c.conn)
	if err != nil {
		return nil, errors.Wrap(err, "get cluster time")
	}
	serVer, err := version.GetMongoVersion(ctx, c.conn.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "get server version")
	}
	rv.ServerVersion = serVer.String()

	rv.FCV, err = version.GetFCV(ctx, c.conn.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "get fcv")
	}

	rv.Command, err = c.CommandInfo(ctx, cid)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, errors.Wrap(err, "get command info")
	}
	rv.Members, err = topo.ClusterMembers(ctx, c.conn.MongoClient())
	if err != nil {
		return nil, errors.Wrap(err, "get members")
	}
	rv.Agents, err = topo.ListAgents(ctx, c.conn)
	if err != nil {
		return nil, errors.Wrap(err, "get agents")
	}

	rv.Locks, err = lock.GetLocks(ctx, c.conn, &lock.LockHeader{})
	if err != nil {
		return nil, errors.Wrap(err, "get locks")
	}
	rv.OpLocks, err = lock.GetOpLocks(ctx, c.conn, &lock.LockHeader{})
	if err != nil {
		return nil, errors.Wrap(err, "get op locks")
	}

	return rv, nil
}
