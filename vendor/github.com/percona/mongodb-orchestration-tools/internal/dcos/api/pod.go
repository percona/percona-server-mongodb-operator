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

package api

import (
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod/dcos"
)

// GetPodURL returns a string representing the full HTTP URI to the 'GET /<version>/pod' API call
func (c *SDKClient) GetPodURL() string {
	return c.scheme.String() + c.config.Host + "/" + SDKAPIVersion + "/pod"
}

// GetPods returns a slice of existing Pods in the DC/OS SDK
func (c *SDKClient) GetPods() (*pod.Pods, error) {
	pods := &pod.Pods{}
	err := c.get(c.GetPodURL(), pods)
	return pods, err
}

// GetPodTasks returns a slice of PodTask for a given DC/OS SDK Pod by name
func (c *SDKClient) GetPodTasks(podName string) ([]pod.Task, error) {
	tasks := make([]pod.Task, 0)
	tasksData := make([]*dcos.TaskData, 0)
	podURL := c.GetPodURL() + "/" + podName + "/info"
	err := c.get(podURL, &tasksData)
	if err != nil {
		return tasks, err
	}
	for _, taskData := range tasksData {
		tasks = append(tasks, dcos.NewTask(taskData, c.ServiceName))
	}
	return tasks, nil
}
