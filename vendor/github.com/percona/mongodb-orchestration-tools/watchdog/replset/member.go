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
	"strconv"
	"strings"

	"github.com/percona/mongodb-orchestration-tools/internal/db"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	"gopkg.in/mgo.v2"
)

const (
	backupPodNamePrefix = "backup-"
)

type Mongod struct {
	Host          string
	Port          int
	Replset       string
	FrameworkName string
	PodName       string
	Task          pod.Task
}

func NewMongod(task pod.Task, frameworkName string, podName string) (*Mongod, error) {
	addr, err := task.GetMongoAddr()
	if err != nil {
		return nil, err
	}

	mongod := &Mongod{
		FrameworkName: frameworkName,
		PodName:       podName,
		Task:          task,
		Host:          addr.Host,
		Port:          addr.Port,
	}

	mongod.Replset, err = task.GetMongoReplsetName()
	if err != nil {
		return mongod, err
	}

	return mongod, err
}

// Name returns a string representing the host and port of a mongod instance
func (m *Mongod) Name() string {
	return m.Host + ":" + strconv.Itoa(m.Port)
}

// IsBackupNode returns a boolean reflecting whether or not the mongod instance is a backup node
func (m *Mongod) IsBackupNode() bool {
	return strings.HasPrefix(m.PodName, backupPodNamePrefix)
}

// DBConfig returns a direct database connection to a single mongod instance
func (m *Mongod) DBConfig(sslCnf *db.SSLConfig) *db.Config {
	return &db.Config{
		DialInfo: &mgo.DialInfo{
			Addrs:    []string{m.Host + ":" + strconv.Itoa(m.Port)},
			Direct:   true,
			FailFast: true,
			Timeout:  db.DefaultMongoDBTimeoutDuration,
		},
		SSL: sslCnf,
	}
}
