package tls

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

var validityNotAfter = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// Issue returns CA certificate, TLS certificate and TLS private key
func Issue(hosts []string) (caCert []byte, tlsCert []byte, tlsKey []byte, err error) {
	rsaBits := 2048
	priv, err := rsa.GenerateKey(rand.Reader, rsaBits)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate rsa key: %v", err)
	}
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "generate serial number for root")
	}
	subject := pkix.Name{
		Organization: []string{"Root CA"},
	}
	issuer := pkix.Name{
		Organization: []string{"Root CA"},
	}
	caTemplate := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               subject,
		NotBefore:             time.Now(),
		NotAfter:              validityNotAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("generate CA certificate: %v", err)
	}
	certOut := &bytes.Buffer{}
	err = pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode CA certificate: %v", err)
	}
	cert := certOut.Bytes()

	serialNumber, err = rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "generate serial number for client")
	}
	subject = pkix.Name{
		Organization: []string{"PSMDB"},
	}
	tlsTemplate := x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               subject,
		Issuer:                issuer,
		NotBefore:             time.Now(),
		NotAfter:              validityNotAfter,
		DNSNames:              hosts,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
	}
	clientKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "generate client key")
	}
	tlsDerBytes, err := x509.CreateCertificate(rand.Reader, &tlsTemplate, &caTemplate, &clientKey.PublicKey, priv)
	if err != nil {
		return nil, nil, nil, err
	}
	tlsCertOut := &bytes.Buffer{}
	err = pem.Encode(tlsCertOut, &pem.Block{Type: "CERTIFICATE", Bytes: tlsDerBytes})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode TLS  certificate: %v", err)
	}
	tlsCert = tlsCertOut.Bytes()

	keyOut := &bytes.Buffer{}
	block := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}
	err = pem.Encode(keyOut, block)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("encode RSA private key: %v", err)
	}
	privKey := keyOut.Bytes()

	return cert, tlsCert, privKey, nil
}

// Config returns tls.Config to be used in mongo.Config
func Config(k8sclient client.Client, cr *api.PerconaServerMongoDB) (tls.Config, error) {
	secretName := cr.Spec.Secrets.SSL
	if len(secretName) == 0 {
		secretName = cr.Name + "-ssl"
	}
	certSecret := &corev1.Secret{}
	err := k8sclient.Get(context.TODO(), types.NamespacedName{
		Name:      secretName,
		Namespace: cr.Namespace,
	}, certSecret)
	if err != nil {
		return tls.Config{}, errors.Wrap(err, "get ssl certSecret")
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(certSecret.Data["ca.crt"])

	cert, err := tls.X509KeyPair(certSecret.Data["tls.crt"], certSecret.Data["tls.key"])
	if err != nil {
		return tls.Config{}, errors.Wrap(err, "load keypair")
	}

	return tls.Config{
		InsecureSkipVerify: true,
		RootCAs:            pool,
		Certificates:       []tls.Certificate{cert},
	}, nil
}
