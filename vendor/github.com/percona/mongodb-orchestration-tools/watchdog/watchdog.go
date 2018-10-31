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

package watchdog

import (
	"runtime"
	"sync"
	"time"

	tools "github.com/percona/mongodb-orchestration-tools"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	"github.com/percona/mongodb-orchestration-tools/watchdog/config"
	"github.com/percona/mongodb-orchestration-tools/watchdog/replset"
	"github.com/percona/mongodb-orchestration-tools/watchdog/watcher"
	"github.com/prometheus/client_golang/prometheus"
	log "github.com/sirupsen/logrus"
)

var (
	sourceFetches = prometheus.NewCounterVec(prometheus.CounterOpts{
		Subsystem: "source",
		Name:      "fetches_total",
		Help:      "API fetches",
	}, []string{"type"})
)

func init() {
	prometheus.MustRegister(sourceFetches)
}

type Watchdog struct {
	config         *config.Config
	podSource      pod.Source
	watcherManager watcher.Manager
	quit           *chan bool
	activePods     *pod.Pods
}

func New(config *config.Config, quit *chan bool, podSource pod.Source) *Watchdog {
	activePods := pod.NewPods()
	return &Watchdog{
		config:         config,
		podSource:      podSource,
		watcherManager: watcher.NewManager(config, quit, activePods),
		quit:           quit,
		activePods:     activePods,
	}
}

func (w *Watchdog) podMongodFetcher(podName string, wg *sync.WaitGroup) {
	defer wg.Done()

	log.WithFields(log.Fields{
		"pod": podName,
	}).Info("Getting tasks for pod")

	tasks, err := w.podSource.GetTasks(podName)
	if err != nil {
		log.WithFields(log.Fields{
			"pod":   podName,
			"error": err,
		}).Error("Error fetching pod tasks")
		return
	}
	sourceFetches.With(prometheus.Labels{"type": "get_pod_tasks"}).Inc()

	for _, task := range tasks {
		if !task.IsTaskType(pod.TaskTypeMongod) {
			log.WithFields(log.Fields{"task": task.Name()}).Debug("Skipping non-mongod task")
			continue
		}

		mongod, err := replset.NewMongod(task, w.config.ServiceName, podName)
		if err != nil {
			log.WithFields(log.Fields{
				"task":  task.Name(),
				"error": err,
			}).Error("Error creating mongod object")
			return
		}

		// ensure the replset has a watcher started
		if !w.watcherManager.HasWatcher(mongod.Replset) {
			rs := replset.New(w.config, mongod.Replset)
			w.watcherManager.Watch(rs)
		}

		// send the update to the watcher for the given replset
		w.watcherManager.Get(mongod.Replset).UpdateMongod(mongod)
	}
}

func (w *Watchdog) doIgnorePod(podName string) bool {
	for _, ignorePodName := range w.config.IgnorePods {
		if podName == ignorePodName {
			return true
		}
	}
	return false
}

func (w *Watchdog) fetchPods() {
	log.WithFields(log.Fields{
		"source": w.podSource.Name(),
		"url":    w.podSource.URL(),
	}).Info("Getting pods from source")

	pods, err := w.podSource.Pods()
	if err != nil {
		log.WithFields(log.Fields{
			"url":   w.podSource.URL(),
			"error": err,
		}).Error("Error fetching pod list")
		return
	}
	sourceFetches.WithLabelValues("get_pods").Inc()

	if pods == nil {
		log.Debug("Found no pods from source")
		return
	}
	log.WithFields(log.Fields{"pods": pods}).Debug("Found pod list from source")
	w.activePods.Set(pods)

	// get updated pods list
	var wg sync.WaitGroup
	for _, podName := range w.activePods.Get() {
		if w.doIgnorePod(podName) {
			log.WithFields(log.Fields{"pod": podName}).Debug("Pod matches ignorePod list, skipping")
			continue
		}
		wg.Add(1)
		go w.podMongodFetcher(podName, &wg)
		log.WithFields(log.Fields{"pod": podName}).Debug("Started pod fetcher")
	}
	wg.Wait()
	log.Debug("Completed all pod fetchers")
}

func (w *Watchdog) Run() {
	log.WithFields(log.Fields{
		"version": tools.Version,
		"service": w.config.ServiceName,
		"go":      runtime.Version(),
		"source":  w.podSource.Name(),
	}).Info("Starting watchdog")

	w.fetchPods()

	ticker := time.NewTicker(w.config.APIPoll)
	for {
		select {
		case <-ticker.C:
			w.fetchPods()
		case <-*w.quit:
			log.Info("Stopping watchers")
			ticker.Stop()
			w.watcherManager.Close()
			return
		}
	}
}
