package topo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

type NodeStatus struct {
	ID     int             `bson:"_id" json:"_id"`
	Name   string          `bson:"name" json:"name"`
	Health defs.NodeHealth `bson:"health" json:"health"`

	// https://github.com/mongodb/mongo/blob/v8.0/src/mongo/db/repl/member_state.h#L52-L109
	State defs.NodeState `bson:"state" json:"state"`
	// https://github.com/mongodb/mongo/blob/v8.0/src/mongo/db/repl/member_state.h#L170-L193
	StateStr string `bson:"stateStr" json:"stateStr"`

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

func (s *NodeStatus) IsArbiter() bool {
	return s.State == defs.NodeStateArbiter
}

type StatusOpTimes struct {
	LastCommittedOpTime       *OpTime `bson:"lastCommittedOpTime" json:"lastCommittedOpTime"`
	ReadConcernMajorityOpTime *OpTime `bson:"readConcernMajorityOpTime" json:"readConcernMajorityOpTime"`
	AppliedOpTime             *OpTime `bson:"appliedOpTime" json:"appliedOpTime"`
	DurableOptime             *OpTime `bson:"durableOpTime" json:"durableOpTime"`
}

type ReplsetStatus struct {
	Set                     string               `bson:"set" json:"set"`
	Date                    time.Time            `bson:"date" json:"date"`
	MyState                 defs.NodeState       `bson:"myState" json:"myState"`
	Members                 []NodeStatus         `bson:"members" json:"members"`
	Term                    int64                `bson:"term,omitempty" json:"term,omitempty"`
	HeartbeatIntervalMillis int64                `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"` //nolint:lll
	Optimes                 *StatusOpTimes       `bson:"optimes,omitempty" json:"optimes,omitempty"`
	Errmsg                  string               `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	Ok                      int                  `bson:"ok" json:"ok"`
	ClusterTime             *ClusterTime         `bson:"$clusterTime,omitempty" json:"$clusterTime,omitempty"`
	ConfigServerState       *ConfigServerState   `bson:"$configServerState,omitempty" json:"$configServerState,omitempty"`
	OperationTime           *primitive.Timestamp `bson:"operationTime,omitempty" json:"operationTime,omitempty"`
	WriteMajorityCount      int                  `bson:"writeMajorityCount,omitempty" json:"writeMajorityCount,omitempty"`
}

func CurrentUser(ctx context.Context, m *mongo.Client) (*AuthInfo, error) {
	c := &ConnectionStatus{}
	err := m.Database(defs.DB).RunCommand(ctx, bson.D{{"connectionStatus", 1}}).Decode(c)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command connectionStatus")
	}

	return &c.AuthInfo, nil
}
