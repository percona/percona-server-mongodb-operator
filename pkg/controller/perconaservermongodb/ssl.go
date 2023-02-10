package perconaservermongodb

import (
	"context"
	"time"

	cm "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/jetstack/cert-manager/pkg/apis/meta/v1"
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
	issuerKind := "Issuer"
	issuerName := cr.Name + "-psmdb-ca"
	certificateDNSNames := []string{"localhost"}

	for _, replset := range cr.Spec.Replsets {
		certificateDNSNames = append(certificateDNSNames, getCertificateSans(cr, replset)...)
	}
	certificateDNSNames = append(certificateDNSNames, getShardingSans(cr)...)
	owner, err := OwnerRef(cr, r.scheme)
	if err != nil {
		return err
	}
	ownerReferences := []metav1.OwnerReference{owner}
	err = r.client.Create(ctx, &cm.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:            issuerName,
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
		},
		Spec: cm.IssuerSpec{
			IssuerConfig: cm.IssuerConfig{
				SelfSigned: &cm.SelfSignedIssuer{},
			},
		},
	})
	if err != nil && !k8serr.IsAlreadyExists(err) {
		return errors.Wrap(err, "create issuer")
	}

	err = r.client.Create(ctx, &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cr.Name + "-ssl",
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
		},
		Spec: cm.CertificateSpec{
			Subject: &cm.X509Subject{
				Organizations: []string{"PSMDB"},
			},
			CommonName: cr.Name,
			SecretName: cr.Spec.Secrets.SSL,
			DNSNames:   certificateDNSNames,
			IsCA:       true,
			Duration:   &cr.Spec.TLS.CertValidityDuration,
			IssuerRef: cmmeta.ObjectReference{
				Name: issuerName,
				Kind: issuerKind,
			},
		},
	})
	if err != nil && !k8serr.IsAlreadyExists(err) {
		return errors.Wrap(err, "create certificate")
	}
	if cr.Spec.Secrets.SSL == cr.Spec.Secrets.SSLInternal {
		return r.waitForCerts(ctx, cr, cr.Namespace, cr.Spec.Secrets.SSL)
	}

	err = r.client.Create(ctx, &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cr.Name + "-ssl-internal",
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
		},
		Spec: cm.CertificateSpec{
			Subject: &cm.X509Subject{
				Organizations: []string{"PSMDB"},
			},
			CommonName: cr.Name,
			SecretName: cr.Spec.Secrets.SSLInternal,
			DNSNames:   certificateDNSNames,
			IsCA:       true,
			Duration:   &cr.Spec.TLS.CertValidityDuration,
			IssuerRef: cmmeta.ObjectReference{
				Name: issuerName,
				Kind: issuerKind,
			},
		},
	})
	if err != nil && !k8serr.IsAlreadyExists(err) {
		return errors.Wrap(err, "create internal certificate")
	}

	return r.waitForCerts(ctx, cr, cr.Namespace, cr.Spec.Secrets.SSL, cr.Spec.Secrets.SSLInternal)
}

func (r *ReconcilePerconaServerMongoDB) waitForCerts(ctx context.Context, cr *api.PerconaServerMongoDB, namespace string, secretsList ...string) error {
	ticker := time.NewTicker(1 * time.Second)
	timeoutTimer := time.NewTimer(30 * time.Second)
	defer timeoutTimer.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-timeoutTimer.C:
			return errors.Errorf("timeout: can't get tls certificates from certmanager, %s", secretsList)
		case <-ticker.C:
			sucessCount := 0
			for _, secretName := range secretsList {
				secret := &corev1.Secret{}
				err := r.client.Get(ctx, types.NamespacedName{
					Name:      secretName,
					Namespace: namespace,
				}, secret)
				if err != nil && !k8serr.IsNotFound(err) {
					return err
				} else if err == nil {
					sucessCount++
					if len(secret.OwnerReferences) == 0 {
						if err = setControllerReference(cr, secret, r.scheme); err != nil {
							return errors.Wrap(err, "set controller reference")
						}
						if err = r.client.Update(ctx, secret); err != nil {
							return errors.Wrap(err, "failed to update secret")
						}
					}
				}
			}
			if sucessCount == len(secretsList) {
				return nil
			}
		}
	}
}

func (r *ReconcilePerconaServerMongoDB) createSSLManually(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	data := make(map[string][]byte)
	certificateDNSNames := []string{"localhost"}
	for _, replset := range cr.Spec.Replsets {
		certificateDNSNames = append(certificateDNSNames, getCertificateSans(cr, replset)...)
	}
	certificateDNSNames = append(certificateDNSNames, getShardingSans(cr)...)
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

func getCertificateSans(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) []string {
	sans := []string{
		cr.Name + "-" + replset.Name,
		cr.Name + "-" + replset.Name + "." + cr.Namespace,
		cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
		"*." + cr.Name + "-" + replset.Name,
		"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace,
		"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
		cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
		"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
	}

	if cr.CompareVersion("1.13.0") >= 0 {
		sans = append(sans, "*."+cr.Namespace+"."+cr.Spec.MultiCluster.DNSSuffix)
	}

	return sans
}
