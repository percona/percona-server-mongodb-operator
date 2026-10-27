package perconaservermongodb

import (
	"context"
	"fmt"
	"slices"
	"sort"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
)

func (r *ReconcilePerconaServerMongoDB) reconcileSSL(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.TLSEnabled() {
		return nil
	}

	secretObj := new(corev1.Secret)
	secretInternalObj := new(corev1.Secret)
	errSecret := r.client.Get(
		ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      api.SSLSecretName(cr),
		},
		secretObj,
	)
	if client.IgnoreNotFound(errSecret) != nil {
		return errors.Wrap(errSecret, "get SSL secret")
	}
	isCustomSecret, serr := tls.IsSecretCreatedByUser(ctx, r.client, cr, secretObj)
	if serr != nil {
		return errors.Wrap(serr, "failed to check if secret is created by user")
	}
	if errSecret != nil {
		secretObj = nil
	}
	errInternalSecret := r.client.Get(
		ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      api.SSLInternalSecretName(cr),
		},
		secretInternalObj,
	)
	if client.IgnoreNotFound(errInternalSecret) != nil {
		return errors.Wrap(errInternalSecret, "get internal SSL secret")
	}
	isCustomSecretInternal, serr := tls.IsSecretCreatedByUser(ctx, r.client, cr, secretInternalObj)
	if serr != nil {
		return errors.Wrap(serr, "failed to check if internal secret is created by user")
	}
	if errInternalSecret != nil {
		secretInternalObj = nil
	}

	if errSecret == nil && isCustomSecret {
		if cr.Spec.Backup.Enabled && cr.CompareVersion("1.24.0") >= 0 {
			name := tls.CertificatePBMClient(cr).SecretName()
			_, err := r.getSecret(ctx, cr, name)
			if k8serrors.IsNotFound(err) {
				return errors.Errorf("PBM client TLS secret %s is required when custom SSL secret %s is provided and backups are enabled", name, api.SSLSecretName(cr))
			}
			if err != nil {
				return errors.Wrap(err, "get PBM client TLS secret")
			}
		}
		// We shouldn't do anything if the user has created a custom ssl secret.
		// We should also allow the use of operator without internal ssl secret, so we only check for non-internal secret here.
		return nil
	}

	// If certManagementPolicy is userProvidedOnly, the operator should not create any certificates automatically.
	if cr.Spec.TLS != nil && cr.Spec.TLS.CertManagementPolicy == api.CertManagementUserProvidedOnly {
		if k8serrors.IsNotFound(errSecret) {
			// The user opted to manage TLS secrets themselves but hasn't created them yet
			// (e.g. during initial cluster creation). Don't bring up the cluster without TLS:
			// wait in init state until the user provides the secret.
			logf.FromContext(ctx).Info("waiting for user-provided TLS secret, certManagementPolicy is userProvidedOnly", "secret", api.SSLSecretName(cr))
			cr.Status.AddCondition(api.ClusterCondition{
				Status:  api.ConditionFalse,
				Type:    api.ConditionTypeTLSSecretsReady,
				Reason:  "TLSSecretNotFound",
				Message: fmt.Sprintf("TLS secret %s is missing, certManagementPolicy is userProvidedOnly", api.SSLSecretName(cr)),
			})
			return errTLSNotReady
		}
		logf.FromContext(ctx).Info("certManagementPolicy is userProvidedOnly, skipping automatic certificate management")
		return nil
	}

	if k8serrors.IsNotFound(errSecret) && errInternalSecret == nil && isCustomSecretInternal {
		// If the user has only created an internal secret, we should create a copy of it as a non-internal secret.
		newSecret := secretInternalObj.DeepCopy()
		newSecret.ObjectMeta = metav1.ObjectMeta{
			Name:                       api.SSLSecretName(cr),
			Namespace:                  secretInternalObj.Namespace,
			DeletionGracePeriodSeconds: secretInternalObj.DeletionGracePeriodSeconds,
			Labels:                     secretInternalObj.Labels,
			Annotations:                secretInternalObj.Annotations,
			OwnerReferences:            secretInternalObj.OwnerReferences,
			Finalizers:                 secretInternalObj.Finalizers,
			ManagedFields:              secretInternalObj.ManagedFields,
		}
		if err := r.client.Create(ctx, newSecret); err != nil {
			return errors.Wrap(err, "failed to create copy of internal secret")
		}
		return nil
	}

	ok, err := r.isCertManagerInstalled(ctx, cr.Namespace, secretObj, secretInternalObj)
	if err != nil {
		return errors.Wrap(err, "check cert-manager")
	}
	if !ok {
		var errPBMSecret error
		if cr.Spec.Backup.Enabled && cr.CompareVersion("1.24.0") >= 0 {
			_, errPBMSecret = r.getSecret(ctx, cr, tls.CertificatePBMClient(cr).SecretName())
			if client.IgnoreNotFound(errPBMSecret) != nil {
				return errors.Wrap(errPBMSecret, "get PBM client TLS secret")
			}
		}
		if errSecret == nil && errInternalSecret == nil && errPBMSecret == nil {
			if r.needsManualSSLUpdate(ctx, cr, secretObj) {
				return r.updateSSLManually(ctx, cr)
			}
			return nil
		}
		if err := r.createSSLManually(ctx, cr); err != nil {
			return errors.Wrap(err, "create ssl manually")
		}
		return nil
	}
	if err := r.createSSLByCertManager(ctx, cr); err != nil {
		return errors.Wrap(err, "create ssl by cert-manager")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) isCertManagerInstalled(ctx context.Context, ns string, secret, internalSecret *corev1.Secret) (bool, error) {
	if secret != nil && internalSecret != nil {
		return secret.Labels[cm.PartOfCertManagerControllerLabelKey] == "true" || internalSecret.Labels[cm.PartOfCertManagerControllerLabelKey] == "true", nil
	}

	c := r.newCertManagerCtrlFunc(r.client, r.scheme, true)
	err := c.Check(ctx, r.restConfig, ns)
	if err != nil {
		switch {
		case errors.Is(err, tls.ErrCertManagerNotFound):
			return false, nil
		case errors.Is(err, tls.ErrCertManagerNotReady):
			return true, nil
		}
		return false, err
	}
	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) doAllStsHasLatestTLS(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	sfsList := appsv1.StatefulSetList{}
	if err := r.client.List(
		ctx, &sfsList,
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				naming.LabelKubernetesInstance: cr.Name,
			}),
		},
	); err != nil {
		return false, errors.Wrap(err, "failed to get statefulset list")
	}

	sslAnn, err := r.sslAnnotation(ctx, cr)
	if err != nil {
		if errors.Is(err, errTLSNotReady) {
			return false, nil
		}
		return false, errors.Wrap(err, "failed to get ssl annotations")
	}
	for _, sts := range sfsList.Items {
		for k, v := range sslAnn {
			if sts.Spec.Template.Annotations[k] != v {
				return false, nil
			}
		}
	}
	return true, nil
}

// updateMongosFirstIfTLSOutdated makes the smart update roll mongos before mongod if mongos pods
// run an older certificate than the one sfs rolls out. With hostname verification on, the operator
// can't reach mongos through hostnames missing from that certificate (e.g. per-pod services right after
// servicePerPod is enabled), while the smart update of the cfg replset needs mongos to stop the balancer.
// The hashes are taken from sfs rather than the TLS secret, so a stale cached secret can't hide the change.
func (r *ReconcilePerconaServerMongoDB) updateMongosFirstIfTLSOutdated(ctx context.Context, cr *api.PerconaServerMongoDB, sfs *appsv1.StatefulSet) error {
	if !mongosNeedsLatestTLS(cr) {
		return nil
	}

	pods, err := r.getMongosPods(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "get mongos pods")
	}

	for _, pod := range pods.Items {
		for _, k := range []string{naming.AnnotationSSLHash, naming.AnnotationSSLInternalHash} {
			if pod.Annotations[k] == sfs.Spec.Template.Annotations[k] {
				continue
			}

			logf.FromContext(ctx).Info("mongos runs an outdated TLS certificate, updating mongos first", "pod", pod.Name, "statefulset", sfs.Name)
			return r.setUpdateMongosFirst(ctx, cr)
		}
	}

	return nil
}

func mongosNeedsLatestTLS(cr *api.PerconaServerMongoDB) bool {
	return cr.Spec.Sharding.Enabled &&
		cr.Spec.Sharding.Mongos.Size > 0 &&
		cr.TLSEnabled() &&
		cr.Spec.UpdateStrategy == api.SmartUpdateStatefulSetStrategyType &&
		!ptr.Deref(cr.Spec.TLS.AllowInvalidCertificates, api.DefaultAllowInvalidCertificates)
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
		// If we have merged the old CA and all sts are ready,
		// we should recreate the secrets by deleting them.
		uptodate, err := r.isAllSfsUpToDate(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "check sfs")
		}
		// These sts should also have latest tls secrets
		hasSSL, err := r.doAllStsHasLatestTLS(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "has ssl")
		}
		if uptodate && hasSSL {
			secretNames := []string{
				api.SSLInternalSecretName(cr),
				api.SSLSecretName(cr),
			}
			managePBMCert, err := r.shouldManagePBMCert(ctx, cr)
			if err != nil {
				return err
			}
			if managePBMCert {
				secretNames = append(secretNames, tls.CertificatePBMClient(cr).SecretName())
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

			externalIssuer, err := r.isExternalIssuer(ctx, cr)
			if err != nil {
				return errors.Wrap(err, "check external issuer")
			}
			if externalIssuer {
				// The CA is owned by the external issuer, the operator must not recreate the secrets.
				return nil
			}

			caCert := tls.CertificateCA(cr)
			caSecret, err := r.getSecretInNamespace(ctx, caCert.Namespace(), caCert.SecretName())
			if err != nil {
				if k8serrors.IsNotFound(err) {
					return nil
				}
				return errors.Wrap(err, "failed to get ca secret")
			}

			for _, name := range secretNames {
				secret, err := r.getSecret(ctx, cr, name)
				if err != nil {
					if k8serrors.IsNotFound(err) {
						continue
					}
					return errors.Wrap(err, "get secret")
				}

				if tls.EqualPEM(secret.Data["ca.crt"], caSecret.Data["ca.crt"]) {
					continue
				}

				// Mongos pods will only accept the first part of the CA.
				// After the secret recreation, all mongod pods will have the last part of the CA
				// and mongos won't be able to connect to them.
				// So we should update the mongos pods before the mongod pods.
				if err := r.setUpdateMongosFirst(ctx, cr); err != nil {
					return errors.Wrap(err, "set update mongos first")
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

	return nil
}

func (r *ReconcilePerconaServerMongoDB) getSecret(ctx context.Context, cr *api.PerconaServerMongoDB, name string) (*corev1.Secret, error) {
	return r.getSecretInNamespace(ctx, cr.Namespace, name)
}

func (r *ReconcilePerconaServerMongoDB) getSecretInNamespace(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	secret := new(corev1.Secret)
	err := r.client.Get(
		ctx,
		types.NamespacedName{
			Namespace: namespace,
			Name:      name,
		},
		secret,
	)
	if err != nil {
		return nil, err
	}
	return secret, nil
}

func (r *ReconcilePerconaServerMongoDB) shouldManagePBMCert(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	if !cr.Spec.Backup.Enabled || cr.CompareVersion("1.24.0") < 0 {
		return false, nil
	}

	secret, err := r.getSecret(ctx, cr, tls.CertificatePBMClient(cr).SecretName())
	if k8serrors.IsNotFound(err) {
		return true, nil
	}
	if err != nil {
		return false, errors.Wrap(err, "get PBM client TLS secret")
	}

	isCustomSecret, err := tls.IsSecretCreatedByUser(ctx, r.client, cr, secret)
	if err != nil {
		return false, errors.Wrap(err, "check PBM client TLS secret ownership")
	}
	return !isCustomSecret, nil
}

func (r *ReconcilePerconaServerMongoDB) updateCertManagerCerts(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	secrets := []string{
		api.SSLSecretName(cr),
		api.SSLInternalSecretName(cr),
	}
	managePBMCert, err := r.shouldManagePBMCert(ctx, cr)
	if err != nil {
		return err
	}
	if managePBMCert {
		secrets = append(secrets, tls.CertificatePBMClient(cr).SecretName())
	}
	log.Info("Creating old secrets")
	for _, name := range secrets {
		secret, err := r.getSecret(ctx, cr, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get secret")
		}
		newSecret := &corev1.Secret{
			Name:      secret.Name + "-old",
			Namespace: secret.Namespace,
			Labels:    naming.ClusterLabels(cr),
			Data:      secret.Data,
		}
		if cr.CompareVersion("1.17.0") < 0 {
			newSecret.Labels = nil
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
	secretNames := []string{api.SSLInternalSecretName(cr), api.SSLSecretName(cr)}
	managePBMCert, err := r.shouldManagePBMCert(ctx, cr)
	if err != nil {
		return err
	}
	if managePBMCert {
		secretNames = append(secretNames, tls.CertificatePBMClient(cr).SecretName())
	}
	oldSecretNames := make([]string, 0, len(secretNames))
	for _, name := range secretNames {
		oldSecretNames = append(oldSecretNames, name+"-old")
	}
	c := tls.NewCertManagerController(r.client, r.scheme, false)
	oldCA, err := c.GetMergedCA(ctx, cr, oldSecretNames)
	if err != nil {
		return errors.Wrap(err, "get old ca")
	}
	if len(oldCA) == 0 {
		return nil
	}
	newCA, err := c.GetMergedCA(ctx, cr, secretNames)
	if err != nil {
		return errors.Wrap(err, "get new ca")
	}
	mergedCA, err := tls.MergePEM(oldCA, newCA)
	if err != nil {
		return errors.Wrap(err, "failed to merge ca")
	}

	for _, secretName := range secretNames {
		secret, err := r.getSecret(ctx, cr, secretName)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get ca secret")
		}
		oldSecret, err := r.getSecret(ctx, cr, secretName+"-old")
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get ca secret")
		}

		// If secret was already updated, we should delete the old one
		if tls.EqualPEM(mergedCA, secret.Data["ca.crt"]) {
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

// isExternalIssuer reports whether the certificates are issued by an issuer the operator doesn't manage.
func (r *ReconcilePerconaServerMongoDB) isExternalIssuer(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	if tls.IsExternalIssuer(cr) {
		if cr.Spec.TLS.IssuerConf.Name == "" {
			return false, errors.New("external issuer requires tls.issuerConf.name")
		}
		return true, nil
	}

	userIssuer, err := tls.IsUserIssuer(ctx, r.client, cr)
	if err != nil {
		return false, errors.Wrap(err, "check user issuer")
	}

	return userIssuer, nil
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

	externalIssuer, err := r.isExternalIssuer(ctx, cr)
	if err != nil {
		return "", err
	}

	if !externalIssuer {
		if err := applyFunc(func() (util.ApplyStatus, error) {
			return c.ApplyCAIssuer(ctx, cr)
		}); err != nil {
			return "", errors.Wrap(err, "apply ca issuer")
		}

		caCert := tls.CertificateCA(cr)

		if err := applyFunc(func() (util.ApplyStatus, error) {
			return c.ApplyCertificate(ctx, cr, caCert)
		}); err != nil {
			return "", errors.Wrap(err, "create ca certificate")
		}

		if err := c.WaitForCerts(ctx, cr, caCert); err != nil {
			return "", errors.Wrap(err, "failed to wait for ca cert")
		}

		if err := applyFunc(func() (util.ApplyStatus, error) {
			return c.ApplyIssuer(ctx, cr)
		}); err != nil {
			return "", errors.Wrap(err, "create issuer")
		}
	}

	tlsCert := tls.CertificateTLS(cr, false)
	certificates := []tls.Certificate{tlsCert}
	if internalCert := tls.CertificateTLS(cr, true); tlsCert.SecretName() != internalCert.SecretName() {
		certificates = append(certificates, internalCert)
	}
	managePBMCert, err := r.shouldManagePBMCert(ctx, cr)
	if err != nil {
		return "", err
	}
	if managePBMCert {
		certificates = append(certificates, tls.CertificatePBMClient(cr))
	}
	for _, cert := range certificates {
		if err := applyFunc(func() (util.ApplyStatus, error) {
			return c.ApplyCertificate(ctx, cr, cert)
		}); err != nil {
			return "", errors.Wrapf(err, "create certificate %s", cert.Name())
		}
	}
	if err := c.WaitForCerts(ctx, cr, certificates...); err != nil {
		return "", errors.Wrap(err, "failed to wait for certs")
	}

	return applyStatus, nil
}

func (r *ReconcilePerconaServerMongoDB) createSSLManually(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	certificateDNSNames := tls.GetCertificateSans(cr)

	owner, err := OwnerRef(cr, r.scheme)
	if err != nil {
		return err
	}
	ownerReferences := []metav1.OwnerReference{owner}

	// Get or create CA secret
	caCertPEM, caKeyPEM, err := r.getOrCreateManualCA(ctx, cr, ownerReferences, naming.ClusterLabels(cr))
	if err != nil {
		return errors.Wrap(err, "get or create CA")
	}

	secretNames := []string{api.SSLSecretName(cr), api.SSLInternalSecretName(cr)}
	pbmSecretName := tls.CertificatePBMClient(cr).SecretName()
	if cr.Spec.Backup.Enabled && cr.CompareVersion("1.24.0") >= 0 {
		secretNames = append(secretNames, pbmSecretName)
	}

	// Issue TLS certs signed by the shared CA
	for _, secretName := range secretNames {
		var tlsCert, tlsKey []byte
		if secretName == pbmSecretName {
			tlsCert, tlsKey, err = tls.IssuePBMClientWithCA(caCertPEM, caKeyPEM)
		} else {
			tlsCert, tlsKey, err = tls.IssueWithCA(certificateDNSNames, caCertPEM, caKeyPEM)
		}
		if err != nil {
			return errors.Wrapf(err, "issue TLS certificate for %s", secretName)
		}

		secretObj := corev1.Secret{
			Name:            secretName,
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
			Labels:          naming.ClusterLabels(cr),
			Data: map[string][]byte{
				"ca.crt":  caCertPEM,
				"tls.crt": tlsCert,
				"tls.key": tlsKey,
			},
			Type: corev1.SecretTypeTLS,
		}
		if err := r.createSSLSecret(ctx, &secretObj); err != nil {
			return errors.Wrapf(err, "create TLS secret %s", secretName)
		}
	}

	return nil
}

// getOrCreateManualCA returns the CA cert and key from the existing CA secret, or creates a new one.
func (r *ReconcilePerconaServerMongoDB) getOrCreateManualCA(ctx context.Context, cr *api.PerconaServerMongoDB, ownerRefs []metav1.OwnerReference, labels map[string]string) (caCert, caKey []byte, err error) {
	caSecretName := tls.ManualCASecretName(cr)
	caSecret, err := r.getSecret(ctx, cr, caSecretName)
	if err == nil {
		return caSecret.Data["ca.crt"], caSecret.Data["ca.key"], nil
	}
	if !k8serrors.IsNotFound(err) {
		return nil, nil, errors.Wrap(err, "get CA secret")
	}

	caCertPEM, caKeyPEM, err := tls.IssueCA()
	if err != nil {
		return nil, nil, errors.Wrap(err, "issue CA")
	}

	caSecretObj := &corev1.Secret{
		Name:            caSecretName,
		Namespace:       cr.Namespace,
		OwnerReferences: ownerRefs,
		Labels:          labels,
		Data: map[string][]byte{
			"ca.crt": caCertPEM,
			"ca.key": caKeyPEM,
		},
	}
	if err := r.client.Create(ctx, caSecretObj); err != nil {
		return nil, nil, errors.Wrap(err, "create CA secret")
	}

	return caCertPEM, caKeyPEM, nil
}

// needsManualSSLUpdate checks if the TLS certificate SANs differ from the expected SANs. Starting
// from 1.24.0 a certificate already covering every expected SAN is left untouched.
func (r *ReconcilePerconaServerMongoDB) needsManualSSLUpdate(ctx context.Context, cr *api.PerconaServerMongoDB, sslSecret *corev1.Secret) bool {
	if sslSecret == nil || len(sslSecret.Data["tls.crt"]) == 0 {
		return false
	}

	currentSANs, err := tls.GetDNSNamesFromCert(sslSecret.Data["tls.crt"])
	if err != nil {
		logf.FromContext(ctx).Error(err, "failed to parse TLS certificate SANs")
		return false
	}

	expectedSANs := tls.GetCertificateSans(cr)

	if cr.CompareVersion("1.24.0") < 0 {
		sort.Strings(currentSANs)
		sort.Strings(expectedSANs)

		return !slices.Equal(currentSANs, expectedSANs)
	}

	_, reissue := tls.SansToIssue(currentSANs, expectedSANs)

	return reissue
}

// updateSSLManually re-signs TLS certificates with the existing CA when SANs change.
func (r *ReconcilePerconaServerMongoDB) updateSSLManually(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx).WithName("updateSSLManually")

	caSecretName := tls.ManualCASecretName(cr)
	caSecret, err := r.getSecret(ctx, cr, caSecretName)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// CA secret doesn't exist (created before this feature). Cannot re-sign safely.
			log.Info("CA secret not found, skipping manual SSL update. Delete TLS secrets to trigger full regeneration.", "secret", caSecretName)
			return nil
		}
		return errors.Wrap(err, "get CA secret")
	}

	caCertPEM := caSecret.Data["ca.crt"]
	caKeyPEM := caSecret.Data["ca.key"]

	certificateDNSNames := tls.GetCertificateSans(cr)
	log.Info("SANs changed, re-signing TLS certificates with existing CA")

	secretNames := []string{api.SSLSecretName(cr), api.SSLInternalSecretName(cr)}
	pbmSecretName := tls.CertificatePBMClient(cr).SecretName()
	if cr.Spec.Backup.Enabled && cr.CompareVersion("1.24.0") >= 0 {
		secretNames = append(secretNames, pbmSecretName)
	}
	for _, secretName := range secretNames {
		secret, err := r.getSecret(ctx, cr, secretName)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return errors.Wrapf(err, "get secret %s", secretName)
		}
		if secretName == pbmSecretName {
			isCustomSecret, err := tls.IsSecretCreatedByUser(ctx, r.client, cr, secret)
			if err != nil {
				return errors.Wrap(err, "check PBM client TLS secret ownership")
			}
			if isCustomSecret {
				continue
			}
		}

		var tlsCert, tlsKey []byte
		if secretName == pbmSecretName {
			tlsCert, tlsKey, err = tls.IssuePBMClientWithCA(caCertPEM, caKeyPEM)
		} else {
			tlsCert, tlsKey, err = tls.IssueWithCA(certificateDNSNames, caCertPEM, caKeyPEM)
		}
		if err != nil {
			return errors.Wrapf(err, "re-sign TLS certificate for %s", secretName)
		}

		secret.Data["tls.crt"] = tlsCert
		secret.Data["tls.key"] = tlsKey

		if err := r.client.Update(ctx, secret); err != nil {
			return errors.Wrapf(err, "update secret %s", secretName)
		}
		log.Info("TLS certificate re-signed", "secret", secretName)
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) createSSLSecret(ctx context.Context, secret *corev1.Secret) error {
	oldSecret := new(corev1.Secret)

	err := r.client.Get(ctx, types.NamespacedName{
		Name:      secret.GetName(),
		Namespace: secret.GetNamespace(),
	}, oldSecret)

	if err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrap(err, "get object")
	}

	if k8serrors.IsNotFound(err) {
		return r.client.Create(ctx, secret)
	}

	return nil
}
