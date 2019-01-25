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

package user

import (
	"errors"
	"time"

	"github.com/percona/mongodb-orchestration-tools/controller"
	user_json "github.com/percona/mongodb-orchestration-tools/controller/user/json"
	"github.com/percona/mongodb-orchestration-tools/internal/db"
	"github.com/percona/mongodb-orchestration-tools/internal/dcos/api"
	log "github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
)

var (
	ErrCannotChgSysUser = errors.New("cannot change system user")
	ErrNoDbProvided     = errors.New("no db/database provided")
	ErrNoUserProvided   = errors.New("no username provided")
	ErrNoPasswdProvided = errors.New("no new password provided")
	ErrUserNotFound     = errors.New("could not find user")
)

type Controller struct {
	api             api.Client
	dbConfig        *db.Config
	session         *mgo.Session
	config          *controller.Config
	maxConnectTries uint
	retrySleep      time.Duration
}

func NewController(config *controller.Config, client api.Client) (*Controller, error) {
	var err error
	uc := &Controller{
		api:             client,
		config:          config,
		maxConnectTries: config.User.MaxConnectTries,
		retrySleep:      config.User.RetrySleep,
	}
	uc.dbConfig, err = uc.getDBConfig()
	if err != nil {
		return nil, err
	}
	uc.session, err = uc.getSession()
	if err != nil {
		return nil, err
	}
	return uc, nil
}

func (uc *Controller) getDBConfig() (*db.Config, error) {
	log.Infof("Gathering MongoDB seed list from endpoint %s", uc.config.User.EndpointName)

	mongodService, err := uc.api.GetEndpoint(uc.config.User.EndpointName)
	if err != nil {
		log.Errorf("Error fetching MongoDB seed list from endpoint %s: %s", uc.config.User.EndpointName, err)
		return nil, err
	}
	return &db.Config{
		DialInfo: &mgo.DialInfo{
			Addrs:          mongodService.Hosts(),
			Username:       uc.config.UserAdminUser,
			Password:       uc.config.UserAdminPassword,
			ReplicaSetName: uc.config.Replset,
			Direct:         true,
			FailFast:       true,
		},
		SSL: uc.config.SSL,
	}, nil
}

func (uc *Controller) getSession() (*mgo.Session, error) {
	session, err := db.WaitForSession(uc.dbConfig, uc.maxConnectTries, uc.retrySleep)
	if err != nil {
		log.WithFields(log.Fields{
			"hosts": uc.dbConfig.DialInfo.Addrs,
		}).Error("Could not connect to host(s)!")
		return nil, err
	}

	log.WithFields(log.Fields{
		"hosts":   uc.dbConfig.DialInfo.Addrs,
		"replset": uc.config.Replset,
	}).Info("Connected to MongoDB host(s)")

	session.SetMode(mgo.Primary, true)
	session.SetSafe(&mgo.Safe{
		WMode: "majority",
		FSync: true,
	})
	return session, err
}

func (uc *Controller) Close() {
	if uc.session != nil {
		log.WithFields(log.Fields{
			"hosts":   uc.dbConfig.DialInfo.Addrs,
			"replset": uc.config.Replset,
		}).Info("Disconnecting from MongoDB host(s)")
		uc.session.Close()
		uc.session = nil
	}
}

func (uc *Controller) UpdateUsers() error {
	if uc.config.User.File == "" {
		return errors.New("No file provided")
	} else if uc.config.User.Database == "" {
		return ErrNoDbProvided
	}

	users, err := user_json.NewFromCLIPayloadFile(uc.config.User.File)
	if err != nil {
		log.WithError(err).Errorf("Failed loading payload file: %s", uc.config.User.File)
		return err
	}

	for _, updateUser := range users {
		if isSystemUser(updateUser.Username, uc.config.User.Database) {
			log.Errorf("Cannot change system user %s in database %s", uc.config.User.Username, uc.config.User.Database)
			return ErrCannotChgSysUser
		}
		err := updateUser.Validate(uc.config.User.Database)
		if err != nil {
			log.WithError(err).Errorf("Cannot validate user %s", uc.config.User.Username)
			return err
		}
		mgoUpdateUser, err := updateUser.ToMgoUser(uc.config.User.Database)
		if err != nil {
			log.WithError(err).Errorf("Cannot parse user %s", uc.config.User.Username)
			return err
		}
		err = UpdateUser(uc.session, mgoUpdateUser, uc.config.User.Database)
		if err != nil {
			log.WithError(err).Errorf("Cannot update user %s", uc.config.User.Username)
			return err
		}
	}

	log.Info("User update complete")
	return nil
}

func (uc *Controller) RemoveUser() error {
	if uc.config.User.Username == "" {
		return ErrNoUserProvided
	} else if uc.config.User.Database == "" {
		return ErrNoDbProvided
	} else if isSystemUser(uc.config.User.Username, uc.config.User.Database) {
		log.Errorf("Cannot change system user %s in database %s", uc.config.User.Username, uc.config.User.Database)
		return ErrCannotChgSysUser
	}

	err := RemoveUser(uc.session, uc.config.User.Username, uc.config.User.Database)
	if err != nil {
		return err
	}

	log.Info("User removal complete")
	return nil
}

func (uc *Controller) ReloadSystemUsers() error {
	err := UpdateUsers(uc.session, SystemUsers(), "admin")
	if err != nil {
		return err
	}

	log.Info("Reloading of system users complete")
	return nil
}
