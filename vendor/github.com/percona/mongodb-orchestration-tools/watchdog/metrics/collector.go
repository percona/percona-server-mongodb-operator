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

package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const namespace = "watchdog"

type Collector struct {
	PodSourceErrorsTotal *prometheus.CounterVec
	PodSourceGetsTotal   *prometheus.CounterVec
}

func NewCollector() *Collector {
	return &Collector{
		PodSourceErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "pod_source",
			Name:      "errors_total",
			Help:      "The total number of errors from polling the watchdog pod source",
		}, []string{"source"}),
		PodSourceGetsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: namespace,
			Subsystem: "pod_source",
			Name:      "gets_total",
			Help:      "The total number of successful times the watchdog has polled a pod source",
		}, []string{"source"}),
	}
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.PodSourceErrorsTotal.Collect(ch)
	c.PodSourceGetsTotal.Collect(ch)
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	c.PodSourceErrorsTotal.Describe(ch)
	c.PodSourceGetsTotal.Describe(ch)
}
