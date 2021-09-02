package pbm

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

type OpTime struct {
	TS   primitive.Timestamp `bson:"ts" json:"ts"`
	Term int64               `bson:"t" json:"t"`
}

// MongoLastWrite represents the last write to the MongoDB server
type MongoLastWrite struct {
	OpTime            OpTime    `bson:"opTime"`
	LastWriteDate     time.Time `bson:"lastWriteDate"`
	MajorityOpTime    OpTime    `bson:"majorityOpTime"`
	MajorityWriteDate time.Time `bson:"majorityWriteDate"`
}

// NodeInfo represents the mongo's node info
type NodeInfo struct {
	Hosts                        []string             `bson:"hosts,omitempty"`
	Msg                          string               `bson:"msg"`
	MaxBsonObjectSise            int64                `bson:"maxBsonObjectSize"`
	MaxMessageSizeBytes          int64                `bson:"maxMessageSizeBytes"`
	MaxWriteBatchSize            int64                `bson:"maxWriteBatchSize"`
	LocalTime                    time.Time            `bson:"localTime"`
	LogicalSessionTimeoutMinutes int64                `bson:"logicalSessionTimeoutMinutes"`
	MaxWireVersion               int64                `bson:"maxWireVersion"`
	MinWireVersion               int64                `bson:"minWireVersion"`
	OK                           int                  `bson:"ok"`
	SetName                      string               `bson:"setName,omitempty"`
	Primary                      string               `bson:"primary,omitempty"`
	SetVersion                   int32                `bson:"setVersion,omitempty"`
	IsPrimary                    bool                 `bson:"ismaster"`
	Secondary                    bool                 `bson:"secondary,omitempty"`
	Hidden                       bool                 `bson:"hidden,omitempty"`
	Passive                      bool                 `bson:"passive,omitempty"`
	ConfigSvr                    int                  `bson:"configsvr,omitempty"`
	Me                           string               `bson:"me"`
	LastWrite                    MongoLastWrite       `bson:"lastWrite"`
	ClusterTime                  *ClusterTime         `bson:"$clusterTime,omitempty"`
	ConfigServerState            *ConfigServerState   `bson:"$configServerState,omitempty"`
	OperationTime                *primitive.Timestamp `bson:"operationTime,omitempty"`
}

// IsSharded returns true is replset is part sharded cluster
func (i *NodeInfo) IsSharded() bool {
	return i.SetName != "" && (i.ConfigServerState != nil || i.ConfigSvr == 2)
}

// IsLeader returns true if node can act as backup leader (it's configsrv or non shareded rs)
func (i *NodeInfo) IsLeader() bool {
	return !i.IsSharded() || i.ReplsetRole() == ReplRoleConfigSrv
}

// IsClusterLeader - cluster leader is a primary node on configsrv
// or just primary node in non-sharded replicaset
func (i *NodeInfo) IsClusterLeader() bool {
	return i.IsPrimary && i.Me == i.Primary && i.IsLeader()
}

// ReplsetRole returns replset role in sharded clister
func (i *NodeInfo) ReplsetRole() ReplRole {
	switch {
	case i.ConfigSvr == 2:
		return ReplRoleConfigSrv
	case i.ConfigServerState != nil:
		return ReplRoleShard
	default:
		return ReplRoleUnknown
	}
}

// IsStandalone returns true if node is not a part of replica set
func (i *NodeInfo) IsStandalone() bool {
	return i.SetName == ""
}

type ClusterTime struct {
	ClusterTime primitive.Timestamp `bson:"clusterTime"`
	Signature   struct {
		Hash  primitive.Binary `bson:"hash"`
		KeyID int64            `bson:"keyId"`
	} `bson:"signature"`
}

type ConfigServerState struct {
	OpTime *OpTime `bson:"opTime"`
}

type Operation string

const (
	OperationInsert  Operation = "i"
	OperationNoop    Operation = "n"
	OperationUpdate  Operation = "u"
	OperationDelete  Operation = "d"
	OperationCommand Operation = "c"
)

type NodeHealth int

const (
	NodeHealthDown NodeHealth = iota
	NodeHealthUp
)

type NodeState int

const (
	NodeStateStartup NodeState = iota
	NodeStatePrimary
	NodeStateSecondary
	NodeStateRecovering
	NodeStateStartup2
	NodeStateUnknown
	NodeStateArbiter
	NodeStateDown
	NodeStateRollback
	NodeStateRemoved
)

type StatusOpTimes struct {
	LastCommittedOpTime       *OpTime `bson:"lastCommittedOpTime" json:"lastCommittedOpTime"`
	ReadConcernMajorityOpTime *OpTime `bson:"readConcernMajorityOpTime" json:"readConcernMajorityOpTime"`
	AppliedOpTime             *OpTime `bson:"appliedOpTime" json:"appliedOpTime"`
	DurableOptime             *OpTime `bson:"durableOpTime" json:"durableOpTime"`
}

type NodeStatus struct {
	ID                int                 `bson:"_id" json:"_id"`
	Name              string              `bson:"name" json:"name"`
	Health            NodeHealth          `bson:"health" json:"health"`
	State             NodeState           `bson:"state" json:"state"`
	StateStr          string              `bson:"stateStr" json:"stateStr"`
	Uptime            int64               `bson:"uptime" json:"uptime"`
	Optime            *OpTime             `bson:"optime" json:"optime"`
	OptimeDate        time.Time           `bson:"optimeDate" json:"optimeDate"`
	ConfigVersion     int                 `bson:"configVersion" json:"configVersion"`
	ElectionTime      primitive.Timestamp `bson:"electionTime,omitempty" json:"electionTime,omitempty"`
	ElectionDate      time.Time           `bson:"electionDate,omitempty" json:"electionDate,omitempty"`
	InfoMessage       string              `bson:"infoMessage,omitempty" json:"infoMessage,omitempty"`
	OptimeDurable     *OpTime             `bson:"optimeDurable,omitempty" json:"optimeDurable,omitempty"`
	OptimeDurableDate time.Time           `bson:"optimeDurableDate,omitempty" json:"optimeDurableDate,omitempty"`
	LastHeartbeat     time.Time           `bson:"lastHeartbeat,omitempty" json:"lastHeartbeat,omitempty"`
	LastHeartbeatRecv time.Time           `bson:"lastHeartbeatRecv,omitempty" json:"lastHeartbeatRecv,omitempty"`
	PingMs            int64               `bson:"pingMs,omitempty" json:"pingMs,omitempty"`
	Self              bool                `bson:"self,omitempty" json:"self,omitempty"`
	SyncingTo         string              `bson:"syncingTo,omitempty" json:"syncingTo,omitempty"`
}

type ReplsetStatus struct {
	Set                     string               `bson:"set" json:"set"`
	Date                    time.Time            `bson:"date" json:"date"`
	MyState                 NodeState            `bson:"myState" json:"myState"`
	Members                 []NodeStatus         `bson:"members" json:"members"`
	Term                    int64                `bson:"term,omitempty" json:"term,omitempty"`
	HeartbeatIntervalMillis int64                `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"`
	Optimes                 *StatusOpTimes       `bson:"optimes,omitempty" json:"optimes,omitempty"`
	Errmsg                  string               `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	Ok                      int                  `bson:"ok" json:"ok"`
	ClusterTime             *ClusterTime         `bson:"$clusterTime,omitempty" json:"$clusterTime,omitempty"`
	ConfigServerState       *ConfigServerState   `bson:"$configServerState,omitempty" json:"$configServerState,omitempty"`
	OperationTime           *primitive.Timestamp `bson:"operationTime,omitempty" json:"operationTime,omitempty"`
}

// Shard represent config.shard https://docs.mongodb.com/manual/reference/config-database/#config.shards
type Shard struct {
	ID   string `bson:"_id"`
	RS   string `bson:"-"`
	Host string `bson:"host"`
}

type ConnectionStatus struct {
	AuthInfo AuthInfo `bson:"authInfo" json:"authInfo"`
}

type AuthInfo struct {
	Users     []AuthUser      `bson:"authenticatedUsers" json:"authenticatedUsers"`
	UserRoles []AuthUserRoles `bson:"authenticatedUserRoles" json:"authenticatedUserRoles"`
}

type AuthUser struct {
	User string `bson:"user" json:"user"`
	DB   string `bson:"db" json:"db"`
}
type AuthUserRoles struct {
	Role string `bson:"role" json:"role"`
	DB   string `bson:"db" json:"db"`
}

type BalancerMode string

const (
	BalancerModeOn  BalancerMode = "full"
	BalancerModeOff BalancerMode = "off"
)

type BalancerStatus struct {
	Mode              BalancerMode `bson:"mode" json:"mode"`
	InBalancerRound   bool         `bson:"inBalancerRound" json:"inBalancerRound"`
	NumBalancerRounds int64        `bson:"numBalancerRounds" json:"numBalancerRounds"`
	Ok                int          `bson:"ok" json:"ok"`
}

func (b *BalancerStatus) IsOn() bool {
	return b.Mode == BalancerModeOn
}
