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
	"io/ioutil"
	"os"
	"strings"
	"time"

	"github.com/alecthomas/kingpin"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/pkg"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/dcos"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
)

var (
	DefaultMongoDBHost            = "localhost"
	DefaultMongoDBPort            = "27017"
	DefaultMongoDBAuthDB          = "admin"
	DefaultMongoDBTimeout         = "5s"
	DefaultMongoDBTimeoutDuration = time.Duration(5) * time.Second
)

type Config struct {
	DialInfo *mgo.DialInfo
	SSL      *SSLConfig
}

func getDefaultMongoDBAddress() string {
	hostname := DefaultMongoDBHost

	// use the full hostname when using SSL mode
	if os.Getenv(pkg.EnvMongoDBNetSSLEnabled) == "true" {
		frameworkHost := os.Getenv(dcos.EnvFrameworkHost)
		taskName := os.Getenv(dcos.EnvTaskName)
		if taskName != "" && frameworkHost != "" {
			hostname = taskName + "." + frameworkHost
		}
	}

	mongodbPort := os.Getenv(pkg.EnvMongoDBPort)
	if mongodbPort != "" {
		return hostname + ":" + mongodbPort
	}
	return hostname + ":" + DefaultMongoDBPort
}

func NewConfig(app *kingpin.Application, envUser string, envPassword string) (*Config, error) {
	db := &Config{
		DialInfo: &mgo.DialInfo{},
	}
	app.Flag(
		"address",
		"mongodb server address (hostname:port), defaults to '$TASK_NAME.$FRAMEWORK_HOST:$MONGODB_PORT' if the env vars are available and SSL is used, if not the default is '"+DefaultMongoDBHost+":"+DefaultMongoDBPort+"'",
	).Default(getDefaultMongoDBAddress()).StringsVar(&db.DialInfo.Addrs)
	app.Flag(
		"replset",
		"mongodb replica set name, overridden by env var "+pkg.EnvMongoDBReplset,
	).Envar(pkg.EnvMongoDBReplset).StringVar(&db.DialInfo.ReplicaSetName)
	app.Flag(
		"timeout",
		"mongodb server timeout",
	).Default(DefaultMongoDBTimeout).DurationVar(&db.DialInfo.Timeout)
	app.Flag(
		"username",
		"mongodb auth username, this flag or env var "+envUser+" is required",
	).Envar(envUser).Required().StringVar(&db.DialInfo.Username)

	pwdFile := "/etc/users-secret/MONGODB_CLUSTER_MONITOR_PASSWORD"
	if _, err := os.Stat(pwdFile); err == nil {
		log.Info("reading password from secret")

		pass, err := ioutil.ReadFile(pwdFile)
		if err != nil {
			return nil, errors.Wrapf(err, "read %s", pwdFile)
		}

		db.DialInfo.Password = string(pass)
	} else if os.IsNotExist(err) {
		app.Flag(
			"password",
			"mongodb auth password, this flag or env var "+envPassword+" is required",
		).Envar(envPassword).Required().StringVar(&db.DialInfo.Password)
	} else {
		return nil, errors.Wrap(err, "failed to get password")
	}

	app.Flag(
		"authDb",
		"mongodb auth database",
	).Default(DefaultMongoDBAuthDB).StringVar(&db.DialInfo.Source)
	app.Flag(
		"useDirectConnection",
		"enable direct connection",
	).Default("true").BoolVar(&db.DialInfo.Direct)
	app.Flag(
		"useFailFastConnection",
		"enable fail-fast connection",
	).Default("true").BoolVar(&db.DialInfo.FailFast)

	db.SSL = NewSSLConfig(app)
	return db, nil
}

func NewSSLConfig(app *kingpin.Application) *SSLConfig {
	ssl := &SSLConfig{}
	app.Flag(
		"ssl",
		"enable SSL secured mongodb connection, overridden by env var "+pkg.EnvMongoDBNetSSLEnabled,
	).Envar(pkg.EnvMongoDBNetSSLEnabled).BoolVar(&ssl.Enabled)
	app.Flag(
		"sslPEMKeyFile",
		"path to client SSL Certificate file (including key, in PEM format), overridden by env var "+pkg.EnvMongoDBNetSSLPEMKeyFile,
	).Envar(pkg.EnvMongoDBNetSSLPEMKeyFile).StringVar(&ssl.PEMKeyFile)
	app.Flag(
		"sslCAFile",
		"path to SSL Certificate Authority file (in PEM format), overridden by env var "+pkg.EnvMongoDBNetSSLCAFile,
	).Envar(pkg.EnvMongoDBNetSSLCAFile).StringVar(&ssl.CAFile)
	app.Flag(
		"sslInsecure",
		"skip validation of the SSL certificate and hostname, overridden by env var "+pkg.EnvMongoDBNetSSLInsecure,
	).Envar(pkg.EnvMongoDBNetSSLInsecure).BoolVar(&ssl.Insecure)

	return ssl
}

func (cnf *Config) Uri() string {
	options := []string{}
	if cnf.DialInfo.ReplicaSetName != "" {
		options = append(options, "replicaSet="+cnf.DialInfo.ReplicaSetName)
	}
	if cnf.SSL.Enabled {
		options = append(options, "ssl=true")
	}
	hosts := strings.Join(cnf.DialInfo.Addrs, ",")
	uri := "mongodb://" + cnf.DialInfo.Username + ":" + cnf.DialInfo.Password + "@" + hosts
	if len(options) > 0 {
		uri = uri + "?" + strings.Join(options, "&")
	}
	return uri
}
