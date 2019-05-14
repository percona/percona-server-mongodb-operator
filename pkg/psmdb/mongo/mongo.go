package mongo

import (
	"crypto/tls"
	"fmt"
	"net"
	"time"

	"github.com/pkg/errors"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
	corev1 "k8s.io/api/core/v1"
)

const (
	MinVotingMembers = 1
	MaxVotingMembers = 7
	MaxMembers       = 50
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

type RSMembers []Member

type RSConfig struct {
	ID                                 string    `bson:"_id" json:"_id"`
	Version                            int       `bson:"version" json:"version"`
	Members                            RSMembers `bson:"members" json:"members"`
	Configsvr                          bool      `bson:"configsvr,omitempty" json:"configsvr,omitempty"`
	ProtocolVersion                    int       `bson:"protocolVersion,omitempty" json:"protocolVersion,omitempty"`
	Settings                           Settings  `bson:"settings,omitempty" json:"settings,omitempty"`
	WriteConcernMajorityJournalDefault bool      `bson:"writeConcernMajorityJournalDefault,omitempty" json:"writeConcernMajorityJournalDefault,omitempty"`
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

// Response document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ReplSetGetConfig struct {
	Config *RSConfig `bson:"config" json:"config"`
	Errmsg string    `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	OKResponse
}

// OKResponse is a standard MongoDB response
type OKResponse struct {
	Errmsg string `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	OK     int    `bson:"ok" json:"ok" json:"ok"`
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

func Dial(addrs []string, replset string, usersSecret *corev1.Secret, useTLS bool) (*mgo.Session, error) {
	dialInfo := mgo.DialInfo{
		Addrs:          addrs,
		ReplicaSetName: replset,
		Username:       string(usersSecret.Data[envMongoDBClusterAdminUser]),
		Password:       string(usersSecret.Data[envMongoDBClusterAdminPassword]),
		Timeout:        3 * time.Second,
		FailFast:       true,
	}
	if useTLS {
		tlsConfig := &tls.Config{
			InsecureSkipVerify: true,
		}
		dialInfo.DialServer = func(addr *mgo.ServerAddr) (net.Conn, error) {
			return tls.Dial("tcp", addr.String(), tlsConfig)
		}
	}
	session, err := mgo.DialWithInfo(&dialInfo)
	if err != nil {
		return nil, err
	}

	session.SetMode(mgo.Primary, true)
	session.SetSafe(&mgo.Safe{
		WMode: "majority",
		FSync: true,
	})
	return session, nil
}

func ReadConfig(session *mgo.Session) (RSConfig, error) {
	resp := &ReplSetGetConfig{}
	err := session.Run(bson.D{{"replSetGetConfig", 1}}, resp)
	if err != nil {
		return RSConfig{}, errors.Wrap(err, "replSetGetConfig")
	}
	if resp.Config == nil {
		return RSConfig{}, errors.New("mongo says: " + resp.Errmsg)
	}

	return *resp.Config, nil
}

func WriteConfig(session *mgo.Session, cfg RSConfig) error {
	resp := &OKResponse{}

	// TODO The 'force' flag should be set to true if there is no PRIMARY in the replset (but this shouldn't ever happen).
	err := session.Run(bson.D{{"replSetReconfig", cfg}, {"force", false}}, resp)
	if err != nil {
		return errors.Wrap(err, "replSetReconfig")
	}

	if resp.OK != 1 {
		return errors.Wrap(err, "mongo")
	}

	return nil
}

// RemoveOld removes from the list those members which are not present in the given list.
// It always should leave at least one element. The config won't be valid for mongo otherwise.
// Better, if the last element has the smallest ID in order not to produce defragmentation
// when the next element will be added (ID = maxID + 1). Mongo replica set member ID must be between 0 and 255, so it matters.
func (m *RSMembers) RemoveOld(compareWith RSMembers) (changes bool) {
	cm := make(map[string]struct{}, len(compareWith))

	for _, member := range compareWith {
		cm[member.Host] = struct{}{}
	}

	// going from the end to the starting in order to leave last element with the smallest id
	for i := len(*m) - 1; i >= 0 && len(*m) > 1; i-- {
		member := []Member(*m)[i]
		if _, ok := cm[member.Host]; !ok {
			*m = append([]Member(*m)[:i], []Member(*m)[i+1:]...)
			changes = true
		}
	}

	return changes
}

// AddNew adds new members from given list
func (m *RSMembers) AddNew(from RSMembers) (changes bool) {
	cm := make(map[string]struct{}, len(*m))
	lastID := 0

	for _, member := range *m {
		cm[member.Host] = struct{}{}
		if member.ID > lastID {
			lastID = member.ID
		}
	}

	for _, member := range from {
		if _, ok := cm[member.Host]; !ok {
			lastID++
			member.ID = lastID
			*m = append(*m, member)
			changes = true
		}
	}

	return changes
}

// SetVotes sets voting parameters for members list
func (m *RSMembers) SetVotes() {
	votes := 0
	lastVoteIdx := -1
	for i, member := range *m {
		if member.Hidden {
			continue
		}
		if votes < MaxVotingMembers {
			[]Member(*m)[i].Votes = 1
			votes++
			if !member.ArbiterOnly {
				lastVoteIdx = i
				[]Member(*m)[i].Priority = 1
			}
		} else if member.ArbiterOnly {
			// Arbiter should always have a vote
			[]Member(*m)[i].Votes = 1
			[]Member(*m)[lastVoteIdx].Votes = 0
			[]Member(*m)[lastVoteIdx].Priority = 0
		}
	}
	if votes == 0 {
		return
	}

	if votes%2 == 0 {
		[]Member(*m)[lastVoteIdx].Votes = 0
		[]Member(*m)[lastVoteIdx].Priority = 0
	}
}

func (m Member) String() string {
	return fmt.Sprintf("{votes: %d, priority: %d}", m.Votes, m.Priority)
}
