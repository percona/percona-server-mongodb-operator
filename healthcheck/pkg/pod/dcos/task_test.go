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
	"os"
	"testing"

	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/dcos"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg/pod"
	"github.com/stretchr/testify/assert"
)

func TestPkgPodDCOSTask(t *testing.T) {
	task := NewTask(&TaskData{
		Info: &TaskInfo{
			Name: t.Name(),
		},
	}, "testPod")
	assert.Implements(t, (*pod.Task)(nil), task)
	assert.Equal(t, t.Name(), task.Name())

	_, err := task.getEnvVar("not here")
	assert.Error(t, err)
}

func TestPkgPodDCOSTaskState(t *testing.T) {
	assert.Implements(t, (*pod.TaskState)(nil), TaskStateRunning)
	task := NewTask(&TaskData{
		Status: &TaskStatus{
			State: &TaskStateRunning,
		},
	}, "testPod")
	assert.NotNil(t, task.State())
	assert.Equal(t, string(TaskStateRunning), task.State().String())
	assert.True(t, task.IsRunning())

	emptyTask := &Task{data: &TaskData{}}
	assert.True(t, task.HasState())
	assert.False(t, emptyTask.HasState())
}

func TestPkgPodDCOSTaskIsTaskType(t *testing.T) {
	task := NewTask(&TaskData{
		Info: &TaskInfo{
			Name: "not a mongod",
			Command: &TaskCommand{
				Value: "mongodb-executor-linux",
			},
		},
	}, "testPod")
	assert.False(t, task.IsTaskType(pod.TaskTypeMongod))

	task.data.Info.Name = "mongodb-rs-mongod"
	assert.True(t, task.IsTaskType(pod.TaskTypeMongod))
}

func TestPkgPodDCOSTaskGetMongoAddr(t *testing.T) {
	os.Setenv(dcos.EnvFrameworkHost, "percona-server-mongodb.autoip.dcos.thisdcos.directory")
	defer os.Unsetenv(dcos.EnvFrameworkHost)

	task := NewTask(&TaskData{
		Info: &TaskInfo{
			Name: t.Name(),
			Command: &TaskCommand{
				Environment: &TaskCommandEnvironment{
					Variables: []*TaskCommandEnvironmentVariable{},
				},
			},
		},
	}, "testPod")
	_, err := task.GetMongoAddr()
	assert.Error(t, err)

	task.data.Info.Command.Environment.Variables = []*TaskCommandEnvironmentVariable{{
		Name:  pkg.EnvMongoDBPort,
		Value: "27017",
	}}
	addr, err := task.GetMongoAddr()
	assert.NoError(t, err)
	assert.Equal(t, t.Name()+".percona-server-mongodb.autoip.dcos.thisdcos.directory", addr.Host)
	assert.Equal(t, 27017, addr.Port)
}

func TestPkgPodDCOSTaskGetReplsetName(t *testing.T) {
	task := NewTask(&TaskData{
		Info: &TaskInfo{
			Command: &TaskCommand{
				Environment: &TaskCommandEnvironment{
					Variables: []*TaskCommandEnvironmentVariable{
						{
							Name:  pkg.EnvMongoDBReplset,
							Value: "rs",
						},
					},
				},
			},
		},
	}, "testPod")
	rsName, err := task.GetMongoReplsetName()
	assert.NoError(t, err)
	assert.Equal(t, "rs", rsName)
}
