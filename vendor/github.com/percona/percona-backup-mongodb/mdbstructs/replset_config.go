package mdbstructs

import "github.com/globalsign/mgo/bson"

// Write Concern document: https://docs.mongodb.com/manual/reference/write-concern/
type WriteConcern struct {
	WriteConcern interface{} `bson:"w" json:"w"`
	WriteTimeout int         `bson:"wtimeout" json:"wtimeout"`
	Journal      bool        `bson:"j,omitempty" json:"j,omitempty"`
}

// Member document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ReplsetConfigMember struct {
	Id           int               `bson:"_id" json:"_id"`
	Host         string            `bson:"host" json:"host"`
	ArbiterOnly  bool              `bson:"arbiterOnly" json:"arbiterOnly"`
	BuildIndexes bool              `bson:"buildIndexes" json:"buildIndexes"`
	Hidden       bool              `bson:"hidden" json:"hidden"`
	Priority     int               `bson:"priority" json:"priority"`
	Tags         map[string]string `bson:"tags,omitempty" json:"tags,omitempty"`
	SlaveDelay   int64             `bson:"slaveDelay" json:"slaveDelay"`
	Votes        int               `bson:"votes" json:"votes"`
}

// Settings document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ReplsetConfigSettings struct {
	ChainingAllowed         bool                         `bson:"chainingAllowed,omitempty" json:"chainingAllowed,omitempty"`
	HeartbeatIntervalMillis int64                        `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"`
	HeartbeatTimeoutSecs    int                          `bson:"heartbeatTimeoutSecs,omitempty" json:"heartbeatTimeoutSecs,omitempty"`
	ElectionTimeoutMillis   int64                        `bson:"electionTimeoutMillis,omitempty" json:"electionTimeoutMillis,omitempty"`
	CatchUpTimeoutMillis    int64                        `bson:"catchUpTimeoutMillis,omitempty" json:"catchUpTimeoutMillis,omitempty"`
	GetLastErrorModes       map[string]map[string]string `bson:"getLastErrorModes,omitempty" json:"getLastErrorModes,omitempty"`
	GetLastErrorDefaults    *WriteConcern                `bson:"getLastErrorDefaults,omitempty" json:"getLastErrorDefaults,omitempty"`
	ReplicaSetId            bson.ObjectId                `bson:"replicaSetId,omitempty" json:"replicaSetId,omitempty"`
}

// Config document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ReplsetConfig struct {
	Name                               string                 `bson:"_id" json:"_id"`
	Version                            int                    `bson:"version" json:"version"`
	Members                            []*ReplsetConfigMember `bson:"members" json:"members"`
	Configsvr                          bool                   `bson:"configsvr,omitempty" json:"configsvr,omitempty"`
	ProtocolVersion                    int                    `bson:"protocolVersion,omitempty" json:"protocolVersion,omitempty"`
	Settings                           *ReplsetConfigSettings `bson:"settings,omitempty" json:"settings,omitempty"`
	WriteConcernMajorityJournalDefault bool                   `bson:"writeConcernMajorityJournalDefault,omitempty" json:"writeConcernMajorityJournalDefault,omitempty"`
}

// Response document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ReplSetGetConfig struct {
	Config            *ReplsetConfig       `bson:"config" json:"config"`
	Errmsg            string               `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	Ok                int                  `bson:"ok" json:"ok" json:"ok"`
	ClusterTime       *ClusterTime         `bson:"$clusterTime,omitempty" json:"$clusterTime,omitempty"`
	ConfigServerState *ConfigServerState   `bson:"$configServerState,omitempty" json:"$configServerState,omitempty"`
	GleStats          *GleStats            `bson:"$gleStats,omitempty" json:"$gleStats,omitempty"`
	OperationTime     *bson.MongoTimestamp `bson:"operationTime,omitempty" json:"operationTime,omitempty"`
}
