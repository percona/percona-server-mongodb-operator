package cluster

import (
	"github.com/globalsign/mgo"
	"github.com/globalsign/mgo/bson"
	"github.com/percona/percona-backup-mongodb/mdbstructs"
)

type IsMaster struct {
	isMaster *mdbstructs.IsMaster
}

// NewIsMaster returns a struct for handling the output of the MongoDB 'isMaster'
// server command.
//
// https://docs.mongodb.com/manual/reference/command/isMaster/
//
func NewIsMaster(session *mgo.Session) (*IsMaster, error) {
	i := IsMaster{}
	err := session.Run(bson.D{{Name: "isMaster", Value: "1"}}, &i.isMaster)
	return &i, err
}

// IsMasterDoc returns the raw response from the MongoDB 'isMaster' server command.
func (i *IsMaster) IsMasterDoc() *mdbstructs.IsMaster {
	return i.isMaster
}

// Use the 'SetName' field to determine if a node is a member of replication.
// If 'SetName' is defined the node is a valid member.
//
func (i *IsMaster) IsReplset() bool {
	return i.isMaster.SetName != ""
}

// Use a combination of the 'isMaster', 'SetName' and 'Msg'
// field to determine a node is a mongos. A mongos will always
// have 'isMaster' equal to true, no 'SetName' defined and 'Msg'
// set to 'isdbgrid'.
//
// https://github.com/mongodb/mongo/blob/v3.6/src/mongo/s/commands/cluster_is_master_cmd.cpp#L112-L113
//
func (i *IsMaster) IsMongos() bool {
	return i.isMaster.IsMaster && !i.IsReplset() && i.isMaster.Msg == "isdbgrid"
}

// Use the undocumented 'configsvr' field to determine a node
// is a config server. This node must have replication enabled.
// For unexplained reasons the value of 'configsvr' must be an int
// equal to '2'.
//
// https://github.com/mongodb/mongo/blob/v3.6/src/mongo/db/repl/replication_info.cpp#L355-L358
//
func (i *IsMaster) IsConfigServer() bool {
	if i.isMaster.ConfigSvr == 2 && i.IsReplset() {
		return true
	}
	return false
}

// Use the existence of the '$configServerState' field to
// determine if a node is a mongod with the 'shardsvr'
// cluster role.
//
func (i *IsMaster) IsShardServer() bool {
	return i.IsReplset() && i.isMaster.ConfigServerState != nil
}

// The isMaster struct is from a Sharded Cluster if the seed host
// is a valid mongos, config server or shard server.
//
func (i *IsMaster) IsShardedCluster() bool {
	if i.IsConfigServer() || i.IsMongos() || i.IsShardServer() {
		return true
	}
	return false
}

// LastWrite returns the last write to the MongoDB database as a
// bson.MongoTimestamp
func (i *IsMaster) LastWrite() bson.MongoTimestamp {
	return i.isMaster.LastWrite.OpTime.Ts
}
