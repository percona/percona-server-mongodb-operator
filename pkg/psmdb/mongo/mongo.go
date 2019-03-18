package mongo

import (
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	corev1 "k8s.io/api/core/v1"
)

// Replica Set tags: https://docs.mongodb.com/manual/tutorial/configure-replica-set-tag-sets/#add-tag-sets-to-a-replica-set
type ReplsetTags map[string]string

// Member document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type Member struct {
	ID           int         `bson:"_id" json:"_id"`
	Host         string      `bson:"host" json:"host"`
	ArbiterOnly  bool        `bson:"arbiterOnly" json:"arbiterOnly"`
	BuildIndexes bool        `bson:"buildIndexes" json:"buildIndexes"`
	Hidden       bool        `bson:"hidden" json:"hidden"`
	Priority     int         `bson:"priority" json:"priority"`
	Tags         ReplsetTags `bson:"tags,omitempty" json:"tags,omitempty"`
	SlaveDelay   int64       `bson:"slaveDelay" json:"slaveDelay"`
	Votes        int         `bson:"votes" json:"votes"`
}

type Cluster struct {
	Name                               string   `bson:"_id" json:"_id"`
	Version                            int      `bson:"version" json:"version"`
	Members                            []Member `bson:"members" json:"members"`
	Configsvr                          bool     `bson:"configsvr,omitempty" json:"configsvr,omitempty"`
	ProtocolVersion                    int      `bson:"protocolVersion,omitempty" json:"protocolVersion,omitempty"`
	Settings                           Settings `bson:"settings,omitempty" json:"settings,omitempty"`
	WriteConcernMajorityJournalDefault bool     `bson:"writeConcernMajorityJournalDefault,omitempty" json:"writeConcernMajorityJournalDefault,omitempty"`
}

// Settings document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type Settings struct {
	ChainingAllowed         bool                   `bson:"chainingAllowed,omitempty" json:"chainingAllowed,omitempty"`
	HeartbeatIntervalMillis int64                  `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"`
	HeartbeatTimeoutSecs    int                    `bson:"heartbeatTimeoutSecs,omitempty" json:"heartbeatTimeoutSecs,omitempty"`
	ElectionTimeoutMillis   int64                  `bson:"electionTimeoutMillis,omitempty" json:"electionTimeoutMillis,omitempty"`
	CatchUpTimeoutMillis    int64                  `bson:"catchUpTimeoutMillis,omitempty" json:"catchUpTimeoutMillis,omitempty"`
	GetLastErrorModes       map[string]ReplsetTags `bson:"getLastErrorModes,omitempty" json:"getLastErrorModes,omitempty"`
	GetLastErrorDefaults    WriteConcern           `bson:"getLastErrorDefaults,omitempty" json:"getLastErrorDefaults,omitempty"`
	ReplicaSetID            bson.ObjectId          `bson:"replicaSetId,omitempty" json:"replicaSetId,omitempty"`
}

// WriteConcern document: https://docs.mongodb.com/manual/reference/write-concern/
type WriteConcern struct {
	WriteConcern interface{} `bson:"w" json:"w"`
	WriteTimeout int         `bson:"wtimeout" json:"wtimeout"`
	Journal      bool        `bson:"j,omitempty" json:"j,omitempty"`
}

const (
	envMongoDBClusterAdminUser     = "MONGODB_CLUSTER_ADMIN_USER"
	envMongoDBClusterAdminPassword = "MONGODB_CLUSTER_ADMIN_PASSWORD"
)

func Dial(addrs []string, replset string, usersSecret *corev1.Secret) (*mgo.Session, error) {
	return mgo.DialWithInfo(&mgo.DialInfo{
		Addrs:          addrs,
		ReplicaSetName: replset,
		Username:       string(usersSecret.Data[envMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[envMongoDBClusterAdminPassword]),
		Timeout:        3 * time.Second,
		FailFast:       true,
	})
}
