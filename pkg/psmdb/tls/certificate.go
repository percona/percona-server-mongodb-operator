package tls

import (
	"time"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

type Certificate interface {
	Name() string
	Namespace() string
	SecretName() string
	Object() *cm.Certificate
}

type caCert struct {
	cr *api.PerconaServerMongoDB
}

func CertificateCA(cr *api.PerconaServerMongoDB) Certificate {
	return &caCert{
		cr: cr,
	}
}

func (c *caCert) Name() string {
	return c.cr.Name + "-ca-cert"
}

func (c *caCert) Namespace() string {
	if c.cr.CompareVersion("1.22.0") >= 0 && c.cr.Spec.TLS != nil && c.cr.Spec.TLS.IssuerConf.Kind == cm.ClusterIssuerKind {
		return certManagerNamespace()
	}

	return c.cr.Namespace
}

func (c *caCert) SecretName() string {
	return c.Name()
}

func (c *caCert) Object() *cm.Certificate {
	cr := c.cr

	labels := naming.ClusterLabels(cr)
	if cr.CompareVersion("1.17.0") < 0 {
		labels = nil
	}

	issuerKind := cm.IssuerKind
	if cr.CompareVersion("1.22.0") >= 0 && cr.Spec.TLS != nil {
		issuerKind = cr.Spec.TLS.IssuerConf.Kind
	}
	return &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name(),
			Namespace: c.Namespace(),
			Labels:    labels,
		},
		Spec: cm.CertificateSpec{
			SecretName: c.SecretName(),
			CommonName: cr.Name + "-ca",
			IsCA:       true,
			IssuerRef: cmmeta.ObjectReference{
				Name: caIssuerName(cr),
				Kind: issuerKind,
			},
			Duration:    &metav1.Duration{Duration: time.Hour * 24 * 365},
			RenewBefore: &metav1.Duration{Duration: 730 * time.Hour},
		},
	}
}

type tlsCert struct {
	cr *api.PerconaServerMongoDB

	internal bool
}

func CertificateTLS(cr *api.PerconaServerMongoDB, internal bool) Certificate {
	return &tlsCert{
		cr:       cr,
		internal: internal,
	}
}

func (c *tlsCert) Name() string {
	if c.internal {
		return c.cr.Name + "-ssl-internal"
	}
	return c.cr.Name + "-ssl"
}

func (c *tlsCert) Namespace() string {
	return c.cr.Namespace
}

func (c *tlsCert) SecretName() string {
	if c.internal {
		return api.SSLInternalSecretName(c.cr)
	}

	return api.SSLSecretName(c.cr)
}

func (c *tlsCert) Object() *cm.Certificate {
	cr := c.cr

	issuerKind := cm.IssuerKind
	issuerGroup := ""
	if cr.Spec.TLS != nil {
		issuerKind = cr.Spec.TLS.IssuerConf.Kind
		issuerGroup = cr.Spec.TLS.IssuerConf.Group
	}

	return &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      c.Name(),
			Namespace: cr.Namespace,
			Labels:    naming.ClusterLabels(cr),
		},
		Spec: cm.CertificateSpec{
			Subject: &cm.X509Subject{
				Organizations: []string{"PSMDB"},
			},
			CommonName: cr.Name,
			SecretName: c.SecretName(),
			DNSNames:   GetCertificateSans(cr),
			IsCA:       false,
			Duration:   &cr.Spec.TLS.CertValidityDuration,
			IssuerRef: cmmeta.ObjectReference{
				Name:  issuerName(cr),
				Kind:  issuerKind,
				Group: issuerGroup,
			},
		},
	}
}
