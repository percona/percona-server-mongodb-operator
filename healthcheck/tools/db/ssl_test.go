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
	"crypto/tls"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools"
	"github.com/percona/percona-server-mongodb-operator/healthcheck/tools/testutils"
	"github.com/stretchr/testify/assert"
)

const testSSLDirRelPath = "../../docker/test/ssl"

var (
	sslCertFile = tools.RelPathToAbs(filepath.Join(testSSLDirRelPath, "client.pem"))
	sslCAFile   = tools.RelPathToAbs(filepath.Join(testSSLDirRelPath, "rootCA.crt"))
)

func TestInternalDBLoadCaCertificate(t *testing.T) {
	sslConfig := &SSLConfig{
		Enabled: true,
		CAFile:  sslCAFile,
	}

	pool, err := sslConfig.loadCaCertificate()
	assert.NoError(t, err, ".loadCaCertificate() should return no error")
	assert.NotEmpty(t, pool.Subjects(), ".loadCaCertificate() should return a non-empty x509.CertPool")

	sslConfig.CAFile = "/does/not/exist.crt"
	_, err = sslConfig.loadCaCertificate()
	assert.Error(t, err, ".loadCaCertificate() should return an error when given missing path")
}

func TestInternalDBConfigureSSLDialInfo(t *testing.T) {
	config := &Config{
		DialInfo: testPrimaryDbConfig.DialInfo,
		SSL: &SSLConfig{
			Enabled:    true,
			PEMKeyFile: sslCertFile,
			CAFile:     sslCAFile,
			Insecure:   true,
		},
	}
	assert.Nil(t, config.DialInfo.DialServer, "config.DialInfo.DialServer should be nil")

	err := config.configureSSLDialInfo()
	assert.NoError(t, err, ".configureSSLDialInfo() should not return an error")
	assert.NotNil(t, config.DialInfo.DialServer, "config.DialInfo.DialServer should not be nil")
}

func TestInternalDBValidateConnection(t *testing.T) {
	testutils.DoSkipTest(t)

	sslCnf := &SSLConfig{
		Enabled:    true,
		PEMKeyFile: sslCertFile,
		CAFile:     sslCAFile,
	}
	roots, err := sslCnf.loadCaCertificate()
	assert.NoError(t, err, "Could not load test root CA cert")

	certificates, err := tls.LoadX509KeyPair(sslCertFile, sslCertFile)
	assert.NoErrorf(t, err, "Cannot load key pair from '%s'", sslCertFile)

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{certificates},
		RootCAs:      roots,
	}
	host := testutils.MongodbHost + ":" + testutils.MongodbPrimaryPort
	conn, err := tls.Dial("tcp", host, tlsConfig)
	assert.NoErrorf(t, err, "Failed to connect to '%s'", host)
	defer conn.Close()

	err = validateConnection(conn, tlsConfig, testutils.MongodbHost)
	assert.NoError(t, err, "Failed to run .validateConnection()")

	err = validateConnection(conn, tlsConfig, "this.should.fail")
	if err == nil || !(strings.HasPrefix(err.Error(), "x509: certificate is valid for ") && strings.HasSuffix(err.Error(), " not this.should.fail")) {
		assert.Failf(t, "Expected an error from .validateConnection(): %v", err.Error())
	}
}

func TestInternalDBGetSessionSSL(t *testing.T) {
	testutils.DoSkipTest(t)

	testPrimaryDbConfig.SSL = &SSLConfig{
		Enabled:    true,
		PEMKeyFile: sslCertFile,
		CAFile:     sslCAFile,
		Insecure:   false,
	}

	// test secure mode
	testLogBuffer.Reset()
	assert.Nil(t, LastSSLError(), ".LastSSLError() should be nil")
	testPrimaryDbConfig.DialInfo.Timeout = 100 * time.Millisecond
	_, err := GetSession(testPrimaryDbConfig)
	assert.NoError(t, err, ".GetSession() should return nil")
	assert.NoError(t, LastSSLError(), ".LastSSLError() should return nil")

	// enable insecure mode (due to self-signed certs) and connect
	testPrimaryDbConfig.DialInfo.Timeout = testutils.MongodbTimeout
	testPrimaryDbConfig.SSL.Insecure = true
	testPrimarySessionSSL, err := GetSession(testPrimaryDbConfig)
	assert.NoError(t, err, ".GetSession() should return no error")
	defer testPrimarySessionSSL.Close()

	// test SSL connection
	assert.NotNil(t, testPrimarySessionSSL, ".GetSession() should not return a nil testPrimarySession")
	assert.NoError(t, testPrimarySessionSSL.Ping(), ".GetSession() returned a session that failed to ping")

	testPrimaryDbConfig.SSL = &SSLConfig{}
}
