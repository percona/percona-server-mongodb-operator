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
	"os"

	"github.com/pkg/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var lastSSLErr error

type SSLConfig struct {
	Enabled    bool
	PEMKeyFile string
	CAFile     string
	Insecure   bool
}

func (sc *SSLConfig) loadCaCertificate() (*x509.CertPool, error) {
	caCert, err := os.ReadFile(sc.CAFile)
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
	log := logf.Log

	if !cnf.SSL.Enabled {
		return nil
	}

	config := &tls.Config{
		InsecureSkipVerify: cnf.SSL.Insecure,
	}

	// Configure client cert
	if len(cnf.SSL.PEMKeyFile) != 0 {
		if err := isFileExists(cnf.SSL.PEMKeyFile); err != nil {
			return errors.Wrapf(err, "check if file with name %s exists", cnf.SSL.PEMKeyFile)
		}

		log.V(1).Info("Loading SSL/TLS PEM certificate", "certificate", cnf.SSL.PEMKeyFile)
		certificates, err := tls.LoadX509KeyPair(cnf.SSL.PEMKeyFile, cnf.SSL.PEMKeyFile)
		if err != nil {
			return errors.Wrapf(err, "load key pair from '%s' to connect to server '%s'", cnf.SSL.PEMKeyFile, cnf.Hosts)
		}

		config.Certificates = []tls.Certificate{certificates}
	}

	// Configure CA cert
	if len(cnf.SSL.CAFile) != 0 {
		if err := isFileExists(cnf.SSL.CAFile); err != nil {
			return errors.Wrapf(err, "check if file with name %s exists", cnf.SSL.CAFile)
		}

		log.V(1).Info("Loading SSL/TLS Certificate Authority: %s", "ca", cnf.SSL.CAFile)
		ca, err := cnf.SSL.loadCaCertificate()
		if err != nil {
			return errors.Wrapf(err, "load client CAs from %s", cnf.SSL.CAFile)
		}

		config.RootCAs = ca
	}

	cnf.TLSConf = config
	return nil
}

func isFileExists(name string) error {
	_, err := os.Stat(name)
	return err
}
