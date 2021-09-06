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
	"time"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	mgo "go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"
)

var (
	ErrMsgAuthFailedStr      string = "server returned error on SASL authentication step: Authentication failed."
	ErrNoReachableServersStr string = "no reachable servers"
	ErrSessionTimeout               = errors.New("timed out waiting for mongodb connection")
	ErrPrimaryTimeout               = errors.New("timed out waiting for host to become primary")
)

func Dial(conf *Config) (*mgo.Client, error) {
	ctx, connectcancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer connectcancel()

	opts := options.Client().
		SetHosts(conf.Hosts).
		SetReplicaSet(conf.ReplSetName).
		SetAuth(options.Credential{
			Password: conf.Password,
			Username: conf.Username,
		}).
		SetWriteConcern(writeconcern.New(writeconcern.WMajority(), writeconcern.J(true))).
		SetReadPreference(readpref.Primary()).SetTLSConfig(conf.TLSConf)

	log.WithFields(log.Fields{
		"hosts":      conf.Hosts,
		"ssl":        conf.SSL.Enabled,
		"ssl_secure": conf.SSL.Insecure,
	}).Debug("Connecting to mongodb")

	if conf.Username != "" && conf.Password != "" {
		log.WithFields(log.Fields{"user": conf.Username}).Debug("Enabling authentication for session")
	}

	client, err := mgo.Connect(ctx, opts)
	if err != nil {
		return nil, errors.Wrap(err, "connect to mongo replica set")
	}

	return client, nil
}
