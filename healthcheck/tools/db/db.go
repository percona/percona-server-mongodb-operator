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

package db

import (
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var (
	ErrMsgAuthFailedStr      string = "server returned error on SASL authentication step: Authentication failed."
	ErrNoReachableServersStr string = "no reachable servers"
)

func Dial(conf *Config) (mongo.Client, error) {
	log.WithFields(log.Fields{
		"hosts":      conf.Hosts,
		"ssl":        conf.SSL.Enabled,
		"ssl_secure": conf.SSL.Insecure,
	}).Debug("Connecting to mongodb")

	if err := conf.configureTLS(); err != nil {
		return nil, errors.Wrap(err, "configure TLS")
	}

	if conf.Username != "" && conf.Password != "" {
		log.WithFields(log.Fields{"user": conf.Username}).Debug("Enabling authentication for session")
	}

	client := mongo.New(&conf.Config)
	if err := client.Dial(); err != nil {
		conf.Config.Direct = true
		client := mongo.New(&conf.Config)
		if err := client.Dial(); err != nil {
			return nil, errors.Wrap(err, "connect to mongo using direct")
		}
	}

	return client, nil
}
