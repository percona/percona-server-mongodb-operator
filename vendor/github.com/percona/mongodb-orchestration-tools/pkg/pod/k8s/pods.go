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
	"os"
	"sync"

	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	corev1 "k8s.io/api/core/v1"
)

const (
	EnvKubernetesHost = "KUBERNETES_SERVICE_HOST"
	EnvKubernetesPort = "KUBERNETES_SERVICE_PORT"
)

func NewPods(serviceName, namespace, portName string) *Pods {
	return &Pods{
		namespace:   namespace,
		serviceName: serviceName,
		portName:    portName,
		pods:        make([]corev1.Pod, 0),
	}
}

type Pods struct {
	sync.Mutex
	namespace   string
	serviceName string
	portName    string
	pods        []corev1.Pod
}

func (p *Pods) Name() string {
	return "k8s"
}

func (p *Pods) URL() string {
	host := os.Getenv(EnvKubernetesHost)
	port := os.Getenv(EnvKubernetesPort)
	if host == "" || port == "" {
		return ""
	}
	return "tcp://" + host + ":" + port
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
		if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
			continue
		}
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
