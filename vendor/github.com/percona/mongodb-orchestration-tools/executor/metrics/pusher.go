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
	mgostatsd "github.com/scullxbones/mgo-statsd"
	"gopkg.in/mgo.v2"
)

// StatsdPusher is a metrics pusher to Statsd
type StatsdPusher struct {
	statsdConfig mgostatsd.Statsd
	verbose      bool
}

// NewStatsdPusher returns a new StatsdPusher
func NewStatsdPusher(statsdCnf mgostatsd.Statsd, verbose bool) *StatsdPusher {
	return &StatsdPusher{
		statsdConfig: statsdCnf,
		verbose:      verbose,
	}
}

// GetServerStatus returns a *mgostatsd.ServerStatus
func (p *StatsdPusher) GetServerStatus(session *mgo.Session) (*mgostatsd.ServerStatus, error) {
	return mgostatsd.GetServerStatus(session)
}

// PushStats pushes metrics to Statsd
func (p *StatsdPusher) Push(status *mgostatsd.ServerStatus) error {
	return mgostatsd.PushStats(p.statsdConfig, status, p.verbose)
}
