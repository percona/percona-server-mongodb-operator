package perconaservermongodb

import (
	"context"
	"fmt"
	"time"

	cm "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/jetstack/cert-manager/pkg/apis/meta/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serr "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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
			Name:      cr.Spec.Secrets.SSL + "-ssl-internal",
		},
		&secretInternalObj,
	)
	if errSecret == nil && errInternalSecret == nil {
		return nil
	} else if errSecret != nil && !k8serrors.IsNotFound(errSecret) {
		return fmt.Errorf("get secret: %v", errSecret)
	} else if errInternalSecret != nil && !k8serrors.IsNotFound(errInternalSecret) {
		return fmt.Errorf("get internal secret: %v", errInternalSecret)
	}
	// don't create secret ssl-internal if secret ssl is not created by operator
	if errSecret == nil && !metav1.IsControlledBy(&secretObj, cr) {
		return nil
	}
	err := r.createSSLByCertManager(ctx, cr)
	if err != nil {
		log.Error(err, "issue cert with cert-manager")
		err = r.createSSLManualy(ctx, cr)
		if err != nil {
			return fmt.Errorf("create ssl manualy: %v", err)
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
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create issuer: %v", err)
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
			IssuerRef: cmmeta.ObjectReference{
				Name: issuerName,
				Kind: issuerKind,
			},
		},
	})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create certificate: %v", err)
	}
	if cr.Spec.Secrets.SSL == cr.Spec.Secrets.SSLInternal {
		return r.waitForCerts(ctx, cr.Namespace, cr.Spec.Secrets.SSL)
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
			CommonName:   cr.Name,
			SecretName:   cr.Spec.Secrets.SSLInternal,
			DNSNames:     certificateDNSNames,
			IsCA:         true,
			IssuerRef: cmmeta.ObjectReference{
				Name: issuerName,
				Kind: issuerKind,
			},
		},
	})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create internal certificate: %v", err)
	}

	return r.waitForCerts(ctx, cr.Namespace, cr.Spec.Secrets.SSL, cr.Spec.Secrets.SSLInternal)
}

func (r *ReconcilePerconaServerMongoDB) waitForCerts(ctx context.Context, namespace string, secretsList ...string) error {
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
		return fmt.Errorf("create proxy certificate: %v", err)
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
	err = r.client.Create(ctx, &secretObj)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create TLS secret: %v", err)
	}

	caCert, tlsCert, key, err = tls.Issue(certificateDNSNames)
	if err != nil {
		return fmt.Errorf("create pxc certificate: %v", err)
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
	err = r.client.Create(ctx, &secretObjInternal)
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create TLS internal secret: %v", err)
	}
	return nil
}

func getShardingSans(cr *api.PerconaServerMongoDB) []string {
	return []string{
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
}

func getCertificateSans(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) []string {
	return []string{
		cr.Name + "-" + replset.Name,
		cr.Name + "-" + replset.Name + "." + cr.Namespace,
		cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
		"*." + cr.Name + "-" + replset.Name,
		"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace,
		"*." + cr.Name + "-" + replset.Name + "." + cr.Namespace + "." + cr.Spec.ClusterServiceDNSSuffix,
	}
}
