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
	"errors"
	"strings"

	"github.com/percona/mongodb-orchestration-tools/pkg/db"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	corev1 "k8s.io/api/core/v1"
)

const (
	MongodContainerName     string = "mongod"
	ClusterServiceDNSSuffix string = "svc.cluster.local"
)

func GetMongoHost(pod, service, namespace string) string {
	return pod + "." + service + "." + namespace + "." + ClusterServiceDNSSuffix
}

type TaskState struct {
	status corev1.PodStatus
}

func NewTaskState(status corev1.PodStatus) *TaskState {
	return &TaskState{status}
}

func (ts TaskState) String() string {
	return strings.ToUpper(string(ts.status.Phase))
}

type Task struct {
	pod         *corev1.Pod
	portName    string
	serviceName string
	namespace   string
}

func NewTask(pod corev1.Pod, serviceName, namespace, portName string) *Task {
	return &Task{
		pod:         &pod,
		namespace:   namespace,
		portName:    portName,
		serviceName: serviceName,
	}
}

func (t *Task) State() pod.TaskState {
	return NewTaskState(t.pod.Status)
}

func (t *Task) HasState() bool {
	return t.pod.Status.Phase != ""
}

func (t *Task) Name() string {
	return t.pod.Name
}

func (t *Task) IsRunning() bool {
	if t.pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, container := range t.pod.Status.ContainerStatuses {
		if container.State.Running == nil {
			return false
		}
	}
	return true
}

func (t *Task) IsTaskType(taskType pod.TaskType) bool {
	switch taskType {
	case pod.TaskTypeMongod:
		for _, container := range t.pod.Spec.Containers {
			if container.Name == MongodContainerName {
				return true
			}
		}
	}
	return false
}

func (t *Task) GetMongoAddr() (*db.Addr, error) {
	for _, container := range t.pod.Spec.Containers {
		for _, port := range container.Ports {
			if port.Name != t.portName {
				continue
			}
			addr := &db.Addr{
				Host: GetMongoHost(t.pod.Name, t.serviceName, t.namespace),
				Port: int(port.HostPort),
			}
			if addr.Port == 0 {
				addr.Port = int(port.ContainerPort)
			}
			return addr, nil
		}
	}
	return nil, errors.New("could not find mongodb address")
}

func (t *Task) GetMongoReplsetName() (string, error) {
	for _, container := range t.pod.Spec.Containers {
		for _, env := range container.Env {
			if env.Name == "MONGODB_REPLSET" {
				return env.Value, nil
			}
		}
	}
	return "", errors.New("could not find mongodb replset name")
}
