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
	"crypto/x509"
	"io/ioutil"
	"os"

	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
)

var lastSSLErr error

type SSLConfig struct {
	Enabled    bool
	PEMKeyFile string
	CAFile     string
	Insecure   bool
}

func (sc *SSLConfig) loadCaCertificate() (*x509.CertPool, error) {
	caCert, err := ioutil.ReadFile(sc.CAFile)
	if err != nil {
		return nil, err
	}
	certificates := x509.NewCertPool()
	certificates.AppendCertsFromPEM(caCert)
	return certificates, nil
}

// LastSSLError returns the last error related to the DB connection SSL handshake
func LastSSLError() error {
	return lastSSLErr
}

func (cnf *Config) configureTLS() error {
	config := &tls.Config{
		InsecureSkipVerify: cnf.SSL.Insecure,
	}

	if len(cnf.SSL.PEMKeyFile) == 0 || len(cnf.SSL.CAFile) == 0 {
		return nil
	}

	pemOk, err := isFileExists(cnf.SSL.PEMKeyFile)
	if err != nil {
		return errors.Wrapf(err, "check if file with name %s exists", cnf.SSL.PEMKeyFile)
	}

	caOk, err := isFileExists(cnf.SSL.CAFile)
	if err != nil {
		return errors.Wrapf(err, "check if file with name %s exists", cnf.SSL.CAFile)
	}

	if !pemOk || !caOk {
		cnf.SSL = nil
		return nil
	}

	log.Debugf("Loading SSL/TLS PEM certificate: %s", cnf.SSL.PEMKeyFile)

	certificates, err := tls.LoadX509KeyPair(cnf.SSL.PEMKeyFile, cnf.SSL.PEMKeyFile)
	if err != nil {
		return errors.Wrapf(err, "load key pair from '%s' to connect to server '%s'", cnf.SSL.PEMKeyFile, cnf.Hosts)
	}

	config.Certificates = []tls.Certificate{certificates}

	log.Debugf("Loading SSL/TLS Certificate Authority: %s", cnf.SSL.CAFile)
	ca, err := cnf.SSL.loadCaCertificate()
	if err != nil {
		return errors.Wrapf(err, "load client CAs from %s", cnf.SSL.CAFile)
	}

	config.RootCAs = ca
	cnf.TLSConf = config

	return nil
}

func isFileExists(name string) (bool, error) {
	_, err := os.Stat(name)
	if os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}
