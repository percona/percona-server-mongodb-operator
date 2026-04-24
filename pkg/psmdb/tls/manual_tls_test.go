package tls

import (
	"crypto/x509"
	"encoding/pem"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestIssueCA(t *testing.T) {
	caCert, caKey, err := IssueCA()
	require.NoError(t, err)
	require.NotEmpty(t, caCert)
	require.NotEmpty(t, caKey)

	// Parse and verify CA certificate
	block, _ := pem.Decode(caCert)
	require.NotNil(t, block, "failed to decode CA cert PEM")

	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.True(t, cert.IsCA)
	assert.Equal(t, "Root CA", cert.Subject.Organization[0])
	assert.True(t, cert.KeyUsage&x509.KeyUsageCertSign != 0)

	// Verify CA key is valid PEM
	keyBlock, _ := pem.Decode(caKey)
	require.NotNil(t, keyBlock, "failed to decode CA key PEM")
	assert.Equal(t, "RSA PRIVATE KEY", keyBlock.Type)

	_, err = x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	require.NoError(t, err)
}

func TestIssueWithCA(t *testing.T) {
	caCert, caKey, err := IssueCA()
	require.NoError(t, err)

	hosts := []string{"mongo-0.example.com", "mongo-1.example.com", "localhost"}

	tlsCert, tlsKey, err := IssueWithCA(hosts, caCert, caKey)
	require.NoError(t, err)
	require.NotEmpty(t, tlsCert)
	require.NotEmpty(t, tlsKey)

	// Parse TLS cert
	block, _ := pem.Decode(tlsCert)
	require.NotNil(t, block)
	cert, err := x509.ParseCertificate(block.Bytes)
	require.NoError(t, err)

	assert.False(t, cert.IsCA)
	assert.Equal(t, "PSMDB", cert.Subject.Organization[0])

	sort.Strings(cert.DNSNames)
	sort.Strings(hosts)
	assert.Equal(t, hosts, cert.DNSNames)

	// Verify TLS cert is signed by the CA
	caBlock, _ := pem.Decode(caCert)
	require.NotNil(t, caBlock)
	caCertParsed, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCertParsed)

	_, err = cert.Verify(x509.VerifyOptions{
		Roots: pool,
	})
	assert.NoError(t, err, "TLS cert should be verifiable by its CA")
}

func TestIssueWithCA_ReSigningSameCA(t *testing.T) {
	caCert, caKey, err := IssueCA()
	require.NoError(t, err)

	// Issue first cert
	hosts1 := []string{"mongo-0.example.com", "mongo-1.example.com"}
	tlsCert1, _, err := IssueWithCA(hosts1, caCert, caKey)
	require.NoError(t, err)

	// Issue second cert with different SANs (simulating splitHorizon addition)
	hosts2 := []string{"mongo-0.example.com", "mongo-1.example.com", "external.example.com"}
	tlsCert2, _, err := IssueWithCA(hosts2, caCert, caKey)
	require.NoError(t, err)

	// Both certs should be verifiable by the same CA
	caBlock, _ := pem.Decode(caCert)
	require.NotNil(t, caBlock)
	caCertParsed, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err)

	pool := x509.NewCertPool()
	pool.AddCert(caCertParsed)

	opts := x509.VerifyOptions{Roots: pool}

	block1, _ := pem.Decode(tlsCert1)
	cert1, err := x509.ParseCertificate(block1.Bytes)
	require.NoError(t, err)
	_, err = cert1.Verify(opts)
	assert.NoError(t, err, "first cert should be verifiable by CA")

	block2, _ := pem.Decode(tlsCert2)
	cert2, err := x509.ParseCertificate(block2.Bytes)
	require.NoError(t, err)
	_, err = cert2.Verify(opts)
	assert.NoError(t, err, "second cert (re-signed) should be verifiable by same CA")

	// Verify SANs differ
	sort.Strings(cert1.DNSNames)
	sort.Strings(cert2.DNSNames)
	assert.NotEqual(t, cert1.DNSNames, cert2.DNSNames)

	expectedHosts2 := make([]string, len(hosts2))
	copy(expectedHosts2, hosts2)
	sort.Strings(expectedHosts2)
	assert.Equal(t, expectedHosts2, cert2.DNSNames)
}

func TestIssueWithCA_InvalidInputs(t *testing.T) {
	caCert, caKey, err := IssueCA()
	require.NoError(t, err)

	t.Run("invalid CA cert", func(t *testing.T) {
		_, _, err := IssueWithCA([]string{"host"}, []byte("not-a-cert"), caKey)
		assert.Error(t, err)
	})

	t.Run("invalid CA key", func(t *testing.T) {
		_, _, err := IssueWithCA([]string{"host"}, caCert, []byte("not-a-key"))
		assert.Error(t, err)
	})

	t.Run("empty inputs", func(t *testing.T) {
		_, _, err := IssueWithCA([]string{"host"}, nil, nil)
		assert.Error(t, err)
	})
}

func TestGetSANsFromCert(t *testing.T) {
	hosts := []string{"localhost", "mongo-0.example.com", "*.mongo.ns"}

	caCert, caKey, err := IssueCA()
	require.NoError(t, err)

	tlsCert, _, err := IssueWithCA(hosts, caCert, caKey)
	require.NoError(t, err)

	sans, err := GetSANsFromCert(tlsCert)
	require.NoError(t, err)

	sort.Strings(sans)
	sort.Strings(hosts)
	assert.Equal(t, hosts, sans)
}

func TestGetSANsFromCert_InvalidInput(t *testing.T) {
	_, err := GetSANsFromCert([]byte("not-a-cert"))
	assert.Error(t, err)

	_, err = GetSANsFromCert(nil)
	assert.Error(t, err)
}

func TestIssueBackwardCompat(t *testing.T) {
	hosts := []string{"localhost", "mongo-0.example.com"}

	caCert, tlsCert, tlsKey, err := Issue(hosts)
	require.NoError(t, err)
	require.NotEmpty(t, caCert)
	require.NotEmpty(t, tlsCert)
	require.NotEmpty(t, tlsKey)

	// CA should be valid
	caBlock, _ := pem.Decode(caCert)
	require.NotNil(t, caBlock)
	ca, err := x509.ParseCertificate(caBlock.Bytes)
	require.NoError(t, err)
	assert.True(t, ca.IsCA)

	// TLS cert should be signed by the CA
	pool := x509.NewCertPool()
	pool.AddCert(ca)

	tlsBlock, _ := pem.Decode(tlsCert)
	require.NotNil(t, tlsBlock)
	cert, err := x509.ParseCertificate(tlsBlock.Bytes)
	require.NoError(t, err)

	_, err = cert.Verify(x509.VerifyOptions{Roots: pool})
	assert.NoError(t, err)

	sort.Strings(cert.DNSNames)
	sort.Strings(hosts)
	assert.Equal(t, hosts, cert.DNSNames)
}

func TestManualCASecretName(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name: "my-cluster",
		},
	}
	assert.Equal(t, "my-cluster-ca-cert", ManualCASecretName(cr))
}
