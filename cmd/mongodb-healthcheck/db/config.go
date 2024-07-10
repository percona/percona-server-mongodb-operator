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
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/pkg/errors"

	"github.com/percona/percona-server-mongodb-operator/cmd/mongodb-healthcheck/tool"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
)

var (
	DefaultMongoDBHost            = "localhost"
	DefaultMongoDBPort            = "27017"
	DefaultMongoDBAuthDB          = "admin"
	DefaultMongoDBTimeout         = "5s"
	DefaultMongoDBTimeoutDuration = time.Duration(5) * time.Second
)

type Config struct {
	mongo.Config
	SSL *SSLConfig
}

func getDefaultMongoDBAddress() string {
	hostname := DefaultMongoDBHost

	mongodbPort := os.Getenv(tool.EnvMongoDBPort)
	if mongodbPort != "" {
		return hostname + ":" + mongodbPort
	}
	return hostname + ":" + DefaultMongoDBPort
}

func NewConfig(app *kingpin.Application, envUser string, envPassword string) (*Config, error) {
	conf := &Config{}
	app.Flag(
		"address",
		"mongodb server address (hostname:port), defaults to '$TASK_NAME.$FRAMEWORK_HOST:$MONGODB_PORT' if the env vars are available and SSL is used, if not the default is '"+DefaultMongoDBHost+":"+DefaultMongoDBPort+"'",
	).Default(getDefaultMongoDBAddress()).StringsVar(&conf.Hosts)
	app.Flag(
		"replset",
		"mongodb replica set name, overridden by env var "+tool.EnvMongoDBReplset,
	).Envar(tool.EnvMongoDBReplset).StringVar(&conf.ReplSetName)

	usernameFile := fmt.Sprintf("/etc/users-secret/%s", envUser)
	if _, err := os.Stat(usernameFile); err == nil {
		username, err := os.ReadFile(usernameFile)
		if err != nil {
			return nil, errors.Wrapf(err, "read %s", usernameFile)
		}

		conf.Username = string(username)
	} else if os.IsNotExist(err) {
		app.Flag(
			"username",
			"mongodb auth username, this flag or env var "+envUser+" is required",
		).Envar(envUser).Required().StringVar(&conf.Username)
	} else {
		return nil, errors.Wrap(err, "failed to get password")
	}

	pwdFile := fmt.Sprintf("/etc/users-secret/%s", envPassword)
	if _, err := os.Stat(pwdFile); err == nil {
		pass, err := os.ReadFile(pwdFile)
		if err != nil {
			return nil, errors.Wrapf(err, "read %s", pwdFile)
		}

		conf.Password = string(pass)
	} else if os.IsNotExist(err) {
		app.Flag(
			"password",
			"mongodb auth password, this flag or env var "+envPassword+" is required",
		).Envar(envPassword).Required().StringVar(&conf.Password)
	} else {
		return nil, errors.Wrap(err, "failed to get password")
	}

	ssl := &SSLConfig{}
	app.Flag(
		"ssl",
		"enable SSL secured mongodb connection, overridden by env var "+tool.EnvMongoDBNetSSLEnabled,
	).Envar(tool.EnvMongoDBNetSSLEnabled).BoolVar(&ssl.Enabled)
	app.Flag(
		"sslPEMKeyFile",
		"path to client SSL Certificate file (including key, in PEM format), overridden by env var "+tool.EnvMongoDBNetSSLPEMKeyFile,
	).Envar(tool.EnvMongoDBNetSSLPEMKeyFile).StringVar(&ssl.PEMKeyFile)
	app.Flag(
		"sslCAFile",
		"path to SSL Certificate Authority file (in PEM format), overridden by env var "+tool.EnvMongoDBNetSSLCAFile,
	).Envar(tool.EnvMongoDBNetSSLCAFile).StringVar(&ssl.CAFile)
	app.Flag(
		"sslInsecure",
		"skip validation of the SSL certificate and hostname, overridden by env var "+tool.EnvMongoDBNetSSLInsecure,
	).Envar(tool.EnvMongoDBNetSSLInsecure).BoolVar(&ssl.Insecure)

	conf.SSL = ssl

	return conf, nil
}
