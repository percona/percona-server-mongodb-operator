package perconaservermongodb

import (
	"bytes"
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

func (r *ReconcilePerconaServerMongoDB) reconsileSSL(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	secretInternalObj := corev1.Secret{}
	errSecret := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      api.SSLSecretName(cr),
		},
		&secretObj,
	)
	errInternalSecret := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      api.SSLInternalSecretName(cr),
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
	c := r.newCertManagerCtrlFunc(r.client, r.scheme, true)
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
	log := logf.FromContext(ctx).WithName("createSSLByCertManager")

	dryController := r.newCertManagerCtrlFunc(r.client, r.scheme, true)
	// checking if certificates will be updated
	applyStatus, err := r.applyCertManagerCertificates(ctx, cr, dryController)
	if err != nil {
		return errors.Wrap(err, "apply cert-manager certificates")
	}

	if applyStatus == util.ApplyStatusUnchanged {
		secretNames := []string{
			api.SSLSecretName(cr),
			api.SSLInternalSecretName(cr),
		}
		// We should be sure that old CA is merged.
		// mergeNewCA will delete old secrets if they are not needed.
		for _, name := range secretNames {
			_, err := r.getSecret(ctx, cr, name+"-old")
			if client.IgnoreNotFound(err) != nil {
				return errors.Wrap(err, "get secret")
			}
			if err != nil {
				continue
			}
			log.Info("Old secret exists, merging ca", "secret", name+"-old")
			if err := r.mergeNewCA(ctx, cr); err != nil {
				return errors.Wrap(err, "update secrets with old ones")
			}
			return nil
		}

		// If we have merged the old CA and all sts are ready,
		// we should recreate the secrets by deleting them.
		uptodate, err := r.isAllSfsUpToDate(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "check sfs")
		}
		if uptodate {
			caSecret, err := r.getSecret(ctx, cr, tls.CACertificateSecretName(cr))
			if err != nil {
				if k8serr.IsNotFound(err) {
					return nil
				}
				return errors.Wrap(err, "failed to get ca secret")
			}

			for _, name := range secretNames {
				secret, err := r.getSecret(ctx, cr, name)
				if err != nil {
					if k8serr.IsNotFound(err) {
						continue
					}
					return errors.Wrap(err, "get secret")
				}

				if bytes.Equal(secret.Data["ca.crt"], caSecret.Data["ca.crt"]) {
					continue
				}

				log.Info("CA is not up to date. Recreating secret", "secret", secret.Name)
				if err := r.client.Delete(ctx, secret); err != nil {
					return err
				}
			}
		}

		return nil
	}

	log.Info("updating cert-manager certificates")

	if err := r.updateCertManagerCerts(ctx, cr); err != nil {
		return errors.Wrap(err, "update cert mangager certs")
	}

	c := r.newCertManagerCtrlFunc(r.client, r.scheme, false)
	if cr.CompareVersion("1.15.0") >= 0 {
		if err := c.DeleteDeprecatedIssuerIfExists(ctx, cr); err != nil {
			return errors.Wrap(err, "delete deprecated issuer")
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) getSecret(ctx context.Context, cr *api.PerconaServerMongoDB, name string) (*corev1.Secret, error) {
	secret := new(corev1.Secret)
	err := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      name,
		},
		secret,
	)
	if err != nil {
		return nil, err
	}
	return secret, nil
}

func (r *ReconcilePerconaServerMongoDB) updateCertManagerCerts(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	secrets := []string{
		api.SSLSecretName(cr),
		api.SSLInternalSecretName(cr),
	}
	log.Info("Creating old secrets")
	for _, name := range secrets {
		secret, err := r.getSecret(ctx, cr, name)
		if err != nil {
			if k8serr.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get secret")
		}
		newSecret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secret.Name + "-old",
				Namespace: secret.Namespace,
			},
			Data: secret.Data,
		}

		if err := r.client.Create(ctx, newSecret); err != nil {
			return errors.Wrap(err, "create secret")
		}
	}

	c := r.newCertManagerCtrlFunc(r.client, r.scheme, false)
	log.Info("applying new certificates")
	if _, err := r.applyCertManagerCertificates(ctx, cr, c); err != nil {
		return errors.Wrap(err, "failed to apply cert-manager certificates")
	}

	log.Info("migrating new ca")
	if err := r.mergeNewCA(ctx, cr); err != nil {
		return errors.Wrap(err, "update secrets with old ones")
	}
	return nil
}

// mergeNewCA overwrites current ssl secrets with the old ones, but merges ca.crt from the current secret
func (r *ReconcilePerconaServerMongoDB) mergeNewCA(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)
	c := tls.NewCertManagerController(r.client, r.scheme, false)
	// In versions 1.14.0 and below, these secrets contained different ca.crt
	oldCA, err := c.GetMergedCA(ctx, cr, []string{
		api.SSLSecretName(cr) + "-old",
		api.SSLInternalSecretName(cr) + "-old",
	})
	if err != nil {
		return errors.Wrap(err, "get old ca")
	}
	if len(oldCA) == 0 {
		return nil
	}

	secretNames := []string{
		api.SSLSecretName(cr),
		api.SSLInternalSecretName(cr),
	}

	newCA, err := c.GetMergedCA(ctx, cr, secretNames)
	if err != nil {
		return errors.Wrap(err, "get new ca")
	}

	for _, secretName := range secretNames {
		secret, err := r.getSecret(ctx, cr, secretName)
		if err != nil {
			if k8serr.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get ca secret")
		}
		oldSecret, err := r.getSecret(ctx, cr, secretName+"-old")
		if err != nil {
			if k8serr.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get ca secret")
		}

		mergedCA, err := tls.MergePEM(oldSecret.Data["ca.crt"], oldCA, newCA)
		if err != nil {
			return errors.Wrap(err, "failed to merge ca")
		}

		// If secret was already updated, we should delete the old one
		if bytes.Equal(mergedCA, secret.Data["ca.crt"]) {
			if err := r.client.Delete(ctx, oldSecret); err != nil {
				return err
			}
			log.Info("new ca is already in secret, deleting old secret")
			continue
		}

		secret.Data = oldSecret.Data
		secret.Data["ca.crt"] = mergedCA

		if err := r.client.Update(ctx, secret); err != nil {
			return errors.Wrap(err, "update ca secret")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) applyCertManagerCertificates(ctx context.Context, cr *api.PerconaServerMongoDB, c tls.CertManagerController) (util.ApplyStatus, error) {
	applyStatus := util.ApplyStatusUnchanged
	applyFunc := func(f func() (util.ApplyStatus, error)) error {
		status, err := f()
		if err != nil {
			return err
		}
		if status != util.ApplyStatusUnchanged {
			applyStatus = status
		}
		return nil
	}
	if cr.CompareVersion("1.15.0") >= 0 {
		err := applyFunc(func() (util.ApplyStatus, error) {
			return c.ApplyCAIssuer(ctx, cr)
		})
		if err != nil {
			return "", errors.Wrap(err, "apply ca issuer")
		}

		err = applyFunc(func() (util.ApplyStatus, error) {
			return c.ApplyCACertificate(ctx, cr)
		})
		if err != nil {
			return "", errors.Wrap(err, "create ca certificate")
		}

		err = c.WaitForCerts(ctx, cr, tls.CACertificateSecretName(cr))
		if err != nil {
			return "", errors.Wrap(err, "failed to wait for ca cert")
		}
	}

	err := applyFunc(func() (util.ApplyStatus, error) {
		return c.ApplyIssuer(ctx, cr)
	})
	if err != nil {
		return "", errors.Wrap(err, "create issuer")
	}

	err = applyFunc(func() (util.ApplyStatus, error) {
		return c.ApplyCertificate(ctx, cr, false)
	})
	if err != nil {
		return "", errors.Wrap(err, "create certificate")
	}

	secretNames := []string{tls.CertificateSecretName(cr, false)}

	if tls.CertificateSecretName(cr, false) != tls.CertificateSecretName(cr, true) {
		err = applyFunc(func() (util.ApplyStatus, error) {
			return c.ApplyCertificate(ctx, cr, true)
		})
		if err != nil {
			return "", errors.Wrap(err, "create certificate")
		}
		secretNames = append(secretNames, tls.CertificateSecretName(cr, true))
	}

	err = c.WaitForCerts(ctx, cr, secretNames...)
	if err != nil {
		return "", errors.Wrap(err, "failed to wait for certs")
	}
	return applyStatus, nil
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
			Name:            api.SSLSecretName(cr),
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
			Name:            api.SSLInternalSecretName(cr),
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
