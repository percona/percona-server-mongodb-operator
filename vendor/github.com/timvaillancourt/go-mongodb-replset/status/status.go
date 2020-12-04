package status

import (
	"encoding/json"
	"errors"
	"time"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

const (
	StatusCommand = "replSetGetStatus"
)

// Manager is an interface describing a Status manager
type Manager interface {
	GetMember(name string) *Member
	GetMemberId(id int) *Member
	GetMembersByState(state MemberState, limit int) []*Member
	GetSelf() *Member
	HasMember(name string) bool
	Primary() *Member
	Secondaries() []*Member
	String() string
	ToJSON() ([]byte, error)
}

type Optime struct {
	Timestamp bson.MongoTimestamp `bson:"ts" json:"ts"`
	Term      int64               `bson:"t" json:"t"`
}

type StatusOptimes struct {
	LastCommittedOpTime *Optime `bson:"lastCommittedOpTime" json:"lastCommittedOpTime"`
	AppliedOpTime       *Optime `bson:"appliedOpTime" json:"appliedOpTime"`
	DurableOptime       *Optime `bson:"durableOpTime" json:"durableOpTime"`
}

type Status struct {
	Set                     string         `bson:"set" json:"set"`
	Date                    time.Time      `bson:"date" json:"date"`
	MyState                 MemberState    `bson:"myState" json:"myState"`
	Members                 []*Member      `bson:"members" json:"members"`
	Term                    int64          `bson:"term,omitempty" json:"term,omitempty"`
	HeartbeatIntervalMillis int64          `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"`
	Optimes                 *StatusOptimes `bson:"optimes,omitempty" json:"optimes,omitempty"`
	Errmsg                  string         `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	Ok                      int            `bson:"ok" json:"ok"`
}

func New(session *mgo.Session) (*Status, error) {
	status := &Status{}
	err := session.DB("admin").Run(bson.D{{StatusCommand, 1}}, status)
	if err != nil {
		return nil, err
	}
	if status.Ok == 0 {
		return nil, errors.New(status.Errmsg)
	}
	return status, nil
}

func (s *Status) ToJSON() ([]byte, error) {
	return json.MarshalIndent(s, "", "\t")
}

func (s *Status) String() string {
	raw, err := s.ToJSON()
	if err != nil {
		return ""
	}
	return string(raw)
}
