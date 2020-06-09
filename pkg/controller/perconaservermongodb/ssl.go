package perconaservermongodb

import (
	"context"
	"fmt"

	cmmeta "github.com/jetstack/cert-manager/pkg/apis/meta/v1"

	cm "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha3"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func (r *ReconcilePerconaServerMongoDB) reconsileSSL(cr *api.PerconaServerMongoDB) error {
	secretObj := corev1.Secret{}
	err := r.client.Get(context.TODO(),
		types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      cr.Spec.Secrets.SSL,
		},
		&secretObj,
	)
	if err == nil {
		return nil
	} else if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("get secret: %v", err)
	}
	err = r.createSSLByCertManager(cr)
	if err != nil {
		log.Info("using cert-manger: " + err.Error())
		err = r.createSSLManualy(cr)
		if err != nil {
			return fmt.Errorf("create ssl manualy: %v", err)
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createSSLByCertManager(cr *api.PerconaServerMongoDB) error {
	issuerKind := "Issuer"
	issuerName := cr.Name + "-psmdb-ca"
	certificateDNSNames := []string{"localhost"}

	for _, replset := range cr.Spec.Replsets {
		certificateDNSNames = append(certificateDNSNames, getCertificateSans(cr, replset)...)
	}
	owner, err := OwnerRef(cr, r.scheme)
	if err != nil {
		return err
	}
	ownerReferences := []metav1.OwnerReference{owner}
	err = r.client.Create(context.TODO(), &cm.Issuer{
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

	err = r.client.Create(context.TODO(), &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cr.Name + "-ssl",
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
		},
		Spec: cm.CertificateSpec{
			SecretName: cr.Spec.Secrets.SSL,
			CommonName: cr.Name,
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
		return nil
	}

	err = r.client.Create(context.TODO(), &cm.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:            cr.Name + "-ssl-internal",
			Namespace:       cr.Namespace,
			OwnerReferences: ownerReferences,
		},
		Spec: cm.CertificateSpec{
			SecretName: cr.Spec.Secrets.SSLInternal,
			CommonName: cr.Name,
			DNSNames:   certificateDNSNames,
			IsCA:       true,
			IssuerRef: cmmeta.ObjectReference{
				Name: issuerName,
				Kind: issuerKind,
			},
		},
	})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("create internal certificate: %v", err)
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createSSLManualy(cr *api.PerconaServerMongoDB) error {
	data := make(map[string][]byte)
	certificateDNSNames := []string{"localhost"}
	for _, replset := range cr.Spec.Replsets {
		certificateDNSNames = append(certificateDNSNames, getCertificateSans(cr, replset)...)
	}
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
	err = r.client.Create(context.TODO(), &secretObj)
	if err != nil {
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
	err = r.client.Create(context.TODO(), &secretObjInternal)
	if err != nil {
		return fmt.Errorf("create TLS internal secret: %v", err)
	}
	return nil
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
