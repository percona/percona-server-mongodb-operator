package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

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
	if errSecret == nil && k8serr.IsNotFound(errInternalSecret) && !metav1.IsControlledBy(&secretObj, cr) {
		// don't create secret ssl-internal if secret ssl is not created by operator
		return nil
	} else if errSecret != nil && !k8serr.IsNotFound(errSecret) {
		return errors.Wrap(errSecret, "get SSL secret")
	} else if errInternalSecret != nil && !k8serr.IsNotFound(errInternalSecret) {
		return errors.Wrap(errInternalSecret, "get internal SSL secret")
	}

	ok, err := r.isCertManagerInstalled(ctx, cr.Namespace)
	if err != nil {
		return errors.Wrap(err, "check cert-manager")
	}
	if !ok {
		if errSecret == nil && errInternalSecret == nil {
			return nil
		}
		err = r.createSSLManually(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "create ssl manually")
		}
		return nil
	}
	err = r.createSSLByCertManager(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "create ssl by cert-manager")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) isCertManagerInstalled(ctx context.Context, ns string) (bool, error) {
	c := tls.NewCertManagerController(r.client, r.scheme)
	err := c.Check(ctx, r.restConfig, ns)
	if err != nil {
		switch err {
		case tls.ErrCertManagerNotFound:
			return false, nil
		case tls.ErrCertManagerNotReady:
			return true, nil
		}
		return false, err
	}
	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) createSSLByCertManager(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	c := tls.NewCertManagerController(r.client, r.scheme)

	if cr.CompareVersion("1.15.0") >= 0 {
		err := c.ApplyCAIssuer(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "create ca issuer")
		}

		err = c.ApplyCACertificate(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "create ca certificate")
		}

		err = c.WaitForCerts(ctx, cr, tls.CACertificateSecretName(cr))
		if err != nil {
			return errors.Wrap(err, "failed to wait for ca cert")
		}
	}

	err := c.ApplyIssuer(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "create issuer")
	}

	err = c.ApplyCertificate(ctx, cr, false)
	if err != nil {
		return errors.Wrap(err, "create certificate")
	}

	secretNames := []string{tls.CertificateSecretName(cr, false)}

	if tls.CertificateSecretName(cr, false) != tls.CertificateSecretName(cr, true) {
		err = c.ApplyCertificate(ctx, cr, true)
		if err != nil && !k8serr.IsAlreadyExists(err) {
			return errors.Wrap(err, "create certificate")
		}
		secretNames = append(secretNames, tls.CertificateSecretName(cr, true))
	}

	err = c.WaitForCerts(ctx, cr, secretNames...)
	if err != nil {
		return errors.Wrap(err, "failed to wait for certs")
	}

	if cr.CompareVersion("1.15.0") >= 0 {
		err = c.DeleteDeprecatedIssuerIfExists(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "delete deprecated issuer")
		}
	}

	return nil
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
