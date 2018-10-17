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
	"sync"

	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	corev1 "k8s.io/api/core/v1"
)

func NewPods(serviceName, namespace, portName string) *Pods {
	return &Pods{
		pods:        make([]corev1.Pod, 0),
		namespace:   namespace,
		portName:    portName,
		serviceName: serviceName,
	}
}

type Pods struct {
	sync.Mutex
	namespace   string
	pods        []corev1.Pod
	portName    string
	serviceName string
}

func (p *Pods) Name() string {
	return "k8s"
}

func (p *Pods) URL() string {
	return "operator-sdk"
}

func (p *Pods) SetPods(pods []corev1.Pod) {
	p.Lock()
	defer p.Unlock()
	p.pods = pods
}

func (p *Pods) Pods() ([]string, error) {
	p.Lock()
	defer p.Unlock()

	pods := []string{}
	for _, pod := range p.pods {
		pods = append(pods, pod.Name)
	}
	return pods, nil
}

func (p *Pods) GetTasks(podName string) ([]pod.Task, error) {
	p.Lock()
	defer p.Unlock()

	tasks := make([]pod.Task, 0)
	for _, pod := range p.pods {
		if pod.Name != podName {
			continue
		}
		tasks = append(tasks, NewTask(pod, p.serviceName, p.namespace, p.portName))
	}
	return tasks, nil
}
