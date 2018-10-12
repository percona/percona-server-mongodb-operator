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
	"sync"

	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	"github.com/percona/mongodb-orchestration-tools/watchdog/config"
	"github.com/percona/mongodb-orchestration-tools/watchdog/replset"
	log "github.com/sirupsen/logrus"
)

type Manager interface {
	Close()
	Get(rsName string) *Watcher
	HasWatcher(rsName string) bool
	Stop(rsName string)
	Watch(rs *replset.Replset)
}

type WatcherManager struct {
	sync.Mutex
	config     *config.Config
	stop       *chan bool
	quitChans  map[string]chan bool
	watchers   map[string]*Watcher
	activePods *pod.Pods
}

func NewManager(config *config.Config, stop *chan bool, activePods *pod.Pods) *WatcherManager {
	return &WatcherManager{
		config:     config,
		stop:       stop,
		activePods: activePods,
		quitChans:  make(map[string]chan bool),
		watchers:   make(map[string]*Watcher),
	}
}

func (wm *WatcherManager) HasWatcher(rsName string) bool {
	wm.Lock()
	defer wm.Unlock()

	if _, ok := wm.watchers[rsName]; ok {
		return true
	}
	return false
}

func (wm *WatcherManager) Watch(rs *replset.Replset) {
	if !wm.HasWatcher(rs.Name) {
		log.WithFields(log.Fields{
			"replset": rs.Name,
		}).Info("Starting replset watcher")

		wm.Lock()

		quitChan := make(chan bool)
		wm.quitChans[rs.Name] = quitChan
		wm.watchers[rs.Name] = New(rs, wm.config, &quitChan, wm.activePods)
		go wm.watchers[rs.Name].Run()

		wm.Unlock()
	}
}

func (wm *WatcherManager) Get(rsName string) *Watcher {
	if !wm.HasWatcher(rsName) {
		return nil
	}
	return wm.watchers[rsName]
}

func (wm *WatcherManager) Stop(rsName string) {
	if wm.HasWatcher(rsName) {
		wm.Lock()
		defer wm.Unlock()
		close(wm.quitChans[rsName])
		delete(wm.quitChans, rsName)
	}
}

func (wm *WatcherManager) Close() {
	for rsName := range wm.watchers {
		wm.Stop(rsName)
	}
}
