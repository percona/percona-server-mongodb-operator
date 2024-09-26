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
	"context"

	"github.com/pkg/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var (
	ErrMsgAuthFailedStr      = "server returned error on SASL authentication step: Authentication failed."
	ErrNoReachableServersStr = "no reachable servers"
)

func Dial(ctx context.Context, conf *Config) (mongo.Client, error) {
	if err := conf.configureTLS(); err != nil {
		return nil, errors.Wrap(err, "configure TLS")
	}

	log := logf.FromContext(ctx)
	log.V(1).Info("Connecting to mongodb", "hosts", conf.Hosts, "ssl", conf.SSL.Enabled, "ssl_insecure", conf.SSL.Insecure)

	if conf.Username != "" && conf.Password != "" {
		log.V(1).Info("Enabling authentication for session", "user", conf.Username)
	}

	cl, err := mongo.Dial(&conf.Config)
	if err != nil {
		cfg := conf.Config
		cfg.Direct = true
		cfg.ReplSetName = ""
		cl, err = mongo.Dial(&cfg)
		if err != nil {
			return nil, errors.Wrap(err, "filed to dial mongo")
		}
	}

	return cl, nil
}
