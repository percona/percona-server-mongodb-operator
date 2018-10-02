package status

import (
	"time"

	"gopkg.in/mgo.v2/bson"
)

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

func (ms MemberState) String() string {
	if str, ok := MemberStateStrings[ms]; ok {
		return str
	}
	return ""
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
	ElectionTime      bson.MongoTimestamp `bson:"electionTime,omitempty" json:"electionTime,omitempty"`
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

func (s *Status) GetSelf() *Member {
	for _, member := range s.Members {
		if member.Self == true {
			return member
		}
	}
	return nil
}

func (s *Status) GetMember(name string) *Member {
	for _, member := range s.Members {
		if member.Name == name {
			return member
		}
	}
	return nil
}

func (s *Status) HasMember(name string) bool {
	return s.GetMember(name) != nil
}

func (s *Status) GetMemberId(id int) *Member {
	for _, member := range s.Members {
		if member.Id == id {
			return member
		}
	}
	return nil
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

func (s *Status) Secondaries() []*Member {
	return s.GetMembersByState(MemberStateSecondary, 0)
}
