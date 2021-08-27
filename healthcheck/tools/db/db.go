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
	"errors"
	"net/url"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

var (
	ErrMsgAuthFailedStr      string = "server returned error on SASL authentication step: Authentication failed."
	ErrNoReachableServersStr string = "no reachable servers"
	ErrSessionTimeout               = errors.New("timed out waiting for mongodb connection")
	ErrPrimaryTimeout               = errors.New("timed out waiting for host to become primary")
)

func GetSession(cnf *Config) (*mgo.Session, error) {
	cnf.DialInfo.Password = url.QueryEscape(cnf.DialInfo.Password)

	if cnf.SSL == nil {
		cnf.SSL = &SSLConfig{}
	}

	log.WithFields(log.Fields{
		"hosts":      cnf.DialInfo.Addrs,
		"ssl":        cnf.SSL.Enabled,
		"ssl_secure": !cnf.SSL.Insecure,
	}).Debug("Connecting to mongodb")

	if cnf.DialInfo.Username != "" && cnf.DialInfo.Password != "" {
		log.WithFields(log.Fields{
			"user":   cnf.DialInfo.Username,
			"source": cnf.DialInfo.Source,
		}).Debug("Enabling authentication for session")
	}

	if cnf.SSL.Enabled {
		err := cnf.configureSSLDialInfo()
		if err != nil {
			log.Errorf("failed to configure SSL/TLS: %s", err)
			return nil, err
		}
	}

	session, err := mgo.DialWithInfo(cnf.DialInfo)
	if err != nil {
		switch err.Error() {
		case ErrMsgAuthFailedStr:
			log.Debug("authentication failed, retrying with authentication disabled")
			cnf.DialInfo.Username = ""
			cnf.DialInfo.Password = ""
			session, err = mgo.DialWithInfo(cnf.DialInfo)
		case ErrNoReachableServersStr:
			log.Debug("connection failed, retrying without replSet")
			cnf.DialInfo.ReplicaSetName = ""
			// if replset is not yet initialized, we won't have users either
			cnf.DialInfo.Username = ""
			cnf.DialInfo.Password = ""
			session, err = mgo.DialWithInfo(cnf.DialInfo)
		}
	}

	if err != nil {
		return nil, err
	}

	session.SetMode(mgo.Monotonic, true)
	return session, err
}

func WaitForSession(cnf *Config, maxRetries uint, sleepDuration time.Duration) (*mgo.Session, error) {
	var err error
	var tries uint
	for tries <= maxRetries || maxRetries == 0 {
		session, err := GetSession(cnf)
		if err == nil && session.Ping() == nil {
			return session, err
		}
		time.Sleep(sleepDuration)
		tries++
	}
	if err == nil {
		return nil, ErrSessionTimeout
	}
	return nil, err
}

func WaitForPrimary(session *mgo.Session, maxRetries uint, sleepDuration time.Duration) error {
	resp := struct {
		IsMaster bool `bson:"ismaster"`
		ReadOnly bool `bson:"readOnly"`
	}{}
	var err error
	var tries uint
	for tries <= maxRetries {
		err = session.Run(bson.D{{Name: "isMaster", Value: "1"}}, &resp)
		if err == nil && resp.IsMaster && !resp.ReadOnly {
			return nil
		}
		time.Sleep(sleepDuration)
		tries++
	}
	if err == nil {
		return ErrPrimaryTimeout
	}
	return err
}
