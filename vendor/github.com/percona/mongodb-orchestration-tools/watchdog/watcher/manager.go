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
	Get(serviceName, rsName string) *Watcher
	HasWatcher(serviceName, rsName string) bool
	Stop(serviceName, rsName string)
	Watch(serviceName string, rs *replset.Replset)
}

type WatcherManager struct {
	sync.Mutex
	config     *config.Config
	quitChans  map[string]chan bool
	watchers   map[string]*Watcher
	activePods *pod.Pods
}

func NewManager(config *config.Config, activePods *pod.Pods) *WatcherManager {
	return &WatcherManager{
		config:     config,
		activePods: activePods,
		quitChans:  make(map[string]chan bool),
		watchers:   make(map[string]*Watcher),
	}
}

func (wm *WatcherManager) HasWatcher(serviceName, rsName string) bool {
	return wm.Get(serviceName, rsName) != nil
}

func (wm *WatcherManager) Watch(serviceName string, rs *replset.Replset) {
	if wm.HasWatcher(serviceName, rs.Name) {
		return
	}

	log.WithFields(log.Fields{
		"replset": rs.Name,
		"service": serviceName,
	}).Info("Starting replset watcher")

	wm.Lock()
	defer wm.Unlock()

	quitChan := make(chan bool)
	watcherName := serviceName + "-" + rs.Name
	wm.quitChans[watcherName] = quitChan
	wm.watchers[watcherName] = New(rs, wm.config, quitChan, wm.activePods)

	go wm.watchers[watcherName].Run()
}

func (wm *WatcherManager) Get(serviceName, rsName string) *Watcher {
	wm.Lock()
	defer wm.Unlock()

	return wm.watchers[serviceName+"-"+rsName]
}

func (wm *WatcherManager) stopWatcher(name string) {
	for watcherName := range wm.watchers {
		if watcherName == name {
			close(wm.quitChans[watcherName])
			delete(wm.quitChans, watcherName)
			delete(wm.watchers, watcherName)
		}
	}
}

func (wm *WatcherManager) Stop(serviceName, rsName string) {
	wm.Lock()
	defer wm.Unlock()

	wm.stopWatcher(serviceName + "-" + rsName)
}

func (wm *WatcherManager) Close() {
	wm.Lock()
	defer wm.Unlock()

	for watcherName := range wm.watchers {
		wm.stopWatcher(watcherName)
	}
}
