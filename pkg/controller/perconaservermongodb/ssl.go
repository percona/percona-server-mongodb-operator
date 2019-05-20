package perconaservermongodb

import (
	"context"
	"fmt"

	cm "github.com/jetstack/cert-manager/pkg/apis/certmanager/v1alpha1"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
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

	issuerKind := "Issuer"
	issuerName := cr.Name + "-psmdb-ca"
	var certificateDNSNames []string
	for _, replset := range cr.Spec.Replsets {
		certificateDNSNames = append(certificateDNSNames,
			cr.Name+"-"+replset.Name,
			"*."+cr.Name+"-"+replset.Name,
		)
	}

	err = r.client.Create(context.TODO(), &cm.Issuer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      issuerName,
			Namespace: cr.Namespace,
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
			Name:      cr.Name + "-ssl",
			Namespace: cr.Namespace,
		},
		Spec: cm.CertificateSpec{
			SecretName: cr.Spec.Secrets.SSL,
			CommonName: cr.Name,
			DNSNames:   certificateDNSNames,
			// DNSNames: []string{"*.", "*"}, //certificateDNSNames,
			// IPAddresses: []string{"10.0.0.1"},
			IsCA: true,
			IssuerRef: cm.ObjectReference{
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
			Name:      cr.Name + "-ssl-internal",
			Namespace: cr.Namespace,
		},
		Spec: cm.CertificateSpec{
			SecretName: cr.Spec.Secrets.SSLInternal,
			CommonName: cr.Name,
			DNSNames:   certificateDNSNames,
			// IPAddresses: []string{"10.0.0.1"},
			IsCA: true,
			IssuerRef: cm.ObjectReference{
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
