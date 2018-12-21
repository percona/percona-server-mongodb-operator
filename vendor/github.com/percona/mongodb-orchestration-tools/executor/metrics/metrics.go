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

package metrics

import (
	"sync"
	"time"

	mgostatsd "github.com/scullxbones/mgo-statsd"
	log "github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
)

const jobName = "DC/OS Metrics"

// Pusher is an interface for a DC/OS Metrics pusher
type Pusher interface {
	GetServerStatus(session *mgo.Session) (*mgostatsd.ServerStatus, error)
	Push(status *mgostatsd.ServerStatus) error
}

type Metrics struct {
	sync.Mutex
	config  *Config
	running bool
	session *mgo.Session
	pusher  Pusher
}

func New(config *Config, session *mgo.Session, pusher Pusher) *Metrics {
	return &Metrics{
		config:  config,
		session: session,
		pusher:  pusher,
	}
}

func (m *Metrics) Name() string {
	return jobName
}

func (m *Metrics) DoRun() bool {
	return m.config.Enabled
}

func (m *Metrics) setRunning(running bool) {
	m.Lock()
	defer m.Unlock()
	m.running = running
}

func (m *Metrics) IsRunning() bool {
	m.Lock()
	defer m.Unlock()
	return m.running
}

func (m *Metrics) Run(quit *chan bool) {
	if m.DoRun() == false {
		log.Warn("DC/OS Metrics client executor disabled! Skipping start")
		return
	}

	log.WithFields(log.Fields{
		"interval":    m.config.Interval,
		"statsd_host": m.config.StatsdHost,
		"statsd_port": m.config.StatsdPort,
	}).Info("Starting DC/OS Metrics pusher")

	ticker := time.NewTicker(m.config.Interval)
	m.setRunning(true)
	for {
		select {
		case <-ticker.C:
			status, err := m.pusher.GetServerStatus(m.session)
			if err != nil {
				log.Warnf("Failed to get serverStatus for DC/OS Metrics: %s", err)
				continue
			}

			log.WithFields(log.Fields{
				"statsd_host": m.config.StatsdHost,
				"statsd_port": m.config.StatsdPort,
			}).Info("Pushing DC/OS Metrics")

			err = m.pusher.Push(status)
			if err != nil {
				log.Errorf("DC/OS Metrics push error: %s", err)
			}
		case <-*quit:
			log.Info("Stopping DC/OS Metrics pusher")
			ticker.Stop()
			m.setRunning(false)
			break
		}
	}
}
