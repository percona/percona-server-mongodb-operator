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

package replset

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/percona/mongodb-orchestration-tools/controller"
	"github.com/percona/mongodb-orchestration-tools/controller/user"
	"github.com/percona/mongodb-orchestration-tools/internal/db"
	log "github.com/sirupsen/logrus"
	rsConfig "github.com/timvaillancourt/go-mongodb-replset/config"
	"gopkg.in/mgo.v2"
)

const (
	ErrMsgDNSNotReady         = "No host described in new configuration 1 for replica set rs maps to this node"
	ErrMsgNotAuthorizedPrefix = "not authorized on admin to execute command"
	ErrMsgNotPrimary          = "not primary"
	ErrMsgRsInitRequiresAuth  = "command replSetInitiate requires authentication"
)

var (
	ErrCannotInitReplset = errors.New("could not init replset")
)

type Initiator struct {
	config        *controller.Config
	replInitTries uint
}

func NewInitiator(config *controller.Config) *Initiator {
	return &Initiator{
		config: config,
	}
}

func isError(err error, prefix string) bool {
	if err != nil {
		return strings.HasPrefix(err.Error(), prefix)
	}
	return false
}

func (i *Initiator) initReplset(rsCnfMan rsConfig.Manager, out io.Writer) error {
	config := rsConfig.NewConfig(i.config.Replset)
	member := rsConfig.NewMember(i.config.ReplsetInit.PrimaryAddr)
	member.Tags = &rsConfig.ReplsetTags{
		"serviceName": i.config.ServiceName,
	}
	config.AddMember(member)
	rsCnfMan.Set(config)

	log.Info("Initiating replset")
	fmt.Fprintln(out, config)

	for i.replInitTries < i.config.ReplsetInit.MaxReplTries {
		err := rsCnfMan.Initiate()
		if err == nil {
			log.WithFields(log.Fields{
				"version": config.Version,
			}).Info("Initiated replset with config:")
			return nil
		}
		if isError(err, ErrMsgRsInitRequiresAuth) || isError(err, ErrMsgNotAuthorizedPrefix) || isError(err, ErrMsgDNSNotReady) {
			return err
		}
		log.WithFields(log.Fields{
			"replset": i.config.Replset,
			"error":   err,
		}).Error("Error initiating replset! Retrying")
		time.Sleep(i.config.ReplsetInit.RetrySleep)
		i.replInitTries += 1
	}
	if i.replInitTries >= i.config.ReplsetInit.MaxReplTries {
		return ErrCannotInitReplset
	}

	return nil
}

func (i *Initiator) initAdminUser(session *mgo.Session) error {
	err := user.UpdateUser(session, user.UserAdmin, "admin")
	if err != nil && !isError(err, ErrMsgNotAuthorizedPrefix) {
		return err
	}
	return nil
}

func (i *Initiator) initUsers(session *mgo.Session) error {
	systemUsers := user.SystemUsers()
	if len(systemUsers) > 0 {
		err := user.UpdateUsers(session, systemUsers, "admin")
		if err != nil {
			return err
		}
	}
	return nil
}

func (i *Initiator) getLocalhostNoAuthSession() (*mgo.Session, error) {
	// if enabled, use an insecure SSL connection to avoid hostname validation error
	// for the server hostname, only for the first connection.
	sslCnfInsecure := db.SSLConfig{}
	if i.config.SSL != nil {
		sslCnfInsecure = *i.config.SSL
		sslCnfInsecure.Insecure = true
	}

	split := strings.SplitN(i.config.ReplsetInit.PrimaryAddr, ":", 2)
	localhostHost := "localhost:" + split[1]
	session, err := db.WaitForSession(
		&db.Config{
			DialInfo: &mgo.DialInfo{
				Addrs:    []string{localhostHost},
				Direct:   true,
				FailFast: true,
				Timeout:  db.DefaultMongoDBTimeoutDuration,
			},
			SSL: &sslCnfInsecure,
		},
		i.config.ReplsetInit.MaxConnectTries,
		i.config.ReplsetInit.RetrySleep,
	)
	if err != nil {
		return nil, err
	}

	log.WithFields(log.Fields{
		"host":       localhostHost,
		"auth":       false,
		"replset":    "",
		"ssl":        sslCnfInsecure.Enabled,
		"ssl_secure": !sslCnfInsecure.Insecure,
	}).Info("Connected to MongoDB")

	return session, nil
}

func (i *Initiator) getReplsetSession() (*mgo.Session, error) {
	session, err := db.WaitForSession(
		&db.Config{
			DialInfo: &mgo.DialInfo{
				Addrs:          []string{i.config.ReplsetInit.PrimaryAddr},
				Username:       i.config.UserAdminUser,
				Password:       i.config.UserAdminPassword,
				ReplicaSetName: i.config.Replset,
				Direct:         true,
				FailFast:       true,
				Timeout:        db.DefaultMongoDBTimeoutDuration,
			},
			SSL: i.config.SSL,
		},
		i.config.ReplsetInit.MaxConnectTries,
		i.config.ReplsetInit.RetrySleep,
	)
	if err != nil {
		return nil, err
	}

	log.WithFields(log.Fields{
		"host":       i.config.ReplsetInit.PrimaryAddr,
		"auth":       true,
		"replset":    i.config.Replset,
		"ssl":        i.config.SSL.Enabled,
		"ssl_secure": !i.config.SSL.Insecure,
	}).Info("Connected to MongoDB")

	return session, nil
}

func (i *Initiator) prepareReplset(session *mgo.Session, out io.Writer) error {
	err := i.initReplset(rsConfig.New(session), out)
	if err != nil {
		log.WithError(err).Error("Error intiating replica set")
		return err
	} else {
		log.Info("Waiting for host to become primary")
		err = db.WaitForPrimary(session, i.config.ReplsetInit.MaxConnectTries, i.config.ReplsetInit.RetrySleep)
		if err != nil {
			log.WithError(err).Error("Error getting waiting for primary session")
			return err
		}
	}

	err = i.initAdminUser(session)
	if err != nil {
		return err
	}
	return nil
}

func (i *Initiator) Run() error {
	log.WithFields(log.Fields{
		"service": i.config.ServiceName,
	}).Info("Mongod replset initiator started")

	log.WithFields(log.Fields{
		"sleep": i.config.ReplsetInit.Delay,
	}).Info("Waiting to start initiation")
	time.Sleep(i.config.ReplsetInit.Delay)

	// First we must use a localhost, no-authentication session
	// so that we can use the MongoDB Localhost Exception:
	// https://docs.mongodb.com/manual/core/security-users/#localhost-exception
	localhostNoAuthSession, err := i.getLocalhostNoAuthSession()
	if err != nil {
		log.WithError(err).Error("Error getting localhost no-auth session")
		return err
	}
	defer localhostNoAuthSession.Close()

	err = i.prepareReplset(localhostNoAuthSession, os.Stdout)
	if err != nil {
		if isError(err, ErrMsgNotAuthorizedPrefix) || isError(err, ErrMsgNotPrimary) || isError(err, ErrMsgRsInitRequiresAuth) {
			log.Warning("Replset already initiated, skipping initiation")
			return nil
		}
		log.WithError(err).Error("Error preparing replset")
		return err
	}

	log.Info("Closing localhost connection, reconnecting with a replset+auth connection")
	localhostNoAuthSession.Close()

	replsetAuthSession, err := i.getReplsetSession()
	if err != nil {
		log.WithError(err).Error("Error getting replica set session")
		return err
	}
	defer replsetAuthSession.Close()

	err = i.initUsers(replsetAuthSession)
	if err != nil {
		log.WithError(err).Error("Error adding system users")
		return err
	}

	log.Info("Mongod replset initiator complete")
	return nil
}
