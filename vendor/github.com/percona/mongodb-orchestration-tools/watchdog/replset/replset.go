// Copyright 2018 Percona LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package replset

import (
	"errors"
	"sync"

	"github.com/percona/mongodb-orchestration-tools/internal/db"
	"github.com/percona/mongodb-orchestration-tools/watchdog/config"
	"gopkg.in/mgo.v2"
)

const (
	MaxMembers       int = 50
	MinVotingMembers int = 1
	MaxVotingMembers int = 7
)

type Replset struct {
	sync.Mutex
	Name    string
	config  *config.Config
	members map[string]*Mongod
}

func New(config *config.Config, name string) *Replset {
	return &Replset{
		Name:    name,
		config:  config,
		members: make(map[string]*Mongod),
	}
}

func (r *Replset) getAddrs() []string {
	addrs := []string{}
	for _, member := range r.GetMembers() {
		addrs = append(addrs, member.Name())
	}
	return addrs
}

// UpdateMember adds/updates the state of a MongoDB instance in a Replica Set
func (r *Replset) UpdateMember(member *Mongod) error {
	r.Lock()
	defer r.Unlock()

	if !r.HasMember(member.Name()) && len(r.members) >= MaxMembers {
		return errors.New("maximum members reached")
	}
	r.members[member.Name()] = member
	return nil
}

// RemoveMember removes the state of a MongoDB instance from a Replica Set
func (r *Replset) RemoveMember(name string) error {
	r.Lock()
	defer r.Unlock()

	if !r.HasMember(name) {
		return errors.New("member does not exist")
	}
	delete(r.members, name)
	return nil
}

// HasMember returns a boolean reflecting whether or not the state of a MongoDB instance exists in Replica Set
func (r *Replset) HasMember(name string) bool {
	if _, ok := r.members[name]; ok {
		return true
	}
	return false
}

// GetMember returns a Mongod structure reflecting a MongoDB mongod instance
func (r *Replset) GetMember(name string) *Mongod {
	r.Lock()
	defer r.Unlock()

	if r.HasMember(name) {
		return r.members[name]
	}
	return nil
}

// GetMembers returns a map of all mongod instances in a MongoDB Replica Set
func (r *Replset) GetMembers() map[string]*Mongod {
	r.Lock()
	defer r.Unlock()

	return r.members
}

// GetReplsetDBConfig returns a db.Config for the MongoDB Replica Set
func (r *Replset) GetReplsetDBConfig(sslCnf *db.SSLConfig) *db.Config {
	cnf := &db.Config{
		DialInfo: &mgo.DialInfo{
			Addrs:          r.getAddrs(),
			Direct:         false,
			FailFast:       true,
			ReplicaSetName: r.Name,
			Timeout:        r.config.ReplsetTimeout,
		},
		SSL: sslCnf,
	}
	if r.config.Username != "" && r.config.Password != "" {
		cnf.DialInfo.Username = r.config.Username
		cnf.DialInfo.Password = r.config.Password
	}
	return cnf
}
