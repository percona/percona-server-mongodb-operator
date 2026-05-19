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
	"slices"
	"time"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

var validityNotAfter = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

func IsSecretCreatedByUser(ctx context.Context, c client.Client, cr *api.PerconaServerMongoDB, secret *corev1.Secret) (bool, error) {
	if metav1.IsControlledBy(secret, cr) {
		return false, nil
	}
	if secret.Labels[cm.PartOfCertManagerControllerLabelKey] == "true" {
		return isCertManagerSecretCreatedByUser(ctx, c, cr, secret)
	}
	return true, nil
}

func isCertManagerSecretCreatedByUser(ctx context.Context, c client.Client, cr *api.PerconaServerMongoDB, secret *corev1.Secret) (bool, error) {
	if metav1.IsControlledBy(secret, cr) {
		return false, nil
	}

	issuerName := secret.Annotations[cm.IssuerNameAnnotationKey]
	if secret.Annotations[cm.IssuerKindAnnotationKey] != cm.IssuerKind || issuerName == "" {
		return true, nil
	}
	issuer := new(cm.Issuer)
	if err := c.Get(ctx, types.NamespacedName{
		Name:      issuerName,
		Namespace: secret.Namespace,
	}, issuer); err != nil {
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		return true, errors.Wrap(err, "failed to get issuer")
	}
	if metav1.IsControlledBy(issuer, cr) {
		return false, nil
	}
	return true, nil
}

// IssueCA generates a new self-signed CA certificate and returns the CA cert and CA private key in PEM format.
func IssueCA() (caCertPEM []byte, caKeyPEM []byte, err error) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA key: %v", err)
	}
	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, errors.Wrap(err, "generate serial number for CA")
	}
	caTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Root CA"},
		},
		NotBefore:             time.Now(),
		NotAfter:              validityNotAfter,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageCodeSigning},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &caTemplate, &caTemplate, &priv.PublicKey, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("generate CA certificate: %v", err)
	}

	certOut := &bytes.Buffer{}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return nil, nil, fmt.Errorf("encode CA certificate: %v", err)
	}

	keyOut := &bytes.Buffer{}
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		return nil, nil, fmt.Errorf("encode CA private key: %v", err)
	}

	return certOut.Bytes(), keyOut.Bytes(), nil
}

// IssueWithCA generates a TLS certificate signed by the given CA and returns the TLS cert and TLS private key in PEM format.
func IssueWithCA(hosts []string, caCertPEM, caKeyPEM []byte) (tlsCert []byte, tlsKey []byte, err error) {
	caBlock, _ := pem.Decode(caCertPEM)
	if caBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode CA certificate PEM")
	}
	caCert, err := x509.ParseCertificate(caBlock.Bytes)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse CA certificate")
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, nil, fmt.Errorf("failed to decode CA private key PEM")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, nil, errors.Wrap(err, "parse CA private key")
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, errors.Wrap(err, "generate serial number for TLS cert")
	}

	tlsTemplate := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"PSMDB"},
		},
		Issuer: pkix.Name{
			Organization: []string{"Root CA"},
		},
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
		return nil, nil, errors.Wrap(err, "generate TLS key")
	}

	tlsDerBytes, err := x509.CreateCertificate(rand.Reader, &tlsTemplate, caCert, &clientKey.PublicKey, caKey)
	if err != nil {
		return nil, nil, errors.Wrap(err, "generate TLS certificate")
	}

	tlsCertOut := &bytes.Buffer{}
	if err := pem.Encode(tlsCertOut, &pem.Block{Type: "CERTIFICATE", Bytes: tlsDerBytes}); err != nil {
		return nil, nil, fmt.Errorf("encode TLS certificate: %v", err)
	}

	keyOut := &bytes.Buffer{}
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(clientKey)}); err != nil {
		return nil, nil, fmt.Errorf("encode TLS private key: %v", err)
	}

	return tlsCertOut.Bytes(), keyOut.Bytes(), nil
}

// Issue returns CA certificate, TLS certificate and TLS private key
func Issue(hosts []string) (caCert []byte, tlsCert []byte, tlsKey []byte, err error) {
	caCertPEM, caKeyPEM, err := IssueCA()
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "issue CA")
	}

	tlsCertPEM, tlsKeyPEM, err := IssueWithCA(hosts, caCertPEM, caKeyPEM)
	if err != nil {
		return nil, nil, nil, errors.Wrap(err, "issue TLS cert")
	}

	return caCertPEM, tlsCertPEM, tlsKeyPEM, nil
}

// GetSANsFromCert extracts DNS SANs from a PEM-encoded TLS certificate.
func GetSANsFromCert(tlsCertPEM []byte) ([]string, error) {
	block, _ := pem.Decode(tlsCertPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode TLS certificate PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.Wrap(err, "parse TLS certificate")
	}
	return cert.DNSNames, nil
}

// ManualCASecretName returns the name of the CA secret for manual TLS management.
func ManualCASecretName(cr *api.PerconaServerMongoDB) string {
	return cr.Name + "-ca-cert"
}

// Config returns tls.Config to be used in mongo.Config
func Config(ctx context.Context, k8sclient client.Client, cr *api.PerconaServerMongoDB) (tls.Config, error) {
	secretName := api.SSLSecretName(cr)
	certSecret := &corev1.Secret{}
	err := k8sclient.Get(ctx, types.NamespacedName{
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

func getShardingSans(cr *api.PerconaServerMongoDB) []string {
	sans := []string{
		cr.Name + "-mongos",
		cr.Name + "-mongos" + "." + cr.Namespace,
		cr.Name + "-mongos" + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
		"*." + cr.Name + "-mongos",
		"*." + cr.Name + "-mongos" + "." + cr.Namespace,
		"*." + cr.Name + "-mongos" + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
		cr.Name + "-" + api.ConfigReplSetName,
		cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace,
		cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
		"*." + cr.Name + "-" + api.ConfigReplSetName,
		"*." + cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace,
		"*." + cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
		cr.Name + "-mongos" + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
		"*." + cr.Name + "-mongos" + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
		cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
		"*." + cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
	}
	return sans
}

func GetCertificateSans(cr *api.PerconaServerMongoDB) []string {
	sans := []string{"localhost"}
	for _, replset := range cr.Spec.Replsets {
		sans = append(sans, []string{
			cr.Name + "-" + replset.Name,
			cr.Name + "-" + replset.Name + "." + cr.Namespace,
			cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
			"*." + cr.Name + "-" + replset.Name,
			"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace,
			"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
			cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
			"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
		}...)

		if cr.CompareVersion("1.22.0") >= 0 && len(replset.Horizons) > 0 {
			horizonSans := make(map[string]struct{})
			for _, podMap := range replset.GetHorizons(false) {
				for _, domain := range podMap {
					horizonSans[domain] = struct{}{}
				}
			}
			var uniqueHorizonSans []string
			for domain := range horizonSans {
				uniqueHorizonSans = append(uniqueHorizonSans, domain)
			}
			slices.Sort(uniqueHorizonSans)
			sans = append(sans, uniqueHorizonSans...)
		}
	}
	sans = append(sans, "*."+cr.Namespace+"."+cr.Spec.MultiCluster.DNSSuffix)
	sans = append(sans, getShardingSans(cr)...)

	return sans
}
