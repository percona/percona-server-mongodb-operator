package mdbstructs

import (
	"time"

	"github.com/globalsign/mgo/bson"
)

type ReplsetMemberHealth int
type ReplsetMemberState int

const (
	ReplsetMemberHealthDown ReplsetMemberHealth = iota
	ReplsetMemberHealthUp
	ReplsetMemberStateStartup    ReplsetMemberState = 0
	ReplsetMemberStatePrimary    ReplsetMemberState = 1
	ReplsetMemberStateSecondary  ReplsetMemberState = 2
	ReplsetMemberStateRecovering ReplsetMemberState = 3
	ReplsetMemberStateStartup2   ReplsetMemberState = 5
	ReplsetMemberStateUnknown    ReplsetMemberState = 6
	ReplsetMemberStateArbiter    ReplsetMemberState = 7
	ReplsetMemberStateDown       ReplsetMemberState = 8
	ReplsetMemberStateRollback   ReplsetMemberState = 9
	ReplsetMemberStateRemoved    ReplsetMemberState = 10
)

var ReplsetMemberStateStrings = map[ReplsetMemberState]string{
	ReplsetMemberStateStartup:    "STARTUP",
	ReplsetMemberStatePrimary:    "PRIMARY",
	ReplsetMemberStateSecondary:  "SECONDARY",
	ReplsetMemberStateRecovering: "RECOVERING",
	ReplsetMemberStateStartup2:   "STARTUP2",
	ReplsetMemberStateUnknown:    "UNKNOWN",
	ReplsetMemberStateArbiter:    "ARBITER",
	ReplsetMemberStateDown:       "DOWN",
	ReplsetMemberStateRollback:   "ROLLBACK",
	ReplsetMemberStateRemoved:    "REMOVED",
}

func (ms ReplsetMemberState) String() string {
	if str, ok := ReplsetMemberStateStrings[ms]; ok {
		return str
	}
	return ""
}

type StatusOpTimes struct {
	LastCommittedOpTime       *OpTime `bson:"lastCommittedOpTime" json:"lastCommittedOpTime"`
	ReadConcernMajorityOpTime *OpTime `bson:"readConcernMajorityOpTime" json:"readConcernMajorityOpTime"`
	AppliedOpTime             *OpTime `bson:"appliedOpTime" json:"appliedOpTime"`
	DurableOptime             *OpTime `bson:"durableOpTime" json:"durableOpTime"`
}

type ReplsetStatusMember struct {
	Id                int                 `bson:"_id" json:"_id"`
	Name              string              `bson:"name" json:"name"`
	Health            ReplsetMemberHealth `bson:"health" json:"health"`
	State             ReplsetMemberState  `bson:"state" json:"state"`
	StateStr          string              `bson:"stateStr" json:"stateStr"`
	Uptime            int64               `bson:"uptime" json:"uptime"`
	Optime            *OpTime             `bson:"optime" json:"optime"`
	OptimeDate        time.Time           `bson:"optimeDate" json:"optimeDate"`
	ConfigVersion     int                 `bson:"configVersion" json:"configVersion"`
	ElectionTime      bson.MongoTimestamp `bson:"electionTime,omitempty" json:"electionTime,omitempty"`
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
	Set                     string                 `bson:"set" json:"set"`
	Date                    time.Time              `bson:"date" json:"date"`
	MyState                 ReplsetMemberState     `bson:"myState" json:"myState"`
	Members                 []*ReplsetStatusMember `bson:"members" json:"members"`
	Term                    int64                  `bson:"term,omitempty" json:"term,omitempty"`
	HeartbeatIntervalMillis int64                  `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"`
	Optimes                 *StatusOpTimes         `bson:"optimes,omitempty" json:"optimes,omitempty"`
	Errmsg                  string                 `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	Ok                      int                    `bson:"ok" json:"ok"`
	ClusterTime             *ClusterTime           `bson:"$clusterTime,omitempty" json:"$clusterTime,omitempty"`
	ConfigServerState       *ConfigServerState     `bson:"$configServerState,omitempty" json:"$configServerState,omitempty"`
	GleStats                *GleStats              `bson:"$gleStats,omitempty" json:"$gleStats,omitempty"`
	OperationTime           *bson.MongoTimestamp   `bson:"operationTime,omitempty" json:"operationTime,omitempty"`
}
