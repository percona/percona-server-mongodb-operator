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

	log "github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
)

const (
	RoleBackup         mgo.Role = "backup"
	RoleRestore        mgo.Role = "restore"
	RoleClusterAdmin   mgo.Role = mgo.RoleClusterAdmin
	RoleClusterMonitor mgo.Role = "clusterMonitor"
	RoleUserAdminAny   mgo.Role = mgo.RoleUserAdminAny
)

type UserChangeData struct {
	Users []*mgo.User `bson:"users"`
}

func UpdateUser(session *mgo.Session, user *mgo.User, dbName string) error {
	if user.Username == "" || user.Password == "" {
		return errors.New("No username or password defined for user")
	}
	if len(user.Roles) < 1 && len(user.OtherDBRoles) < 1 {
		return errors.New("No roles defined for user")
	}

	log.WithFields(log.Fields{
		"user":         user.Username,
		"roles":        user.Roles,
		"otherDBRoles": user.OtherDBRoles,
		"db":           dbName,
	}).Info("Adding/updating MongoDB user")

	return session.DB(dbName).UpsertUser(user)
}

func UpdateUsers(session *mgo.Session, users []*mgo.User, dbName string) error {
	for _, user := range users {
		err := UpdateUser(session, user, dbName)
		if err != nil {
			return err
		}
	}
	return nil
}

func RemoveUser(session *mgo.Session, username, db string) error {
	log.Infof("Removing user %s from db %s", username, db)
	err := session.DB(db).RemoveUser(username)
	if err == mgo.ErrNotFound {
		log.Warnf("Cannot remove user, %s does not exist in database %s", username, db)
		return nil
	}
	return err
}

func isSystemUser(username, db string) bool {
	if db != SystemUserDatabase {
		return false
	}
	for _, sysUsername := range SystemUsernames {
		if username == sysUsername {
			return true
		}
	}
	return false
}
