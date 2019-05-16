package testutils

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globalsign/mgo"
	"github.com/pkg/errors"
)

func loadCaCertificate(caFile string) (*x509.CertPool, error) {
	caCert, err := ioutil.ReadFile(caFile)
	if err != nil {
		return nil, err
	}
	certificates := x509.NewCertPool()
	if ok := certificates.AppendCertsFromPEM(caCert); !ok {
		return nil, errors.New("could not append x509 PEM certificate to pool")
	}
	return certificates, nil
}

func dialInfo(addrs []string, rs string) (*mgo.DialInfo, error) {
	MongoDBSSLDir = "../docker/test/ssl"
	MongoDBSSLPEMKeyFile = filepath.Join(MongoDBSSLDir, "client.pem")
	MongoDBSSLCACertFile = filepath.Join(MongoDBSSLDir, "rootCA.crt")
	di, err := newDialInfo(&mgo.DialInfo{
		Addrs:          addrs,
		ReplicaSetName: rs,
		Username:       MongoDBUser,
		Password:       MongoDBPassword,
		Timeout:        MongoDBTimeout,
	})
	if err != nil {
		return nil, err
	}
	return di, nil
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

func newDialInfo(di *mgo.DialInfo) (*mgo.DialInfo, error) {
	dialInfo := &mgo.DialInfo{
		AppName:        "mongodbtest",
		Addrs:          di.Addrs,
		ReplicaSetName: di.ReplicaSetName,
		Username:       di.Username,
		Password:       di.Password,
		Direct:         true,
	}

	MongoDBSSLDir := filepath.Join(BaseDir(), "docker/test/ssl")
	MongoDBSSLPEMKeyFile := filepath.Join(MongoDBSSLDir, "client.pem")
	MongoDBSSLCACertFile := filepath.Join(MongoDBSSLDir, "rootCA.crt")
	CertFile := MongoDBSSLPEMKeyFile
	CAFile := MongoDBSSLCACertFile

	if di.Timeout > 0 {
		dialInfo.Timeout = di.Timeout
	}

	// return early if ssl is disabled
	if CertFile == "" && CAFile == "" {
		return dialInfo, nil
	}

	certFileExists := false
	caFileExists := false

	if _, err := os.Stat(CertFile); !os.IsNotExist(err) {
		certFileExists = true
	}

	if _, err := os.Stat(CAFile); !os.IsNotExist(err) {
		caFileExists = true
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: !certFileExists || !caFileExists,
	}

	if certFileExists {
		certificates, err := tls.LoadX509KeyPair(CertFile, CertFile)
		if err != nil {
			return nil, fmt.Errorf("Cannot load key pair from '%s': %v", CertFile, err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificates}
	}
	if caFileExists {
		caCert, err := loadCaCertificate(CAFile)
		if err != nil {
			return nil, fmt.Errorf("Cannot load client CA from '%s': %s", CAFile, err)
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

func DialInfoForPort(rs, port string) (*mgo.DialInfo, error) {
	di, err := dialInfo([]string{
		MongoDBHost + ":" + port},
		rs,
	)
	if err != nil {
		return nil, errors.Wrap(err, ".DialInfoForPort() failed")
	}
	return di, nil
}

func PrimaryDialInfo(t *testing.T, rs string) *mgo.DialInfo {
	di, err := dialInfo([]string{
		GetMongoDBAddr(rs, "primary")},
		rs,
	)
	if err != nil {
		t.Fatalf(".PrimaryDialInfo() failed: %v", err.Error())
	}
	return di
}

func ReplsetDialInfo(t *testing.T, rs string) *mgo.DialInfo {
	di, err := dialInfo(
		GetMongoDBReplsetAddrs(rs),
		rs,
	)
	if err != nil {
		t.Fatalf(".ReplsetDialInfo() failed: %v", err.Error())
	}
	di.Direct = false
	return di
}

func ConfigsvrReplsetDialInfo(t *testing.T) *mgo.DialInfo {
	di, err := dialInfo(
		GetMongoDBReplsetAddrs(MongoDBConfigsvrReplsetName),
		MongoDBConfigsvrReplsetName,
	)
	if err != nil {
		t.Fatalf(".ConfigsvrReplsetDialInfo() failed: %v", err.Error())
	}
	di.Direct = false
	return di
}

func MongosDialInfo(t *testing.T) *mgo.DialInfo {
	di, err := dialInfo([]string{MongoDBHost + ":" + MongoDBMongosPort}, "")
	if err != nil {
		t.Fatalf(".MongosDialInfo() failed: %v", err.Error())
	}
	return di
}

func CleanupDatabases(session *mgo.Session, ignore map[string][]string) error {
	if ignore == nil {
		ignore = map[string][]string{}
	}

	// Ensure we don't delete the system.users collection otherwise authentication will be broken
	if _, ok := ignore["admin"]; !ok {
		ignore["admin"] = []string{"system.users"}
	} else {
		ignore["admin"] = append(ignore["admin"], "system.users")
	}
	databases, err := session.DatabaseNames()
	if err != nil {
		return errors.Wrap(err, "cannot get DBs list to clean up the config server")
	}
	for _, dbName := range databases {
		collections, err := session.DB(dbName).CollectionNames()
		if err != nil {
			return errors.Wrapf(err, "cannot list %s database collections to clean up config server", dbName)
		}
		for _, collection := range collections {
			if skipCollection(dbName, collection, ignore) {
				continue
			}
			if _, err := session.DB(dbName).C(collection).RemoveAll(nil); err != nil {
				return errors.Wrapf(err, "cannot empty %s.%s collection", dbName, collection)
			}
		}
	}

	return nil
}

func skipCollection(db, col string, ignoreList map[string][]string) bool {
	ignoreCols, ok := ignoreList[db]
	if !ok {
		return false
	}
	for _, c := range ignoreCols {
		if c == col {
			return true
		}
	}
	return false
}
