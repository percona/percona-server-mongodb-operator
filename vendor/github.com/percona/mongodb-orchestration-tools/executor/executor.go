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

package executor

import (
	"github.com/percona/mongodb-orchestration-tools/executor/config"
	log "github.com/sirupsen/logrus"
)

type Executor struct {
	config *config.Config
	quit   *chan bool
}

func New(config *config.Config, quit *chan bool) *Executor {
	return &Executor{
		config: config,
		quit:   quit,
	}
}

func (e *Executor) Run(daemon Daemon) error {
	log.Infof("Running %s daemon", e.config.NodeType)
	err := daemon.Start()
	if err != nil {
		daemon.Kill()
		return err
	}
	return nil
}
