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

import "sync"

type Pods []string

func (p Pods) HasPod(name string) bool {
	for _, pod := range p {
		if pod == name {
			return true
		}
	}
	return false
}

type ActivePods struct {
	sync.Mutex
	pods *Pods
}

func NewActivePods() *ActivePods {
	return &ActivePods{pods: &Pods{}}
}

func (p *ActivePods) Get() *Pods {
	p.Lock()
	defer p.Unlock()
	return p.pods
}

func (p *ActivePods) Set(pods *Pods) {
	p.Lock()
	defer p.Unlock()
	p.pods = pods
}

func (p *ActivePods) Has(name string) bool {
	p.Lock()
	defer p.Unlock()
	return p.pods.HasPod(name)
}

type Source interface {
	Name() string
	GetPodURL() string
	GetPods() (*Pods, error)
	GetPodTasks(podName string) ([]Task, error)
}
