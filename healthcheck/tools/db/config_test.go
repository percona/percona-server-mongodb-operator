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
	"os"
	"testing"

	"github.com/alecthomas/kingpin"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/stretchr/testify/assert"
	"gopkg.in/mgo.v2"
)

func TestInternalDBUri(t *testing.T) {
	config := &Config{
		DialInfo: &mgo.DialInfo{
			Addrs:    []string{"test:1234"},
			Username: "admin",
			Password: "123456",
		},
		SSL: &SSLConfig{
			Enabled: false,
		},
	}
	assert.Equal(t, "mongodb://admin:123456@test:1234", config.Uri(), ".Uri() returned invalid uri")

	config.SSL.Enabled = true
	assert.Equal(t, "mongodb://admin:123456@test:1234?ssl=true", config.Uri(), ".Uri() returned invalid uri")

	config.DialInfo.ReplicaSetName = "test"
	assert.Equal(t, "mongodb://admin:123456@test:1234?replicaSet=test&ssl=true", config.Uri(), ".Uri() returned invalid uri")
}

func TestInternalDBNewConfig(t *testing.T) {
	app := kingpin.New(t.Name(), t.Name())
	cnf := NewConfig(app, "", "")
	assert.NotNil(t, cnf)

	os.Setenv(pkg.EnvMongoDBNetSSLEnabled, "true")
	os.Setenv(pkg.EnvMongoDBPort, "27017")
	os.Setenv(pkg.EnvMongoDBReplset, t.Name())
	defer os.Unsetenv(pkg.EnvMongoDBNetSSLEnabled)
	defer os.Unsetenv(pkg.EnvMongoDBPort)
	defer os.Unsetenv(pkg.EnvMongoDBReplset)

	_, err := app.Parse([]string{"--username=test", "--password=test"})
	assert.NoError(t, err)
	assert.Equal(t, []string{getDefaultMongoDBAddress()}, cnf.DialInfo.Addrs)
	assert.Equal(t, t.Name(), cnf.DialInfo.ReplicaSetName)
}

func TestInternalDBNewSSLConfig(t *testing.T) {
	app := kingpin.New(t.Name(), t.Name())
	cnf := NewConfig(app, "", "")
	assert.NotNil(t, cnf)

	os.Setenv(pkg.EnvMongoDBNetSSLEnabled, "true")
	defer os.Unsetenv(pkg.EnvMongoDBNetSSLEnabled)
	_, err := app.Parse([]string{"--username=test", "--password=test"})
	assert.NoError(t, err)
	assert.True(t, cnf.SSL.Enabled)
}
