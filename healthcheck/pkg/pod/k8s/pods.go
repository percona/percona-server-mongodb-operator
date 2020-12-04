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

	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg/pod"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

const (
	EnvKubernetesHost = "KUBERNETES_SERVICE_HOST"
	EnvKubernetesPort = "KUBERNETES_SERVICE_PORT"
)

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

// CustomResourceState represents the state of a single
// Kubernetes CR for PSMDB
type CustomResourceState struct {
	Name           string
	Pods           []corev1.Pod
	Services       []corev1.Service
	ServicesExpose bool
	Statefulsets   []appsv1.StatefulSet
}

func (cr *CustomResourceState) getServiceFromPod(pod *corev1.Pod) *corev1.Service {
	serviceName := pod.Name
	for i, svc := range cr.Services {
		if svc.Name != serviceName {
			continue
		}
		return &cr.Services[i]
	}
	return nil
}

func (cr *CustomResourceState) getStatefulSetFromPod(pod *corev1.Pod) *appsv1.StatefulSet {
	replsetName, err := getPodReplsetName(pod)
	if err != nil {
		return nil
	}
	setServiceName := cr.Name + "-" + replsetName
	for i, statefulset := range cr.Statefulsets {
		if statefulset.Spec.ServiceName != setServiceName {
			continue
		}
		return &cr.Statefulsets[i]
	}
	return nil
}

func NewPods(namespace string) *Pods {
	return &Pods{
		namespace: namespace,
		crs:       make(map[string]*CustomResourceState),
	}
}

type Pods struct {
	sync.Mutex
	namespace string
	crs       map[string]*CustomResourceState
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

// Delete deletes the state of a single PSMDB CR
func (p *Pods) Delete(crState *CustomResourceState) {
	p.Lock()
	defer p.Unlock()
	delete(p.crs, crState.Name)
}

// Update updates the state of a single PSMDB CR
func (p *Pods) Update(crState *CustomResourceState) {
	p.Lock()
	defer p.Unlock()
	p.crs[crState.Name] = crState
}

// Pods returns all available pod names
func (p *Pods) Pods() ([]string, error) {
	p.Lock()
	defer p.Unlock()

	pods := make([]string, 0)
	for _, cr := range p.crs {
		for _, pod := range cr.Pods {
			if pod.Status.Phase != corev1.PodRunning && pod.Status.Phase != corev1.PodPending {
				continue
			}
			pods = append(pods, pod.Name)
		}
	}
	return pods, nil
}

// GetTasks returns tasks for a single pod by name
func (p *Pods) GetTasks(podName string) ([]pod.Task, error) {
	p.Lock()
	defer p.Unlock()

	tasks := make([]pod.Task, 0)
	for _, cr := range p.crs {
		for i := range cr.Pods {
			pod := &cr.Pods[i]
			if pod.Name != podName {
				continue
			}
			tasks = append(tasks, NewTask(p.namespace, cr, pod))
		}
	}
	return tasks, nil
}
