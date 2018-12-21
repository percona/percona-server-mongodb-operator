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
	"os"

	"github.com/percona/mongodb-orchestration-tools/pkg"
	"gopkg.in/mgo.v2"
)

var (
	clusterAdminUsername   = os.Getenv(pkg.EnvMongoDBClusterAdminUser)
	clusterAdminPassword   = os.Getenv(pkg.EnvMongoDBClusterAdminPassword)
	clusterMonitorUsername = os.Getenv(pkg.EnvMongoDBClusterMonitorUser)
	clusterMonitorPassword = os.Getenv(pkg.EnvMongoDBClusterMonitorPassword)
	backupUsername         = os.Getenv(pkg.EnvMongoDBBackupUser)
	backupPassword         = os.Getenv(pkg.EnvMongoDBBackupPassword)
	userAdminUsername      = os.Getenv(pkg.EnvMongoDBUserAdminUser)
	userAdminPassword      = os.Getenv(pkg.EnvMongoDBUserAdminPassword)

	SystemUserDatabase = "admin"
	UserAdmin          = &mgo.User{
		Username: userAdminUsername,
		Password: userAdminPassword,
		Roles: []mgo.Role{
			RoleUserAdminAny,
		},
	}
	systemUsers = []*mgo.User{
		{
			Username: clusterAdminUsername,
			Password: clusterAdminPassword,
			Roles: []mgo.Role{
				RoleClusterAdmin,
			},
		},
		{
			Username: clusterMonitorUsername,
			Password: clusterMonitorPassword,
			Roles: []mgo.Role{
				RoleClusterMonitor,
			},
		},
		{
			Username: backupUsername,
			Password: backupPassword,
			Roles: []mgo.Role{
				RoleBackup,
				RoleClusterMonitor,
				RoleRestore,
			},
		},
	}
	SystemUsernames = []string{
		userAdminUsername,
		clusterAdminUsername,
		clusterMonitorUsername,
		backupUsername,
	}
)

func SystemUsers() []*mgo.User {
	users := []*mgo.User{}
	for _, user := range systemUsers {
		if user.Username == "" || user.Password == "" || len(user.Roles) < 1 {
			continue
		}
		users = append(users, user)
	}
	return users
}

func SetSystemUsers(users []*mgo.User) {
	systemUsers = users
}
