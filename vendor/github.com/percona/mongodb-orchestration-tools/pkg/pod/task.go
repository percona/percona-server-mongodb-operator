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

package pod

import (
	"github.com/percona/mongodb-orchestration-tools/pkg/db"
)

type TaskType string

var (
	TaskTypeMongod       TaskType = "mongod"
	TaskTypeMongodBackup TaskType = "mongod-backup"
	TaskTypeArbiter      TaskType = "arbiter"
	TaskTypeConfigSvr    TaskType = "configsvr"
	TaskTypeMongos       TaskType = "mongos"
)

func (t TaskType) String() string {
	return string(t)
}

type TaskState interface {
	String() string
}

type Task interface {
	Name() string
	State() TaskState
	HasState() bool
	IsRunning() bool
	IsUpdating() bool
	IsTaskType(taskType TaskType) bool
	GetMongoAddr() (*db.Addr, error)
	GetMongoReplsetName() (string, error)
}
