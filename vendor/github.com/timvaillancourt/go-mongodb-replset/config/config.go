package config

import (
	"encoding/json"

	"gopkg.in/mgo.v2/bson"
)

const ConfigCommand = "replSetGetConfig"

// Write Concern document: https://docs.mongodb.com/manual/reference/write-concern/
type WriteConcern struct {
	WriteConcern interface{} `bson:"w" json:"w"`
	WriteTimeout int         `bson:"wtimeout" json:"wtimeout"`
	Journal      bool        `bson:"j,omitempty" json:"j,omitempty"`
}

// Standard MongoDB response
type OkResponse struct {
	Ok int `bson:"ok" json:"ok" json:"ok"`
}

// Settings document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type Settings struct {
	ChainingAllowed         bool                    `bson:"chainingAllowed,omitempty" json:"chainingAllowed,omitempty"`
	HeartbeatIntervalMillis int64                   `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis,omitempty"`
	HeartbeatTimeoutSecs    int                     `bson:"heartbeatTimeoutSecs,omitempty" json:"heartbeatTimeoutSecs,omitempty"`
	ElectionTimeoutMillis   int64                   `bson:"electionTimeoutMillis,omitempty" json:"electionTimeoutMillis,omitempty"`
	CatchUpTimeoutMillis    int64                   `bson:"catchUpTimeoutMillis,omitempty" json:"catchUpTimeoutMillis,omitempty"`
	GetLastErrorModes       map[string]*ReplsetTags `bson:"getLastErrorModes,omitempty" json:"getLastErrorModes,omitempty"`
	GetLastErrorDefaults    *WriteConcern           `bson:"getLastErrorDefaults,omitempty" json:"getLastErrorDefaults,omitempty"`
	ReplicaSetId            bson.ObjectId           `bson:"replicaSetId,omitempty" json:"replicaSetId,omitempty"`
}

// Config document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type Config struct {
	Name                               string    `bson:"_id" json:"_id"`
	Version                            int       `bson:"version" json:"version"`
	Members                            []*Member `bson:"members" json:"members"`
	Configsvr                          bool      `bson:"configsvr,omitempty" json:"configsvr,omitempty"`
	ProtocolVersion                    int       `bson:"protocolVersion,omitempty" json:"protocolVersion,omitempty"`
	Settings                           *Settings `bson:"settings,omitempty" json:"settings,omitempty"`
	WriteConcernMajorityJournalDefault bool      `bson:"writeConcernMajorityJournalDefault,omitempty" json:"writeConcernMajorityJournalDefault,omitempty"`
}

// Response document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type ReplSetGetConfig struct {
	Config *Config `bson:"config" json:"config"`
	Errmsg string  `bson:"errmsg,omitempty" json:"errmsg,omitempty"`
	Ok     int     `bson:"ok" json:"ok" json:"ok"`
}

// Create a new *Config struct. Takes in string of the MongoDB Replica Set name.
func NewConfig(rsName string) *Config {
	return &Config{
		Name:    rsName,
		Members: make([]*Member, 0),
		Version: 1,
	}
}

// Increment the 'Version' field of Config.
func (c *Config) IncrVersion() {
	c.Version = c.Version + 1
}

// Return the Config as a pretty-printed JSON bytes.
func (c *Config) ToJSON() ([]byte, error) {
	return json.MarshalIndent(c, "", "\t")
}

// Return the Config as a pretty-printed JSON string.
func (c *Config) String() string {
	raw, err := c.ToJSON()
	if err != nil {
		return ""
	}
	return string(raw)
}
