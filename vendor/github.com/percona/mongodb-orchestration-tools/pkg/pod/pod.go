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

type Pods struct {
	sync.Mutex
	pods []string
}

func NewPods() *Pods {
	return &Pods{}
}

func (p *Pods) Get() []string {
	p.Lock()
	defer p.Unlock()
	return p.pods
}

func (p *Pods) Set(pods []string) {
	p.Lock()
	defer p.Unlock()
	p.pods = pods
}

func (p *Pods) Has(name string) bool {
	p.Lock()
	defer p.Unlock()
	for _, podName := range p.pods {
		if name == podName {
			return true
		}
	}
	return false
}

type Source interface {
	Name() string
	URL() string
	Pods() ([]string, error)
	GetTasks(podName string) ([]Task, error)
}
