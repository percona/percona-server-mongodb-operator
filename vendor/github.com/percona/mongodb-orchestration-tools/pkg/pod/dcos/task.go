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

package dcos

import (
	"errors"
	"strconv"
	"strings"

	"github.com/percona/mongodb-orchestration-tools/internal/dcos"
	"github.com/percona/mongodb-orchestration-tools/pkg/db"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
)

type TaskState string

var (
	AutoIPDNSSuffix   string    = "autoip.dcos.thisdcos.directory"
	TaskStateError    TaskState = "TASK_ERROR"
	TaskStateFailed   TaskState = "TASK_FAILED"
	TaskStateFinished TaskState = "TASK_FINISHED"
	TaskStateKilled   TaskState = "TASK_KILLED"
	TaskStateLost     TaskState = "TASK_LOST"
	TaskStateRunning  TaskState = "TASK_RUNNING"
	TaskStateUnknown  TaskState = "UNKNOWN"
)

func (s TaskState) String() string {
	return string(s)
}

type Task struct {
	frameworkName string
	data          *TaskData
}

type TaskData struct {
	Info   *TaskInfo   `json:"info"`
	Status *TaskStatus `json:"status"`
}

type TaskCommandEnvironmentVariable struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type TaskCommandEnvironment struct {
	Variables []*TaskCommandEnvironmentVariable `json:"variables"`
}

type TaskCommand struct {
	Environment *TaskCommandEnvironment `json:"environment"`
	Value       string                  `json:"value"`
}

type TaskInfo struct {
	Name    string       `json:"name"`
	Command *TaskCommand `json:"command"`
}

type TaskStatus struct {
	State *TaskState `json:"state"`
}

func NewTask(data *TaskData, frameworkName string) *Task {
	return &Task{data: data, frameworkName: frameworkName}
}

func (task *Task) getEnvVar(variableName string) (string, error) {
	if task.data.Info.Command != nil && task.data.Info.Command.Environment != nil {
		for _, variable := range task.data.Info.Command.Environment.Variables {
			if variable.Name == variableName {
				return variable.Value, nil
			}
		}
	}
	return "", errors.New("Could not find env variable: " + variableName)
}

func (task *Task) Name() string {
	return task.data.Info.Name
}

func (task *Task) HasState() bool {
	return task.data.Status != nil && task.data.Status.State != nil
}

func (task *Task) State() pod.TaskState {
	if task.HasState() {
		return *task.data.Status.State
	}
	return TaskStateUnknown
}

func (task *Task) IsRunning() bool {
	return task.State() == TaskStateRunning
}

func (task *Task) IsTaskType(taskType pod.TaskType) bool {
	if task.data.Info != nil && strings.HasSuffix(task.data.Info.Name, "-"+taskType.String()) {
		return strings.Contains(task.data.Info.Command.Value, "mongodb-executor")
	}
	return false
}

func (task *Task) GetMongoAddr() (*db.Addr, error) {
	addr := &db.Addr{
		Host: task.data.Info.Name + "." + task.frameworkName + "." + AutoIPDNSSuffix,
	}
	portStr, err := task.getEnvVar(dcos.EnvMongoDBPort)
	if err != nil {
		return addr, err
	}
	addr.Port, err = strconv.Atoi(portStr)
	return addr, err
}

func (task *Task) GetMongoReplsetName() (string, error) {
	return task.getEnvVar(dcos.EnvMongoDBReplset)
}
