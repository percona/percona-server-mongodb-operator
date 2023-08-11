package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
)

func (r *ReconcilePerconaServerMongoDB) reconsileSSL(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	secretInternalObj := corev1.Secret{}
	errSecret := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.SSL,
		},
		&secretObj,
	)
	errInternalSecret := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.SSL + "-internal",
		},
		&secretInternalObj,
	)
	if errSecret == nil && errInternalSecret == nil {
		return nil
	} else if errSecret == nil && k8serr.IsNotFound(errInternalSecret) && !metav1.IsControlledBy(&secretObj, cr) {
		// don't create secret ssl-internal if secret ssl is not created by operator
		return nil
	} else if errSecret != nil && !k8serr.IsNotFound(errSecret) {
		return errors.Wrap(errSecret, "get SSL secret")
	} else if errInternalSecret != nil && !k8serr.IsNotFound(errInternalSecret) {
		return errors.Wrap(errInternalSecret, "get internal SSL secret")
	}

	err := r.createSSLByCertManager(ctx, cr)
	if err != nil {
		logf.FromContext(ctx).Error(err, "issue cert with cert-manager")
		err = r.createSSLManually(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "create ssl manually")
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createSSLByCertManager(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	c := tls.NewCertManagerController(r.client, r.scheme)

	if cr.CompareVersion("1.15.0") >= 0 {
		err := c.CreateCAIssuer(ctx, cr)
		if err != nil && !k8serr.IsAlreadyExists(err) {
			return errors.Wrap(err, "create certificate")
		}

		err = c.CreateCACertificate(ctx, cr)
		if err != nil && !k8serr.IsAlreadyExists(err) {
			return errors.Wrap(err, "create ca certificate")
		}
	}

	err := c.CreateIssuer(ctx, cr)
	if err != nil && !k8serr.IsAlreadyExists(err) {
		return errors.Wrap(err, "create issuer")
	}

	err = c.CreateCertificate(ctx, cr, false)
	if err != nil && !k8serr.IsAlreadyExists(err) {
		return errors.Wrap(err, "create certificate")
	}

	if tls.CertificateSecretName(cr, false) == tls.CertificateSecretName(cr, true) {
		return c.WaitForCerts(ctx, cr, tls.CertificateSecretName(cr, false))
	}

	err = c.CreateCertificate(ctx, cr, true)
	if err != nil && !k8serr.IsAlreadyExists(err) {
		return errors.Wrap(err, "create certificate")
	}

	return c.WaitForCerts(ctx, cr, tls.CertificateSecretName(cr, false), tls.CertificateSecretName(cr, true))
}

func (r *ReconcilePerconaServerMongoDB) createSSLManually(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	data := make(map[string][]byte)
	certificateDNSNames := tls.GetCertificateSans(cr)

	caCert, tlsCert, key, err := tls.Issue(certificateDNSNames)
	if err != nil {
		return errors.Wrap(err, "create proxy certificate")
	}
	data["ca.crt"] = caCert
	data["tls.crt"] = tlsCert
	data["tls.key"] = key

	owner, err := OwnerRef(cr, r.scheme)
	if err != nil {
		return err
	}
	ownerReferences := []metav1.OwnerReference{owner}

	secretObj := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cr.Spec.Secrets.SSL,
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
		},
		Data: data,
		Type: corev1.SecretTypeTLS,
	}
	err = r.createSSLSecret(ctx, &secretObj, certificateDNSNames)
	if err != nil {
		return errors.Wrap(err, "create TLS secret")
	}

	caCert, tlsCert, key, err = tls.Issue(certificateDNSNames)
	if err != nil {
		return errors.Wrap(err, "create psmdb certificate")
	}
	data["ca.crt"] = caCert
	data["tls.crt"] = tlsCert
	data["tls.key"] = key
	secretObjInternal := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cr.Spec.Secrets.SSLInternal,
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
		},
		Data: data,
		Type: corev1.SecretTypeTLS,
	}
	err = r.createSSLSecret(ctx, &secretObjInternal, certificateDNSNames)
	if err != nil {
		return errors.Wrap(err, "create TLS internal secret")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createSSLSecret(ctx context.Context, secret *corev1.Secret, DNSNames []string) error {
	oldSecret := new(corev1.Secret)

	err := r.client.Get(ctx, types.NamespacedName{
		Name:      secret.GetName(),
		Namespace: secret.GetNamespace(),
	}, oldSecret)

	if err != nil && !k8serr.IsNotFound(err) {
		return errors.Wrap(err, "get object")
	}

	if k8serr.IsNotFound(err) {
		return r.client.Create(ctx, secret)
	}

	return nil
}
