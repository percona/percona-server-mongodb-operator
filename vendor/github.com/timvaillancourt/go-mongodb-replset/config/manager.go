package config

import (
	"errors"
	"sync"

	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

var (
	ErrNoReplsetId        = errors.New("replset config has no _id field!")
	ErrNoReplsetMembers   = errors.New("replset config has no members!")
	ErrZeroReplsetVersion = errors.New("replset config has a version field that is not greater than zero!")
)

// Manager is an interface describing a Config manager
type Manager interface {
	AddMember(member *Member)
	Get() *Config
	GetMember(name string) *Member
	IncrVersion()
	Initiate() error
	IsInitiated() bool
	Load() error
	RemoveMember(member *Member)
	Save() error
	Set(config *Config)
	Validate() error
}

type ConfigManager struct {
	sync.Mutex
	session   *mgo.Session
	config    *Config
	initiated bool
}

// Create a new *ConfigManager struct. Takes in a *mgo.Session and returns a *ConfigManager struct.
func New(session *mgo.Session) *ConfigManager {
	return &ConfigManager{
		session:   session,
		initiated: false,
	}
}

// Get the current config. Returns a *Config struct.
func (c *ConfigManager) Get() *Config {
	c.Lock()
	defer c.Unlock()

	return c.config
}

// Set the current config. Takes in a *Config struct.
func (c *ConfigManager) Set(config *Config) {
	c.Lock()
	defer c.Unlock()

	c.config = config
}

// Perform GetMember on a Config struct with locking
func (c *ConfigManager) GetMember(name string) *Member {
	c.Lock()
	defer c.Unlock()

	return c.config.GetMember(name)
}

// Perform AddMember on a Config struct with locking
func (c *ConfigManager) AddMember(member *Member) {
	c.Lock()
	defer c.Unlock()

	c.config.AddMember(member)
}

// Perform RemoveMember on a Config struct with locking
func (c *ConfigManager) RemoveMember(member *Member) {
	c.Lock()
	defer c.Unlock()

	c.config.RemoveMember(member)
}

// Perform IncrVersion on a Config struct with locking
func (c *ConfigManager) IncrVersion() {
	c.Lock()
	defer c.Unlock()

	c.config.IncrVersion()
}

// Load the current Config from the MongoDB session, overwriting the current Config if it exists.
// Uses the 'replSetGetConfig' server command. Returns an error or nil.
func (c *ConfigManager) Load() error {
	resp := &ReplSetGetConfig{}
	err := c.session.Run(bson.D{{"replSetGetConfig", 1}}, resp)
	if err != nil {
		return err
	}
	if resp.Ok == 1 && resp.Config != nil {
		c.Lock()
		defer c.Unlock()
		c.config = resp.Config
		c.initiated = true
	} else {
		return errors.New(resp.Errmsg)
	}
	return nil
}

// Check if the MongoDB Replica Set is initiated. Returns a boolean.
func (c *ConfigManager) IsInitiated() bool {
	if c.initiated {
		return true
	}
	err := c.Load()
	if err != nil {
		return false
	}
	return true
}

// Initiate the MongoDB Replica Set using the current Config, via the 'replSetInitiate' server command. Returns an error or nil.
func (c *ConfigManager) Initiate() error {
	if c.initiated {
		return nil
	}
	c.Lock()
	defer c.Unlock()

	resp := &OkResponse{}
	err := c.session.Run(bson.D{{"replSetInitiate", c.config}}, resp)
	if err != nil {
		if err.Error() == "already initialized" {
			c.initiated = true
			return nil
		}
		return err
	}
	if resp.Ok == 1 {
		c.initiated = true
	}
	return nil
}

// Validate the MongoDB Replica Set Config. Returns an error or nil.
func (c *ConfigManager) Validate() error {
	if c.config.Name == "" {
		return ErrNoReplsetId
	}
	if len(c.config.Members) == 0 {
		return ErrNoReplsetMembers
	}
	if c.config.Version == 0 {
		return ErrZeroReplsetVersion
	}
	return nil
}

// Save the current MongoDB Replica Set Config to the server via 'replSetReconfig' server command. Returns an error or nil.
func (c *ConfigManager) Save() error {
	c.Lock()
	defer c.Unlock()

	err := c.Validate()
	if err != nil {
		return err
	}
	if c.IsInitiated() {
		resp := &OkResponse{}
		err = c.session.Run(bson.D{{"replSetReconfig", c.config}}, resp)
	}
	if err != nil {
		return err
	}
	return nil
}
