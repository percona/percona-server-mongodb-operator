package pbm

import (
	"context"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type Node struct {
	name string
	ctx  context.Context
	cn   *mongo.Client
	curi string
}

// ReplRole is a replicaset role in sharded cluster
type ReplRole string

const (
	ReplRoleUnknown   = "unknown"
	ReplRoleShard     = "shard"
	ReplRoleConfigSrv = "configsrv"
)

func NewNode(ctx context.Context, name string, conn *mongo.Client, curi string) *Node {
	return &Node{
		name: name,
		ctx:  ctx,
		cn:   conn,
		curi: curi,
	}
}

func (n *Node) GetIsMaster() (*IsMaster, error) {
	im := &IsMaster{}
	err := n.cn.Database(DB).RunCommand(n.ctx, bson.D{{"isMaster", 1}}).Decode(im)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command isMaster")
	}
	return im, nil
}

// IsSharded return true if node is part of the sharded cluster (in shard or configsrv replset).
func (n *Node) IsSharded() (bool, error) {
	im, err := n.GetIsMaster()
	if err != nil {
		return false, err
	}

	return im.IsSharded(), nil
}

func (n *Node) Name() (string, error) {
	im, err := n.GetIsMaster()
	if err != nil {
		return "", err
	}
	return im.Me, nil
}

type MongoVersion struct {
	VersionString string `bson:"version"`
	Version       []int  `bson:"versionArray"`
}

func (n *Node) GetMongoVersion() (*MongoVersion, error) {
	ver := new(MongoVersion)
	err := n.cn.Database(DB).RunCommand(n.ctx, bson.D{{"buildInfo", 1}}).Decode(ver)
	return ver, err
}

func (n *Node) GetReplsetStatus() (*ReplsetStatus, error) {
	status := &ReplsetStatus{}
	err := n.cn.Database(DB).RunCommand(n.ctx, bson.D{{"replSetGetStatus", 1}}).Decode(status)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command replSetGetStatus")
	}
	return status, err
}

func (n *Node) Status() (*NodeStatus, error) {
	s, err := n.GetReplsetStatus()
	if err != nil {
		return nil, errors.Wrap(err, "get replset status")
	}

	name, err := n.Name()
	if err != nil {
		return nil, errors.Wrap(err, "get node name")
	}

	for _, m := range s.Members {
		if m.Name == name {
			return &m, nil
		}
	}

	return nil, errors.New("not found")
}

// ReplicationLag returns node replication lag in seconds
func (n *Node) ReplicationLag() (int, error) {
	s, err := n.GetReplsetStatus()
	if err != nil {
		return -1, errors.Wrap(err, "get replset status")
	}

	name, err := n.Name()
	if err != nil {
		return -1, errors.Wrap(err, "get node name")
	}

	var primaryOptime, nodeOptime int
	for _, m := range s.Members {
		if m.Name == name {
			nodeOptime = int(m.Optime.TS.T)
		}
		if m.StateStr == "PRIMARY" {
			primaryOptime = int(m.Optime.TS.T)
		}
	}

	return primaryOptime - nodeOptime, nil
}

func (n *Node) ConnURI() string {
	return n.curi
}

func (n *Node) Session() *mongo.Client {
	return n.cn
}
