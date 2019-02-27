package db

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/globalsign/mgo"
)

const appName = "percona/percona-backup-mongodb"

// TODO: move this to a common place because we also load CA
// certs in other parts of the code
func loadCaCertificate(caFile string) (*x509.CertPool, error) {
	caCert, err := ioutil.ReadFile(filepath.Clean(caFile))
	if err != nil {
		return nil, err
	}
	certificates := x509.NewCertPool()
	if ok := certificates.AppendCertsFromPEM(caCert); !ok {
		return nil, errors.New("could not append x509 PEM certificate to pool")
	}
	return certificates, nil
}

func validateConnection(conn *tls.Conn, tlsConfig *tls.Config, dnsName string) error {
	if err := conn.Handshake(); err != nil {
		conn.Close()
		return err
	}

	opts := x509.VerifyOptions{
		CurrentTime:   time.Now(),
		DNSName:       dnsName,
		Roots:         tlsConfig.RootCAs,
		Intermediates: x509.NewCertPool(),
	}

	certs := conn.ConnectionState().PeerCertificates
	for i, cert := range certs {
		if i == 0 {
			continue
		}
		opts.Intermediates.AddCert(cert)
	}

	_, err := certs[0].Verify(opts)
	if err != nil {
		conn.Close()
		return err
	}

	return nil
}

type Config struct {
	Addrs    []string
	Replset  string
	Username string
	Password string
	CertFile string
	CAFile   string
	Timeout  time.Duration
	Insecure bool
}

func NewDialInfo(config *Config) (*mgo.DialInfo, error) {
	dialInfo := &mgo.DialInfo{
		AppName:        appName,
		Addrs:          config.Addrs,
		ReplicaSetName: config.Replset,
		Username:       config.Username,
		Password:       config.Password,
		Direct:         true,
	}
	if config.Timeout > 0 {
		dialInfo.Timeout = config.Timeout
	}

	// return early if ssl is disabled
	if config.CertFile == "" && config.CAFile == "" {
		return dialInfo, nil
	}

	// #nosec G402
	tlsConfig := &tls.Config{
		InsecureSkipVerify: config.Insecure,
	}

	if config.CertFile != "" {
		certificates, err := tls.LoadX509KeyPair(config.CertFile, config.CertFile)
		if err != nil {
			return nil, fmt.Errorf("Cannot load key pair from '%s': %v", config.CertFile, err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificates}
	}
	if config.CAFile != "" {
		caCert, err := loadCaCertificate(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("Cannot load client CA from '%s': %s", config.CAFile, err)
		}
		tlsConfig.RootCAs = caCert
	}
	dialInfo.DialServer = func(addr *mgo.ServerAddr) (net.Conn, error) {
		conn, err := tls.Dial("tcp", addr.String(), tlsConfig)
		if err != nil {
			return nil, err
		}
		if !tlsConfig.InsecureSkipVerify {
			// TODO: find a way to allow .validateConnection() errors to be read
			// from outside of this func
			dnsName := strings.SplitN(addr.String(), ":", 2)[0]
			if err := validateConnection(conn, tlsConfig, dnsName); err != nil {
				return nil, err
			}
		}
		return conn, err
	}
	return dialInfo, nil
}
