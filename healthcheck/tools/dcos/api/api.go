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

package api

import (
	"time"

	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg/pod"
)

// HTTPScheme is the scheme type to be used for HTTP calls
type HTTPScheme string

const (
	HTTPSchemePlain  HTTPScheme = "http://"
	HTTPSchemeSecure HTTPScheme = "https://"
)

// String returns a string representation of the HTTPScheme
func (s HTTPScheme) String() string {
	return string(s)
}

// Config is a struct of configuration options for the API
type Config struct {
	Host    string
	Timeout time.Duration
	Secure  bool
}

// Client is an interface describing a DC/OS SDK API Client
type Client interface {
	Name() string
	URL() string
	Pods() ([]string, error)
	GetTasks(podName string) ([]pod.Task, error)
	Endpoints() ([]string, error)
	GetEndpoint(endpointName string) (*Endpoint, error)
}
