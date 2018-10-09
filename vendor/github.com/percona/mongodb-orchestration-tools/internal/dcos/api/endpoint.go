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

// Endpoints contains several DC/OS SDK Service endpoints
type Endpoints []string

// Endpoint represents a DC/OS SDK Service endpoint
type Endpoint struct {
	Address []string `json:"address"`
	Dns     []string `json:"dns"`
}

// Addresses returns a list of IP:Port addresses for the DC/OS SDK Service endpoint
func (e *Endpoint) Addresses() []string {
	return e.Address
}

// Hosts returns a list of DNS:Port addresses for the DC/OS SDK Service endpoint
func (e *Endpoint) Hosts() []string {
	return e.Dns
}

func (c *SDKClient) getEndpointsURL() string {
	return c.scheme.String() + c.config.Host + "/" + SDKAPIVersion + "/endpoints"
}

// GetEndpoints returns a slice of DC/OS SDK Service Endpoints
func (c *SDKClient) Endpoints() ([]string, error) {
	endpoints := []string{}
	err := c.get(c.getEndpointsURL(), &endpoints)
	return endpoints, err
}

// GetEndpoint returns an DC/OS SDK Service Endpoint by name
func (c *SDKClient) GetEndpoint(endpointName string) (*Endpoint, error) {
	endpointURL := c.getEndpointsURL() + "/" + endpointName
	endpoint := &Endpoint{}
	err := c.get(endpointURL, endpoint)
	return endpoint, err
}
