package tls

import (
	"context"
	"time"

	cm "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/jetstack/cert-manager/pkg/apis/meta/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

type CertManagerController struct {
	cl     client.Client
	scheme *runtime.Scheme
}

func NewCertManagerController(cl client.Client, scheme *runtime.Scheme) *CertManagerController {
	return &CertManagerController{
		cl:     cl,
		scheme: scheme,
	}
}

func certificateName(cr *api.PerconaServerMongoDB, internal bool) string {
	if internal {
		return cr.Name + "-ssl-internal"
	}
	return cr.Name + "-ssl"
}

func CertificateSecretName(cr *api.PerconaServerMongoDB, internal bool) string {
	if internal {
		return cr.Spec.Secrets.SSLInternal
	}

	return cr.Spec.Secrets.SSL
}

func issuerName(cr *api.PerconaServerMongoDB) string {
	if cr.CompareVersion("1.15.0") < 0 {
		return cr.Name + "-psmdb-ca"
	}
	return cr.Name + "-psmdb-issuer"
}

func caIssuerName(cr *api.PerconaServerMongoDB) string {
	return cr.Name + "-psmdb-ca-issuer"
}

func CACertificateSecretName(cr *api.PerconaServerMongoDB) string {
	return cr.Name + "-ca-cert"
}

func (c *CertManagerController) create(ctx context.Context, cr *api.PerconaServerMongoDB, obj client.Object) error {
	if err := controllerutil.SetControllerReference(cr, obj, c.scheme); err != nil {
		return errors.Wrap(err, "set controller reference")
	}
	return c.cl.Create(ctx, obj)
}

func (c *CertManagerController) CreateIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	issuer := &cm.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      issuerName(cr),
			Namespace: cr.Namespace,
		},
		Spec: cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				CA: &cm.CAIssuer{
					SecretName: CACertificateSecretName(cr),
				},
			},
		},
	}

	if cr.CompareVersion("1.15.0") < 0 {
		issuer.Spec = cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				SelfSigned: &cm.SelfSignedIssuer{},
			},
		}
	}

	return c.create(ctx, cr, issuer)
}

func (c *CertManagerController) CreateCAIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	issuer := &cm.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      caIssuerName(cr),
			Namespace: cr.Namespace,
		},
		Spec: cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				SelfSigned: &cm.SelfSignedIssuer{},
			},
		},
	}

	return c.create(ctx, cr, issuer)
}

func (c *CertManagerController) CreateCertificate(ctx context.Context, cr *api.PerconaServerMongoDB, internal bool) error {
	isCA := false
	if cr.CompareVersion("1.15.0") < 0 {
		isCA = true
	}

	certificate := &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      certificateName(cr, internal),
			Namespace: cr.Namespace,
		},
		Spec: cm.CertificateSpec{
			Subject: &cm.X509Subject{
				Organizations: []string{"PSMDB"},
			},
			CommonName: cr.Name,
			SecretName: CertificateSecretName(cr, internal),
			DNSNames:   GetCertificateSans(cr),
			IsCA:       isCA,
			Duration:   &cr.Spec.TLS.CertValidityDuration,
			IssuerRef: cmmeta.ObjectReference{
				Name: issuerName(cr),
				Kind: cm.IssuerKind,
			},
		},
	}

	return c.create(ctx, cr, certificate)
}

func (c *CertManagerController) CreateCACertificate(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	cert := &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      CACertificateSecretName(cr),
			Namespace: cr.Namespace,
		},
		Spec: cm.CertificateSpec{
			SecretName: CACertificateSecretName(cr),
			CommonName: cr.Name + "-ca",
			IsCA:       true,
			IssuerRef: cmmeta.ObjectReference{
				Name: caIssuerName(cr),
				Kind: cm.IssuerKind,
			},
			Duration:    &metav1.Duration{Duration: time.Hour * 24 * 365},
			RenewBefore: &metav1.Duration{Duration: 730 * time.Hour},
		},
	}

	return c.create(ctx, cr, cert)
}

func (c *CertManagerController) WaitForCerts(ctx context.Context, cr *api.PerconaServerMongoDB, secretsList ...string) error {
	ticker := time.NewTicker(1 * time.Second)
	timeoutTimer := time.NewTimer(30 * time.Second)
	defer timeoutTimer.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-timeoutTimer.C:
			return errors.Errorf("timeout: can't get tls certificates from certmanager, %s", secretsList)
		case <-ticker.C:
			successCount := 0
			for _, secretName := range secretsList {
				secret := &corev1.Secret{}
				err := c.cl.Get(ctx, types.NamespacedName{
					Name:      secretName,
					Namespace: cr.Namespace,
				}, secret)
				if err != nil && !k8serrors.IsNotFound(err) {
					return err
				} else if err == nil {
					successCount++
					if len(secret.OwnerReferences) == 0 {
						if err = controllerutil.SetControllerReference(cr, secret, c.scheme); err != nil {
							return errors.Wrap(err, "set controller reference")
						}
						if err = c.cl.Update(ctx, secret); err != nil {
							return errors.Wrap(err, "failed to update secret")
						}
					}
				}
			}
			if successCount == len(secretsList) {
				return nil
			}
		}
	}
}
