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
	"fmt"
	"sync"

	"github.com/percona/mongodb-orchestration-tools/pkg/pod"

	log "github.com/sirupsen/logrus"
	rsConfig "github.com/timvaillancourt/go-mongodb-replset/config"
	rsStatus "github.com/timvaillancourt/go-mongodb-replset/status"
	"gopkg.in/mgo.v2"
)

const (
	serviceTagName = "serviceName"
)

// State is a struct reflecting the state of a MongoDB Replica Set
type State struct {
	sync.Mutex
	Replset  string
	Config   *rsConfig.Config
	Status   *rsStatus.Status
	doUpdate bool
}

func (s *State) updateConfig(configManager rsConfig.Manager) error {
	if s.doUpdate == false {
		return nil
	}

	configManager.IncrVersion()
	config := configManager.Get()
	log.WithFields(log.Fields{
		"replset":        s.Replset,
		"config_version": config.Version,
	}).Info("Writing new replset config")
	fmt.Println(config)

	err := configManager.Save()
	if err != nil {
		log.WithError(err).Error("Cannot save replset config")
		return err
	}
	s.doUpdate = false

	return nil
}

func (s *State) fetchConfig(configManager rsConfig.Manager) error {
	err := configManager.Load()
	if err != nil {
		return err
	}

	s.Config = configManager.Get()
	return nil
}

func (s *State) fetchStatus(session *mgo.Session) error {
	status, err := rsStatus.New(session)
	if err != nil {
		return err
	}

	s.Status = status
	return nil
}

// VotingMembers returns the number of replset members with one or more votes
func (s *State) VotingMembers() int {
	if s.Config == nil {
		return -1
	}
	votingMembers := 0
	for _, member := range s.Config.Members {
		if member.Votes > 0 {
			votingMembers++
		}
	}
	return votingMembers
}

func isEven(i int) bool {
	return i%2 == 0
}

func (s *State) getMaxIDVotingMember() *rsConfig.Member {
	var maxIDMember *rsConfig.Member
	for _, member := range s.Config.Members {
		if member.Votes == 0 {
			continue
		}
		if maxIDMember == nil || member.Id > maxIDMember.Id {
			maxIDMember = member
		}
	}
	return maxIDMember
}

func (s *State) getMinIDNonVotingMember() *rsConfig.Member {
	var minIDMember *rsConfig.Member
	for _, member := range s.Config.Members {
		if member.Votes == 1 || member.Hidden {
			continue
		}
		if minIDMember == nil || member.Id < minIDMember.Id {
			minIDMember = member
		}
	}
	return minIDMember
}

func (s *State) resetConfigVotes() {
	totalMembers := 0
	totalVoteable := 0
	for _, member := range s.Config.Members {
		if !member.Hidden {
			totalVoteable++
		}
		totalMembers++
	}
	votingMembers := s.VotingMembers()

	if !isEven(votingMembers) && votingMembers <= MaxVotingMembers && votingMembers >= MinVotingMembers {
		return
	}

	log.WithFields(log.Fields{
		"total_members":  totalMembers,
		"total_voteable": totalVoteable,
		"voting":         votingMembers,
		"voting_max":     MaxVotingMembers,
	}).Info("Adjusting replica set votes")

	for isEven(votingMembers) || votingMembers > MaxVotingMembers {
		if isEven(votingMembers) && votingMembers < MaxVotingMembers && totalVoteable > votingMembers {
			member := s.getMinIDNonVotingMember()
			if member != nil && votingMembers < MaxVotingMembers {
				log.Infof("Adding replica set vote to member: %s", member.Host)
				member.Priority = 1
				member.Votes = 1
				votingMembers++
			}
		} else if votingMembers > MinVotingMembers {
			member := s.getMaxIDVotingMember()
			if member != nil {
				log.Infof("Removing replica set vote from member: %s", member.Host)
				member.Priority = 0
				member.Votes = 0
				votingMembers--
			}
		}
	}

	log.Infof("Replica set now has %d voting members", s.VotingMembers())
}

// NewState returns a new State struct
func NewState(replset string) *State {
	return &State{
		Replset: replset,
	}
}

// Fetch gets the current MongoDB Replica Set status and config while locking the State
func (s *State) Fetch(session *mgo.Session, configManager rsConfig.Manager) error {
	s.Lock()
	defer s.Unlock()

	log.WithFields(log.Fields{
		"replset": s.Replset,
	}).Info("Updating replset config and status")

	err := s.fetchConfig(configManager)
	if err != nil {
		return err
	}

	return s.fetchStatus(session)
}

// GetConfig returns a Config struct representing a MongoDB Replica Set configuration
func (s *State) GetConfig() *rsConfig.Config {
	s.Lock()
	defer s.Unlock()

	return s.Config
}

// GetStatus returns a Status struct representing the status of a MongoDB Replica Set
func (s *State) GetStatus() *rsStatus.Status {
	s.Lock()
	defer s.Unlock()

	return s.Status
}

// AddConfigMembers adds members to the MongoDB Replica Set config
func (s *State) AddConfigMembers(session *mgo.Session, configManager rsConfig.Manager, members []*Mongod) error {
	if len(members) == 0 {
		return nil
	}

	s.Lock()
	defer s.Unlock()

	err := s.fetchConfig(configManager)
	if err != nil {
		log.Errorf("Error fetching config while adding members: '%s'", err.Error())
		return err
	}

	for _, mongod := range members {
		member := rsConfig.NewMember(mongod.Name())
		member.Tags = &rsConfig.ReplsetTags{
			serviceTagName: mongod.ServiceName,
		}
		if len(s.Config.Members) >= MaxMembers {
			log.Errorf("Maximum replset member count reached, cannot add member")
			break
		}
		if s.VotingMembers() >= MaxVotingMembers {
			log.Infof("Max replset voting members reached, disabling votes for new config member: %s", mongod.Name())
			member.Priority = 0
			member.Votes = 0
		}
		if mongod.Task.IsTaskType(pod.TaskTypeMongodBackup) {
			log.Infof("Adding dedicated backup mongod as a hidden-secondary: %s", mongod.Name())
			member.Hidden = true
			member.Priority = 0
			member.Tags = &rsConfig.ReplsetTags{
				"backup":       "true",
				serviceTagName: mongod.ServiceName,
			}
			member.Votes = 0
		}
		configManager.AddMember(member)
		s.doUpdate = true
	}
	s.resetConfigVotes()

	return s.updateConfig(configManager)
}

// RemoveConfigMembers removes members from the MongoDB Replica Set config
func (s *State) RemoveConfigMembers(session *mgo.Session, configManager rsConfig.Manager, members []*rsConfig.Member) error {
	if len(members) == 0 {
		return nil
	}

	s.Lock()
	defer s.Unlock()

	err := s.fetchConfig(configManager)
	if err != nil {
		log.Errorf("Error fetching config while removing members: '%s'", err.Error())
		return err
	}

	for _, member := range members {
		configManager.RemoveMember(member)
		s.doUpdate = true
	}
	s.resetConfigVotes()

	return s.updateConfig(configManager)
}
