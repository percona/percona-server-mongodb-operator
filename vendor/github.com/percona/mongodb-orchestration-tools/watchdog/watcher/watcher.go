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

package watcher

import (
	"errors"
	"sync"
	"time"

	"github.com/percona/mongodb-orchestration-tools/internal/db"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	"github.com/percona/mongodb-orchestration-tools/watchdog/config"
	"github.com/percona/mongodb-orchestration-tools/watchdog/replset"
	log "github.com/sirupsen/logrus"
	rsConfig "github.com/timvaillancourt/go-mongodb-replset/config"
	rsStatus "github.com/timvaillancourt/go-mongodb-replset/status"
	"gopkg.in/mgo.v2"
)

var (
	connectReplsetTimeout              = time.Minute * 3
	replsetReadPreference              = mgo.Primary
	waitForMongodAvailableRetries uint = 10
)

type Watcher struct {
	sync.Mutex
	config        *config.Config
	masterSession *mgo.Session
	dbConfig      *db.Config
	replset       *replset.Replset
	state         *replset.State
	quit          *chan bool
	running       bool
	activePods    *pod.Pods
}

func New(rs *replset.Replset, config *config.Config, quit *chan bool, activePods *pod.Pods) *Watcher {
	return &Watcher{
		config:     config,
		replset:    rs,
		state:      replset.NewState(rs.Name),
		quit:       quit,
		activePods: activePods,
	}
}

func (rw *Watcher) getReplsetSession() *mgo.Session {
	if rw.masterSession == nil || rw.masterSession.Ping() != nil {
		err := rw.connectReplsetSession()
		if err != nil {
			return nil
		}
	}
	return rw.masterSession
}

func (rw *Watcher) connectReplsetSession() error {
	var session *mgo.Session
	for {
		ticker := time.NewTicker(rw.config.ReplsetPoll)
		select {
		case <-ticker.C:
			rw.dbConfig = rw.replset.GetReplsetDBConfig(rw.config.SSL)
			if len(rw.dbConfig.DialInfo.Addrs) >= 1 {
				var err error
				if session == nil {
					session, err = db.GetSession(rw.dbConfig)
				}
				if err == nil && session != nil {
					session.SetMode(replsetReadPreference, true)
					err = session.Ping()
					if err == nil {
						ticker.Stop()
						break
					}
				}

				log.WithFields(log.Fields{
					"addrs":   rw.dbConfig.DialInfo.Addrs,
					"replset": rw.replset.Name,
					"ssl":     rw.dbConfig.SSL.Enabled,
				}).Errorf("Error connecting to mongodb replset: %s", err)

				if session != nil {
					session.Close()
				}
			} else {
				return errors.New("no addresses for dial info")
			}
		case <-time.After(connectReplsetTimeout):
			return errors.New("timeout getting replset connection")
		case <-*rw.quit:
			return errors.New("received quit")
		}
		break
	}

	rw.Lock()
	defer rw.Unlock()

	if rw.masterSession != nil {
		log.WithFields(log.Fields{
			"addrs":   rw.dbConfig.DialInfo.Addrs,
			"replset": rw.replset.Name,
			"ssl":     rw.dbConfig.SSL.Enabled,
		}).Info("Reconnecting to mongodb replset")
		rw.masterSession.Close()
	}
	rw.masterSession = session

	return nil
}

func (rw *Watcher) reconnectReplsetSession() {
	err := rw.connectReplsetSession()
	if err != nil {
		log.WithFields(log.Fields{
			"addrs":   rw.dbConfig.DialInfo.Addrs,
			"replset": rw.replset.Name,
			"ssl":     rw.dbConfig.SSL.Enabled,
			"error":   err,
		}).Error("Error reconnecting mongodb replset session")
	}
}

func (rw *Watcher) logReplsetState() {
	status := rw.state.GetStatus()
	if status == nil {
		return
	}

	primary := status.Primary()
	rsPrimary := rw.replset.GetMember(primary.Name)

	log.WithFields(log.Fields{
		"replset": rw.replset.Name,
		"host":    primary.Name,
		"task":    rsPrimary.Task.Name(),
		"state":   rsPrimary.Task.State(),
	}).Infof("Replset %s", primary.State)

	for _, member := range status.Members {
		if member.Name == primary.Name {
			continue
		}
		rsMember := rw.replset.GetMember(member.Name)
		if rsMember == nil || rsMember.Task == nil {
			continue
		}
		log.WithFields(log.Fields{
			"replset": rw.replset.Name,
			"host":    member.Name,
			"task":    rsMember.Task.Name(),
			"state":   rsMember.Task.State(),
		}).Infof("Replset %s", member.State)
	}
}

func (rw *Watcher) getMissingReplsetMembers() []*replset.Mongod {
	notInReplset := make([]*replset.Mongod, 0)
	replsetConfig := rw.state.GetConfig()
	if rw.state != nil && replsetConfig != nil {
		for _, member := range rw.replset.GetMembers() {
			cnfMember := replsetConfig.GetMember(member.Name())
			if cnfMember == nil {
				notInReplset = append(notInReplset, member)
			}
		}
	}
	return notInReplset
}

func (rw *Watcher) getScaledDownMembers() []*rsConfig.Member {
	scaledDown := make([]*rsConfig.Member, 0)
	status := rw.state.GetStatus()
	config := rw.state.GetConfig()
	for _, member := range status.GetMembersByState(rsStatus.MemberStateDown, 0) {
		rsMember := rw.replset.GetMember(member.Name)
		if !rw.activePods.Has(rsMember.PodName) {
			scaledDown = append(scaledDown, config.GetMember(member.Name))
		}
	}
	return scaledDown
}

func (rw *Watcher) waitForMongodAvailable(mongod *replset.Mongod) error {
	session, err := db.WaitForSession(
		mongod.DBConfig(rw.config.SSL),
		waitForMongodAvailableRetries,
		rw.config.ReplsetPoll,
	)
	if err != nil {
		return err
	}
	session.Close()
	return nil
}

func (rw *Watcher) replsetConfigAdder(add []*replset.Mongod) error {
	mongods := make([]*replset.Mongod, 0)
	for _, mongod := range add {
		err := rw.waitForMongodAvailable(mongod)
		if err != nil {
			log.WithFields(log.Fields{
				"host":    mongod.Name(),
				"retries": waitForMongodAvailableRetries,
			}).Error(err)
			continue
		}
		log.WithFields(log.Fields{
			"replset": rw.replset.Name,
			"host":    mongod.Name(),
		}).Info("Mongod not present in replset config, adding it to replset")
		mongods = append(mongods, mongod)
	}
	if len(mongods) == 0 {
		return nil
	}
	session := rw.getReplsetSession()
	if session != nil {
		err := rw.state.AddConfigMembers(session, rsConfig.New(session), mongods)
		if err != nil {
			return err
		}
	}
	rw.reconnectReplsetSession()
	return nil
}

func (rw *Watcher) replsetConfigRemover(remove []*rsConfig.Member) error {
	if rw.state == nil || len(remove) == 0 {
		return nil
	}
	session := rw.getReplsetSession()
	if session != nil {
		for _, member := range remove {
			lf := log.Fields{
				"replset": rw.replset.Name,
				"host":    member.Host,
			}

			rsMember := rw.replset.GetMember(member.Host)
			if rsMember == nil || rsMember.Task.IsUpdating() {
				log.WithFields(lf).Debug("Skipping remove on updating host")
				continue
			}

			log.WithFields(lf).Info("Removing removed/scaled-down replset member")
			err := rw.replset.RemoveMember(member.Host)
			if err != nil {
				return err
			}
		}
		err := rw.state.RemoveConfigMembers(session, rsConfig.New(session), remove)
		if err != nil {
			return err
		}
	}
	rw.reconnectReplsetSession()
	return nil
}

func (rw *Watcher) UpdateMongod(mongod *replset.Mongod) {
	state := mongod.Task.State()
	if state == nil || !mongod.Task.IsRunning() {
		return
	}
	fields := log.Fields{
		"replset": rw.replset.Name,
		"name":    mongod.Task.Name(),
		"host":    mongod.Name(),
		"state":   state.String(),
	}
	if rw.replset.HasMember(mongod.Name()) {
		log.WithFields(fields).Info("Updating running mongod task")
	} else {
		log.WithFields(fields).Info("Adding new mongod task")
	}

	err := rw.replset.UpdateMember(mongod)
	if err != nil {
		log.WithError(err).Errorf("Cannot update member %s", mongod.Name())
	}
}

func (rw *Watcher) setRunning(running bool) {
	rw.Lock()
	defer rw.Unlock()
	rw.running = running
}

func (rw *Watcher) State() *replset.State {
	return rw.state
}

func (rw *Watcher) IsRunning() bool {
	rw.Lock()
	defer rw.Unlock()
	return rw.running
}

func (rw *Watcher) Run() {
	err := rw.connectReplsetSession()
	if err != nil {
		if err.Error() != "received quit" {
			log.WithError(err).Error("Cannot connect to replset")
		}
		return
	}

	rw.setRunning(true)
	defer rw.setRunning(false)

	log.WithFields(log.Fields{
		"replset":  rw.replset.Name,
		"interval": rw.config.ReplsetPoll,
	}).Info("Watching replset")

	ticker := time.NewTicker(rw.config.ReplsetPoll)
	for {
		select {
		case <-ticker.C:
			session := rw.getReplsetSession()
			if session == nil {
				log.Errorf("Could not get session for replset: %s", rw.replset.Name)
				continue
			}

			err := rw.state.Fetch(session, rsConfig.New(session))
			if err != nil {
				log.Errorf("Error fetching state for replset %s: %s", rw.replset.Name, err)
				rw.reconnectReplsetSession()
				continue
			}

			if rw.state.GetStatus() == nil {
				log.Errorf("Could not get status for replset: %s", rw.replset.Name)
				continue
			}

			err = rw.replsetConfigAdder(rw.getMissingReplsetMembers())
			if err != nil {
				log.Errorf("Error adding member(s) to replset %s: %s", rw.replset.Name, err)
				continue
			}

			err = rw.replsetConfigRemover(rw.getScaledDownMembers())
			if err != nil {
				log.Errorf("Error removing stale/scaled-down member(s) from replset %s: %s", rw.replset.Name, err)
				continue
			}

			rw.logReplsetState()
		case <-*rw.quit:
			log.WithFields(log.Fields{
				"replset": rw.replset.Name,
			}).Info("Stopping watcher for replset")
			ticker.Stop()
			return
		}
	}
}
