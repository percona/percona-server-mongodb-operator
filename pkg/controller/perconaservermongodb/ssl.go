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

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
)

func (r *ReconcilePerconaServerMongoDB) reconsileSSL(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	err := r.client.Get(ctx,
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.SSL,
		},
		&secretObj,
	)
	// don't create secret ssl-internal if secret ssl is not created by operator
	if err == nil && !metav1.IsControlledBy(&secretObj, cr) {
		return nil
	}
	err = r.createSSLByCertManager(ctx, cr)
	if err != nil {
		log.Error(err, "issue cert with cert-manager")
		err = r.createSSLManualy(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "create ssl manualy")
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
	err = r.createOrUpdate(ctx, &cm.Issuer{
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
	if err != nil {
		return errors.Wrap(err, "create issuer")
	}

	err = r.createOrUpdate(ctx, &cm.Certificate{
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
	if err != nil {
		return errors.Wrap(err, "create certificate")
	}
	if cr.Spec.Secrets.SSL == cr.Spec.Secrets.SSLInternal {
		return r.waitForCerts(ctx, cr, cr.Namespace, cr.Spec.Secrets.SSL)
	}

	err = r.createOrUpdate(ctx, &cm.Certificate{
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
	if err != nil {
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

func (r *ReconcilePerconaServerMongoDB) createSSLManualy(ctx context.Context, cr *api.PerconaServerMongoDB) error {
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
	secretUpdated, err := r.createOrUpdateSSLSecret(ctx, &secretObj, certificateDNSNames)
	if err != nil {
		return errors.Wrap(err, "create TLS secret")
	}

	caCert, tlsCert, key, err = tls.Issue(certificateDNSNames)
	if err != nil {
		return errors.Wrap(err, "create pxc certificate")
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
	secretInternalUpdated, err := r.createOrUpdateSSLSecret(ctx, &secretObjInternal, certificateDNSNames)
	if err != nil {
		return errors.Wrap(err, "create TLS internal secret")
	}
	if secretUpdated || secretInternalUpdated {
		return errors.New("ssl secrets updated")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateSSLSecret(ctx context.Context, secret *corev1.Secret, DNSNames []string) (updated bool, err error) {
	oldSecret := new(corev1.Secret)

	err = r.client.Get(ctx, types.NamespacedName{
		Name:      secret.GetName(),
		Namespace: secret.GetNamespace(),
	}, oldSecret)

	if err != nil && !k8serr.IsNotFound(err) {
		return false, errors.Wrap(err, "get object")
	}

	if k8serr.IsNotFound(err) {
		return false, r.client.Create(ctx, secret)
	}

	cert, err := tls.ParseTLSCert(oldSecret.Data["tls.crt"])
	if err != nil {
		return false, errors.Wrap(err, "parse tls cert")
	}

	oldDNSNames := cert.DNSNames

	updateObject := false
	if len(oldDNSNames) == len(DNSNames) {
		for _, oldName := range oldDNSNames {
			found := false
			for _, name := range DNSNames {
				if oldName == name {
					found = true
					break
				}
			}
			if !found {
				updateObject = true
				break
			}
		}
	} else {
		updateObject = true
	}

	if updateObject {
		return true, r.client.Update(ctx, secret)
	}

	return false, nil
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
	}
	if cr.Spec.MultiCluster.Enabled {
		sans = append(sans, []string{
			cr.Name + "-mongos" + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
			"*." + cr.Name + "-mongos" + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
			cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
			"*." + cr.Name + "-" + api.ConfigReplSetName + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
		}...)
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
	}
	if cr.Spec.MultiCluster.Enabled {
		sans = append(sans, []string{
			cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
			"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.MultiCluster.DNSSuffix,
		}...)
	}
	return sans
}
