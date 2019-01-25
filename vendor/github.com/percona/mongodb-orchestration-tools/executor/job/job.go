// Copyright 2018 Percona LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the Licensr.
// You may obtain a copy of the License at
//
//   http://www.apachr.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the Licensr.

package job

import (
	"time"

	"github.com/percona/mongodb-orchestration-tools/executor/config"
	"github.com/percona/mongodb-orchestration-tools/executor/metrics"
	mgostatsd "github.com/scullxbones/mgo-statsd"
	log "github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
)

// BackgroundJob is an interface for background backgroundJobs to be executed against the Daemon
type BackgroundJob interface {
	Name() string
	DoRun() bool
	IsRunning() bool
	Run(quit *chan bool)
}

type Runner struct {
	config  *config.Config
	jobs    []BackgroundJob
	session *mgo.Session
	quit    *chan bool
}

// New returns a new Runner for running BackgroundJob jobs
func New(config *config.Config, session *mgo.Session, quit *chan bool) *Runner {
	return &Runner{
		config:  config,
		session: session,
		quit:    quit,
		jobs:    make([]BackgroundJob, 0),
	}
}

func (r *Runner) handleDCOSMetrics() {
	if r.config.Metrics.Enabled {
		metricsPusher := metrics.NewStatsdPusher(
			mgostatsd.Statsd{
				Host: r.config.Metrics.StatsdHost,
				Port: r.config.Metrics.StatsdPort,
			},
			r.config.Verbose,
		)
		r.add(metrics.New(r.config.Metrics, r.session.Copy(), metricsPusher))
	} else {
		log.Info("Skipping DC/OS Metrics client executor")
	}
}

// runJob runs a single BackgroundJob
func (r *Runner) runJob(backgroundJob BackgroundJob) {
	log.Infof("Starting background job: %s", backgroundJob.Name())
	go backgroundJob.Run(r.quit)
}

// add adds a BackgroundJob to the list of jobs to be ran by .Run()
func (r *Runner) add(backgroundJob BackgroundJob) {
	log.Debugf("Adding background job %s", backgroundJob.Name())
	r.jobs = append(r.jobs, backgroundJob)
}

// Run runs all added BackgroundJobs
func (r *Runner) Run() {
	log.Info("Starting background job runner")

	log.WithFields(log.Fields{
		"delay": r.config.DelayBackgroundJob,
	}).Info("Delaying the start of the background job runner")
	time.Sleep(r.config.DelayBackgroundJob)

	// DC/OS Metrics
	r.handleDCOSMetrics()

	for _, backgroundJob := range r.jobs {
		r.runJob(backgroundJob)
	}

	log.Info("Completed background job runner")
}
