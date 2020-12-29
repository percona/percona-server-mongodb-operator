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
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
	"gopkg.in/mgo.v2"
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

func (cnf *Config) configureSSLDialInfo() error {
	config := &tls.Config{
		InsecureSkipVerify: cnf.SSL.Insecure,
	}
	if len(cnf.SSL.PEMKeyFile) > 0 {
		ok, err := isFileExists(cnf.SSL.PEMKeyFile)
		if err != nil {
			return fmt.Errorf("Failed to check if file with name %s exists, err: %v", cnf.SSL.PEMKeyFile, err)
		}
		if ok {
			log.Debugf("Loading SSL/TLS PEM certificate: %s", cnf.SSL.PEMKeyFile)
			certificates, err := tls.LoadX509KeyPair(cnf.SSL.PEMKeyFile, cnf.SSL.PEMKeyFile)
			if err != nil {
				return fmt.Errorf(
					"Cannot load key pair from '%s' to connect to server '%s'. Got: %v",
					cnf.SSL.PEMKeyFile,
					cnf.DialInfo.Addrs,
					err,
				)
			}
			config.Certificates = []tls.Certificate{certificates}
		}
	}
	if len(cnf.SSL.CAFile) > 0 {
		ok, err := isFileExists(cnf.SSL.PEMKeyFile)
		if err != nil {
			return fmt.Errorf("Failed to check if file with name %s exists, err: %v", cnf.SSL.PEMKeyFile, err)
		}
		if ok {
			log.Debugf("Loading SSL/TLS Certificate Authority: %s", cnf.SSL.CAFile)
			ca, err := cnf.SSL.loadCaCertificate()
			if err != nil {
				return fmt.Errorf("Couldn't load client CAs from %s. Got: %s", cnf.SSL.CAFile, err)
			}
			config.RootCAs = ca
		}
	}
	cnf.DialInfo.DialServer = func(addr *mgo.ServerAddr) (net.Conn, error) {
		conn, err := tls.Dial("tcp", addr.String(), config)
		if err != nil {
			log.Errorf("Could not connect to %v. Got: %v", addr, err)
			lastSSLErr = err
			return nil, err
		}
		if !config.InsecureSkipVerify {
			dnsName := strings.SplitN(addr.String(), ":", 2)[0]
			err = validateConnection(conn, config, dnsName)
			if err != nil {
				log.Errorf("Could not disable hostname validation. Got: %v", err)
			}
		}
		return conn, err
	}
	return nil
}

func validateConnection(conn *tls.Conn, tlsConfig *tls.Config, dnsName string) error {
	var err error
	if err = conn.Handshake(); err != nil {
		conn.Close()
		return err
	}

	opts := x509.VerifyOptions{
		Roots:         tlsConfig.RootCAs,
		CurrentTime:   time.Now(),
		DNSName:       dnsName,
		Intermediates: x509.NewCertPool(),
	}

	certs := conn.ConnectionState().PeerCertificates
	for i, cert := range certs {
		if i == 0 {
			continue
		}
		opts.Intermediates.AddCert(cert)
	}

	_, err = certs[0].Verify(opts)
	if err != nil {
		conn.Close()
		return err
	}

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
