package tls

import (
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestCertificate(t *testing.T) {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			TLS:       &api.TLSSpec{},
			Secrets:   &api.SecretsSpec{},
		},
	}

	t.Run("CA certificate", func(t *testing.T) {
		t.Run("IssuerKind", func(t *testing.T) {
			cr := cr.DeepCopy()
			ca := CertificateCA(cr)
			assert.Equal(t, "psmdb", ca.Namespace())
		})
		t.Run("ClusterIssuerKind", func(t *testing.T) {
			cr := cr.DeepCopy()
			cr.Spec.TLS.IssuerConf.Kind = cm.ClusterIssuerKind

			t.Run("default cert-manager namespace when ClusterIssuerKind is used", func(t *testing.T) {
				ca := CertificateCA(cr)
				assert.Equal(t, "cert-manager", ca.Namespace())
			})

			t.Run("namespace when env var is set and ClusterIssuerKind is used", func(t *testing.T) {
				t.Setenv("CERTMANAGER_NAMESPACE", "my-cm")
				ca := CertificateCA(cr)
				assert.Equal(t, "my-cm", ca.Namespace())
			})

			t.Run("issuerRef", func(t *testing.T) {
				t.Run("latest version", func(t *testing.T) {
					ca := CertificateCA(cr)
					obj := ca.Object()
					assert.Equal(t, "psmdb-mock-psmdb-psmdb-ca-issuer", obj.Spec.IssuerRef.Name)
					assert.Equal(t, cm.ClusterIssuerKind, obj.Spec.IssuerRef.Kind)
				})
				t.Run("old version", func(t *testing.T) {
					cr := cr.DeepCopy()
					cr.Spec.CRVersion = "1.21.0"
					ca := CertificateCA(cr)
					obj := ca.Object()
					assert.Equal(t, "psmdb-mock-psmdb-ca-issuer", obj.Spec.IssuerRef.Name)
					assert.Equal(t, cm.IssuerKind, obj.Spec.IssuerRef.Kind)
				})
			})
		})
	})

	t.Run("TLS certificates", func(t *testing.T) {
		t.Run("IssuerKind", func(t *testing.T) {
			cr := cr.DeepCopy()
			t.Run("internal", func(t *testing.T) {
				cert := CertificateTLS(cr, false)
				assert.Equal(t, "psmdb", cert.Namespace())
			})
			t.Run("non-internal", func(t *testing.T) {
				cert := CertificateTLS(cr, true)
				assert.Equal(t, "psmdb", cert.Namespace())
			})
		})
		t.Run("ClusterIssuerKind", func(t *testing.T) {
			cr := cr.DeepCopy()
			cr.Spec.TLS.IssuerConf.Kind = cm.ClusterIssuerKind

			t.Run("issuerRef", func(t *testing.T) {
				t.Run("latest version", func(t *testing.T) {
					t.Run("internal", func(t *testing.T) {
						cert := CertificateTLS(cr, true)
						obj := cert.Object()
						assert.Equal(t, "psmdb-mock-psmdb-psmdb-issuer", obj.Spec.IssuerRef.Name)
						assert.Equal(t, cm.ClusterIssuerKind, obj.Spec.IssuerRef.Kind)
					})
					t.Run("non-internal", func(t *testing.T) {
						cert := CertificateTLS(cr, false)
						obj := cert.Object()
						assert.Equal(t, "psmdb-mock-psmdb-psmdb-issuer", obj.Spec.IssuerRef.Name)
						assert.Equal(t, cm.ClusterIssuerKind, obj.Spec.IssuerRef.Kind)
					})
				})
				t.Run("old version", func(t *testing.T) {
					cr := cr.DeepCopy()
					cr.Spec.CRVersion = "1.21.0"
					t.Run("internal", func(t *testing.T) {
						cert := CertificateTLS(cr, true)
						obj := cert.Object()
						assert.Equal(t, "psmdb-mock-psmdb-issuer", obj.Spec.IssuerRef.Name)
						assert.Equal(t, cm.ClusterIssuerKind, obj.Spec.IssuerRef.Kind)
					})
					t.Run("non-internal", func(t *testing.T) {
						cert := CertificateTLS(cr, false)
						obj := cert.Object()
						assert.Equal(t, "psmdb-mock-psmdb-issuer", obj.Spec.IssuerRef.Name)
						assert.Equal(t, cm.ClusterIssuerKind, obj.Spec.IssuerRef.Kind)
					})
				})
			})
		})
	})
}
