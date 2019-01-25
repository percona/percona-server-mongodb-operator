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

package testutils

import (
	"errors"
	"os"
	"testing"
	"time"

	"gopkg.in/mgo.v2"
)

const (
	envEnableDBTests         = "ENABLE_MONGODB_TESTS"
	envMongoDBReplsetName    = "TEST_RS_NAME"
	envMongoDBPrimaryPort    = "TEST_PRIMARY_PORT"
	envMongoDBSecondary1Port = "TEST_SECONDARY1_PORT"
	envMongoDBSecondary2Port = "TEST_SECONDARY2_PORT"
	envMongoDBAdminUser      = "TEST_ADMIN_USER"
	envMongoDBAdminPassword  = "TEST_ADMIN_PASSWORD"
)

var (
	enableDBTests         = os.Getenv(envEnableDBTests)
	MongodbReplsetName    = os.Getenv(envMongoDBReplsetName)
	MongodbHost           = "127.0.0.1"
	MongodbHostname       = "localhost"
	MongodbPrimaryPort    = os.Getenv(envMongoDBPrimaryPort)
	MongodbSecondary1Port = os.Getenv(envMongoDBSecondary1Port)
	MongodbSecondary2Port = os.Getenv(envMongoDBSecondary2Port)
	MongodbAdminUser      = os.Getenv(envMongoDBAdminUser)
	MongodbAdminPassword  = os.Getenv(envMongoDBAdminPassword)
	MongodbTimeout        = time.Duration(10) * time.Second
)

// Enabled returns a boolean reflecting whether testing against Mongodb should occur
func Enabled() bool {
	return enableDBTests == "true"
}

// getDialInfo returns a *mgo.DialInfo configured for testing
func getDialInfo(host, port string) (*mgo.DialInfo, error) {
	if !Enabled() {
		return nil, nil
	}
	if port == "" {
		return nil, errors.New("Port argument is not set")
	} else if host == "" {
		return nil, errors.New("Host argument is not set")
	} else if MongodbReplsetName == "" {
		return nil, errors.New("Replica set name env var is not set")
	} else if MongodbAdminUser == "" {
		return nil, errors.New("Admin user env var is not set")
	} else if MongodbAdminPassword == "" {
		return nil, errors.New("Admin password env var is not set")
	}
	return &mgo.DialInfo{
		Addrs:          []string{host + ":" + port},
		Direct:         true,
		Timeout:        MongodbTimeout,
		Username:       MongodbAdminUser,
		Password:       MongodbAdminPassword,
		ReplicaSetName: MongodbReplsetName,
	}, nil
}

// GetSession returns a *mgo.Session configured for testing against a MongoDB Primary
func GetSession(port string) (*mgo.Session, error) {
	dialInfo, err := getDialInfo(MongodbHost, port)
	if err != nil {
		return nil, err
	}
	session, err := mgo.DialWithInfo(dialInfo)
	if err != nil {
		return nil, err
	}
	return session, err
}

// DoSkipTest handles the conditional skipping of tests, based on the output of .Enabled()
func DoSkipTest(t *testing.T) {
	if !Enabled() {
		t.Skipf("Skipping test, env var %s is not 'true'", envEnableDBTests)
	}
}
