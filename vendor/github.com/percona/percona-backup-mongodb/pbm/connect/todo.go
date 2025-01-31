package connect

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

// nodeInfo represents the mongo's node info
type nodeInfo struct {
	Msg               string `bson:"msg"`
	Me                string `bson:"me"`
	SetName           string `bson:"setName,omitempty"`
	Primary           string `bson:"primary,omitempty"`
	IsPrimary         bool   `bson:"ismaster"`
	ConfigSvr         int    `bson:"configsvr,omitempty"`
	ConfigServerState *struct {
		OpTime *struct {
			TS   primitive.Timestamp `bson:"ts" json:"ts"`
			Term int64               `bson:"t" json:"t"`
		} `bson:"opTime"`
	} `bson:"$configServerState,omitempty"`
	Opts *mongodOpts `bson:"-"`
}

// isSharded returns true is replset is part sharded cluster
func (i *nodeInfo) isSharded() bool {
	return i.SetName != "" && (i.ConfigServerState != nil || i.Opts.Sharding.ClusterRole != "" || i.isConfigsvr())
}

// isConfigsvr returns replset role in sharded clister
func (i *nodeInfo) isConfigsvr() bool {
	return i.ConfigSvr == 2
}

// IsSharded returns true is replset is part sharded cluster
func (i *nodeInfo) isMongos() bool {
	return i.Msg == "isdbgrid"
}

// IsLeader returns true if node can act as backup leader (it's configsrv or non shareded rs)
func (i *nodeInfo) isLeader() bool {
	return !i.isSharded() || i.isConfigsvr()
}

func (i *nodeInfo) isClusterLeader() bool {
	return i.IsPrimary && i.Me == i.Primary && i.isLeader()
}

type mongodOpts struct {
	Sharding struct {
		ClusterRole string `bson:"clusterRole" json:"clusterRole" yaml:"-"`
	} `bson:"sharding" json:"sharding" yaml:"-"`
}

func getNodeInfo(ctx context.Context, m *mongo.Client) (*nodeInfo, error) {
	res := m.Database(defs.DB).RunCommand(ctx, bson.D{{"isMaster", 1}})
	if err := res.Err(); err != nil {
		return nil, errors.Wrap(err, "cmd: isMaster")
	}

	n := &nodeInfo{}
	err := res.Decode(&n)
	return n, errors.Wrap(err, "decode")
}

func getMongodOpts(ctx context.Context, m *mongo.Client, defaults *mongodOpts) (*mongodOpts, error) {
	opts := struct {
		Parsed mongodOpts `bson:"parsed" json:"parsed"`
	}{}
	if defaults != nil {
		opts.Parsed = *defaults
	}
	err := m.Database("admin").RunCommand(ctx, bson.D{{"getCmdLineOpts", 1}}).Decode(&opts)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command")
	}
	return &opts.Parsed, nil
}

func getConfigsvrURI(ctx context.Context, cn *mongo.Client) (string, error) {
	csvr := struct {
		URI string `bson:"configsvrConnectionString"`
	}{}
	err := cn.Database("admin").Collection("system.version").
		FindOne(ctx, bson.D{{"_id", "shardIdentity"}}).Decode(&csvr)

	return csvr.URI, err
}
