package mongo

import (
	"time"

	"go.mongodb.org/mongo-driver/bson/primitive"
)

const (
	MinVotingMembers = 1
	MaxVotingMembers = 7
	MaxMembers       = 50
)

// Replica Set tags: https://docs.mongodb.com/manual/tutorial/configure-replica-set-tag-sets/#add-tag-sets-to-a-replica-set
type ReplsetTags map[string]string

// RSMember document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ConfigMember struct {
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

type ConfigMembers []ConfigMember

type RSConfig struct {
	ID                                 string        `bson:"_id" json:"_id"`
	Version                            int           `bson:"version" json:"version"`
	Members                            ConfigMembers `bson:"members" json:"members"`
	Configsvr                          bool          `bson:"configsvr,omitempty" json:"configsvr,omitempty"`
	ProtocolVersion                    int           `bson:"protocolVersion,omitempty" json:"protocolVersion,omitempty"`
	Settings                           Settings      `bson:"settings,omitempty" json:"settings,omitempty"`
	WriteConcernMajorityJournalDefault bool          `bson:"writeConcernMajorityJournalDefault,omitempty" json:"writeConcernMajorityJournalDefault,omitempty"`
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
	ReplicaSetID            primitive.ObjectID     `bson:"replicaSetId,omitempty" json:"replicaSetId,omitempty"`
}

// Response document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ReplSetGetConfig struct {
	Config     *RSConfig `bson:"config" json:"config"`
	OKResponse `bson:",inline"`
}

// BuildInfo contains information about mongod build params
type BuildInfo struct {
	Version    string `json:"version" bson:"version"`
	OKResponse `bson:",inline"`
}

// OKResponse is a standard MongoDB response
type OKResponse struct {
	Errmsg string `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	OK     int    `bson:"ok" json:"ok"`
}

// WriteConcern document: https://docs.mongodb.com/manual/reference/write-concern/
type WriteConcern struct {
	WriteConcern interface{} `bson:"w" json:"w"`
	WriteTimeout int         `bson:"wtimeout" json:"wtimeout"`
	Journal      bool        `bson:"j,omitempty" json:"j,omitempty"`
}

type BalancerStatus struct {
	Mode       string `json:"mode"`
	OKResponse `bson:",inline"`
}

type DBList struct {
	DBs []struct {
		Name string `json:"name"`
	} `json:"databases"`
	OKResponse `bson:",inline"`
}

type ShardList struct {
	Shards []struct {
		ID    string `json:"_id"`
		Host  string `json:"host"`
		State int    `json:"state"`
	} `json:"shards"`
	OKResponse `bson:",inline"`
}

const ShardRemoveCompleted string = "completed"

type ShardRemoveResp struct {
	Msg       string `json:"msg" bson:"msg"`
	State     string `json:"state" bson:"state"`
	Remaining struct {
		Chunks      int `json:"chunks" bson:"chunks"`
		JumboChunks int `json:"jumboChunks" bson:"jumboChunks"`
	} `json:"remaining" bson:"remaining"`
	OKResponse `bson:",inline"`
}

type Status struct {
	Set                     string         `bson:"set" json:"set"`
	Date                    time.Time      `bson:"date" json:"date"`
	MyState                 MemberState    `bson:"myState" json:"myState"`
	Members                 []*Member      `bson:"members" json:"members"`
	Term                    int64          `bson:"term,omitempty" json:"term,omitempty"`
	HeartbeatIntervalMillis int64          `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"`
	Optimes                 *StatusOptimes `bson:"optimes,omitempty" json:"optimes,omitempty"`
	OKResponse              `bson:",inline"`
}

type Member struct {
	Id                int                 `bson:"_id" json:"_id"`
	Name              string              `bson:"name" json:"name"`
	Health            MemberHealth        `bson:"health" json:"health"`
	State             MemberState         `bson:"state" json:"state"`
	StateStr          string              `bson:"stateStr" json:"stateStr"`
	Uptime            int64               `bson:"uptime" json:"uptime"`
	Optime            *Optime             `bson:"optime" json:"optime"`
	OptimeDate        time.Time           `bson:"optimeDate" json:"optimeDate"`
	ConfigVersion     int                 `bson:"configVersion" json:"configVersion"`
	ElectionTime      primitive.Timestamp `bson:"electionTime,omitempty" json:"electionTime,omitempty"`
	ElectionDate      time.Time           `bson:"electionDate,omitempty" json:"electionDate,omitempty"`
	InfoMessage       string              `bson:"infoMessage,omitempty" json:"infoMessage,omitempty"`
	OptimeDurable     *Optime             `bson:"optimeDurable,omitempty" json:"optimeDurable,omitempty"`
	OptimeDurableDate time.Time           `bson:"optimeDurableDate,omitempty" json:"optimeDurableDate,omitempty"`
	LastHeartbeat     time.Time           `bson:"lastHeartbeat,omitempty" json:"lastHeartbeat,omitempty"`
	LastHeartbeatRecv time.Time           `bson:"lastHeartbeatRecv,omitempty" json:"lastHeartbeatRecv,omitempty"`
	PingMs            int64               `bson:"pingMs,omitempty" json:"pingMs,omitempty"`
	Self              bool                `bson:"self,omitempty" json:"self,omitempty"`
	SyncingTo         string              `bson:"syncingTo,omitempty" json:"syncingTo,omitempty"`
}

type Optime struct {
	Timestamp primitive.Timestamp `bson:"ts" json:"ts"`
	Term      int64               `bson:"t" json:"t"`
}

type StatusOptimes struct {
	LastCommittedOpTime *Optime `bson:"lastCommittedOpTime" json:"lastCommittedOpTime"`
	AppliedOpTime       *Optime `bson:"appliedOpTime" json:"appliedOpTime"`
	DurableOptime       *Optime `bson:"durableOpTime" json:"durableOpTime"`
}

type MemberHealth int
type MemberState int

const (
	MemberHealthDown MemberHealth = iota
	MemberHealthUp
	MemberStateStartup    MemberState = 0
	MemberStatePrimary    MemberState = 1
	MemberStateSecondary  MemberState = 2
	MemberStateRecovering MemberState = 3
	MemberStateStartup2   MemberState = 5
	MemberStateUnknown    MemberState = 6
	MemberStateArbiter    MemberState = 7
	MemberStateDown       MemberState = 8
	MemberStateRollback   MemberState = 9
	MemberStateRemoved    MemberState = 10
)

var MemberStateStrings = map[MemberState]string{
	MemberStateStartup:    "STARTUP",
	MemberStatePrimary:    "PRIMARY",
	MemberStateSecondary:  "SECONDARY",
	MemberStateRecovering: "RECOVERING",
	MemberStateStartup2:   "STARTUP2",
	MemberStateUnknown:    "UNKNOWN",
	MemberStateArbiter:    "ARBITER",
	MemberStateDown:       "DOWN",
	MemberStateRollback:   "ROLLBACK",
	MemberStateRemoved:    "REMOVED",
}

func (s *Status) GetMembersByState(state MemberState, limit int) []*Member {
	members := make([]*Member, 0)
	for _, member := range s.Members {
		if member.State == state {
			members = append(members, member)
			if limit > 0 && len(members) == limit {
				return members
			}
		}
	}
	return members
}

func (s *Status) Primary() *Member {
	primary := s.GetMembersByState(MemberStatePrimary, 1)
	if len(primary) == 1 {
		return primary[0]
	}
	return nil
}
