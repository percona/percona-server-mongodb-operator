package config

// Member document from 'replSetGetConfig': https://docs.mongodb.com/manual/reference/command/replSetGetConfig/#dbcmd.replSetGetConfig
type Member struct {
	Id           int          `bson:"_id" json:"_id"`
	Host         string       `bson:"host" json:"host"`
	ArbiterOnly  bool         `bson:"arbiterOnly" json:"arbiterOnly"`
	BuildIndexes bool         `bson:"buildIndexes" json:"buildIndexes"`
	Hidden       bool         `bson:"hidden" json:"hidden"`
	Priority     int          `bson:"priority" json:"priority"`
	Tags         *ReplsetTags `bson:"tags,omitempty" json:"tags,omitempty"`
	SlaveDelay   int64        `bson:"slaveDelay" json:"slaveDelay"`
	Votes        int          `bson:"votes" json:"votes"`
}

// Create a new *Member struct. Takes in a string of the hostname of the new Member.
func NewMember(host string) *Member {
	return &Member{
		Id:           0,
		Host:         host,
		BuildIndexes: true,
		Priority:     1,
		Votes:        1,
	}
}

func (c *Config) getMemberMaxId() int {
	var maxId int
	for _, member := range c.Members {
		if member.Id > maxId {
			maxId = member.Id
		}
	}
	return maxId
}

// Add a *Member struct to the Config, if it does not already exist. Takes in a *Member struct to be added.
func (c *Config) AddMember(member *Member) {
	if c.HasMember(member.Host) {
		return
	}
	if len(c.Members) > 0 {
		memberMaxId := c.getMemberMaxId()
		if member.Id <= memberMaxId {
			member.Id = memberMaxId + 1
		}
	}
	c.Members = append(c.Members, member)
}

// Remove a *Member from the Config. Takes in a *Member struct to be removed.
func (c *Config) RemoveMember(removeMember *Member) {
	for i, member := range c.Members {
		if member.Host == removeMember.Host {
			c.Members = append(c.Members[:i], c.Members[i+1:]...)
			return
		}
	}
}

// Get a *Member from the Config. Takes in a string of the Member 'Host' field and returns a *Member if there is a match.
func (c *Config) GetMember(name string) *Member {
	for _, member := range c.Members {
		if member.Host == name {
			return member
		}
	}
	return nil
}

// Checks the existance of a *Member. Takes in a string of the Member 'Host' field and returns a boolean.
func (c *Config) HasMember(name string) bool {
	return c.GetMember(name) != nil
}

// Get a *Member from the Config by Id. Takes in an int of the Member 'Id' (_id) field and returns a *Member if there is a match.
func (c *Config) GetMemberId(id int) *Member {
	for _, member := range c.Members {
		if member.Id == id {
			return member
		}
	}
	return nil
}
