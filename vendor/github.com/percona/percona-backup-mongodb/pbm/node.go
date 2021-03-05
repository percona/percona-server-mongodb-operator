package pbm

import (
	"context"
	"fmt"
	"strings"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

type Node struct {
	rs        string
	me        string
	ctx       context.Context
	cn        *mongo.Client
	curi      string
	dumpConns int
}

// ReplRole is a replicaset role in sharded cluster
type ReplRole string

const (
	ReplRoleUnknown   = "unknown"
	ReplRoleShard     = "shard"
	ReplRoleConfigSrv = "configsrv"

	// TmpUsersCollection and TmpRoles are tmp collections used to avoid
	// user related issues while resoring on new cluster.
	// See https://jira.percona.com/browse/PBM-425
	//
	// Backup should ensure abscense of this collection to avoid
	// restore conflicts. See https://jira.percona.com/browse/PBM-460
	TmpUsersCollection = `pbmRUsers`
	TmpRolesCollection = `pbmRRoles`
)

func NewNode(ctx context.Context, curi string, dumpConns int) (*Node, error) {
	n := &Node{
		ctx:       ctx,
		curi:      curi,
		dumpConns: dumpConns,
	}
	err := n.Connect()
	if err != nil {
		return nil, errors.Wrap(err, "connect")
	}

	nodeInfo, err := n.GetInfo()
	if err != nil {
		return nil, errors.Wrap(err, "get node info")
	}
	n.rs, n.me = nodeInfo.SetName, nodeInfo.Me

	return n, nil
}

// ID returns node ID
func (n *Node) ID() string {
	return fmt.Sprintf("%s/%s", n.rs, n.me)
}

// RS return replicaset name node belongs to
func (n *Node) RS() string {
	return n.rs
}

// Name returns node name
func (n *Node) Name() string {
	return n.me
}

func (n *Node) Connect() error {
	conn, err := mongo.NewClient(options.Client().ApplyURI(n.curi).SetAppName("pbm-agent-exec").SetDirect(true))
	if err != nil {
		return errors.Wrap(err, "create mongo client")
	}
	err = conn.Connect(n.ctx)
	if err != nil {
		return errors.Wrap(err, "connect")
	}

	err = conn.Ping(n.ctx, nil)
	if err != nil {
		return errors.Wrap(err, "ping")
	}

	if n.cn != nil {
		err = n.cn.Disconnect(n.ctx)
		if err != nil {
			return errors.Wrap(err, "close existing connection")
		}
	}

	n.cn = conn
	return nil
}

func (n *Node) GetInfo() (*NodeInfo, error) {
	i := &NodeInfo{}
	err := n.cn.Database(DB).RunCommand(n.ctx, bson.D{{"isMaster", 1}}).Decode(i)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command")
	}
	return i, nil
}

// SizeDBs returns the total size in bytes of all databases' files on disk on replicaset
func (n *Node) SizeDBs() (int, error) {
	i := &struct {
		TotalSize int `bson:"totalSize"`
	}{}
	err := n.cn.Database(DB).RunCommand(n.ctx, bson.D{{"listDatabases", 1}}).Decode(i)
	if err != nil {
		return 0, errors.Wrap(err, "run mongo command listDatabases")
	}
	return i.TotalSize, nil
}

// IsSharded return true if node is part of the sharded cluster (in shard or configsrv replset).
func (n *Node) IsSharded() (bool, error) {
	i, err := n.GetInfo()
	if err != nil {
		return false, err
	}

	return i.IsSharded(), nil
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

	name := n.Name()

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

	name := n.Name()

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

func (n *Node) DumpConns() int {
	return n.dumpConns
}

func (n *Node) Session() *mongo.Client {
	return n.cn
}

func (n *Node) CurrentUser() (*AuthInfo, error) {
	c := &ConnectionStatus{}
	err := n.cn.Database(DB).RunCommand(n.ctx, bson.D{{"connectionStatus", 1}}).Decode(c)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command connectionStatus")
	}

	return &c.AuthInfo, nil
}

func (n *Node) DropTMPcoll() error {
	err := n.cn.Database(DB).Collection(TmpRolesCollection).Drop(n.ctx)
	if err != nil {
		return errors.Wrapf(err, "drop tmp roles collection %s", TmpRolesCollection)
	}

	err = n.cn.Database(DB).Collection(TmpUsersCollection).Drop(n.ctx)
	if err != nil {
		return errors.Wrapf(err, "drop tmp users collection %s", TmpUsersCollection)
	}

	return nil
}

func (n *Node) EnsureNoTMPcoll() error {
	cols, err := n.cn.Database(DB).ListCollectionNames(n.ctx, bson.D{})
	if err != nil {
		return errors.Wrapf(err, "list collections in %s", DB)
	}

	var ext []string
	for _, n := range cols {
		if n == TmpRolesCollection || n == TmpUsersCollection {
			ext = append(ext, DB+"."+n)
		}
	}
	if len(ext) > 0 {
		return errors.Errorf(
			"PBM temporary collections exist. It will lead to a failed restore. Please, drop next collections manually: %s",
			strings.Join(ext, ", "),
		)
	}
	return nil
}
