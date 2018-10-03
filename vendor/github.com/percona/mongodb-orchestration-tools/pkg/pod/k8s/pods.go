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
	"github.com/percona/mongodb-orchestration-tools/pkg/pod"
)

type Pods struct{}

func (p *Pods) Name() string {
	return "k8s"
}

func (p *Pods) GetPodURL() string {
	return "operator-sdk"
}

func (p *Pods) GetPods() (*pod.Pods, error) {
	return &pod.Pods{}, nil
}

func (p *Pods) GetPodTasks(podName string) ([]pod.Task, error) {
	return []pod.Task{}, nil
}
