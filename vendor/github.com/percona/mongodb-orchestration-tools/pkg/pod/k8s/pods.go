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
	"os"
	"sync"

	"github.com/percona/mongodb-orchestration-tools/pkg"
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	EnvKubernetesHost = "KUBERNETES_SERVICE_HOST"
	EnvKubernetesPort = "KUBERNETES_SERVICE_PORT"
)

func NewPods(serviceName, namespace string) *Pods {
	return &Pods{
		namespace:    namespace,
		serviceName:  serviceName,
		pods:         make([]corev1.Pod, 0),
		statefulsets: make([]appsv1.StatefulSet, 0),
	}
}

type Pods struct {
	sync.Mutex
	namespace    string
	serviceName  string
	pods         []corev1.Pod
	statefulsets []appsv1.StatefulSet
}

func getPodReplsetName(pod *corev1.Pod) (string, error) {
	for _, container := range pod.Spec.Containers {
		for _, env := range container.Env {
			if env.Name == pkg.EnvMongoDBReplset {
				return env.Value, nil
			}
		}
	}
	return "", errors.New("could not find mongodb replset name")
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

func (p *Pods) Update(pods []corev1.Pod, statefulsets []appsv1.StatefulSet) {
	p.Lock()
	defer p.Unlock()
	p.pods = pods
	p.statefulsets = statefulsets
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

func (p *Pods) getStatefulSetFromPod(pod *corev1.Pod) *appsv1.StatefulSet {
	replsetName, err := getPodReplsetName(pod)
	if err != nil {
		return nil
	}
	setServiceName := p.serviceName + "-" + replsetName
	for i, statefulset := range p.statefulsets {
		if statefulset.Spec.ServiceName != setServiceName {
			continue
		}
		return &p.statefulsets[i]
	}
	return nil
}

func (p *Pods) GetTasks(podName string) ([]pod.Task, error) {
	p.Lock()
	defer p.Unlock()

	tasks := make([]pod.Task, 0)
	for _, pod := range p.pods {
		if pod.Name != podName {
			continue
		}
		statefulset := p.getStatefulSetFromPod(&pod)
		if statefulset == nil {
			return tasks, errors.New("cannot find statefulset for pod")
		}
		tasks = append(
			tasks,
			NewTask(&pod, statefulset, p.serviceName, p.namespace),
		)
	}
	return tasks, nil
}
