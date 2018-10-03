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

package k8s

import (
	"github.com/percona/mongodb-orchestration-tools/pkg/db"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
)

type TaskState string

var (
	TaskStateRunning TaskState = "running"
	TaskStateStopped TaskState = "stopped"
)

func (s TaskState) String() string {
	return string(s)
}

type Task struct {
	state *TaskState
}

func (task *Task) State() pod.TaskState {
	return task.state
}

func (task *Task) HasState() bool {
	return false
}

func (task *Task) Name() string {
	return "test"
}

func (task *Task) IsRunning() bool {
	return false
}

func (task *Task) IsTaskType(taskType pod.TaskType) bool {
	return false
}

func (task *Task) GetMongoAddr() (*db.Addr, error) {
	addr := &db.Addr{}
	return addr, nil
}

func (task *Task) GetMongoReplsetName() (string, error) {
	return "rs", nil
}
