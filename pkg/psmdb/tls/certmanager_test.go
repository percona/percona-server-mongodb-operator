package tls

import (
	"context"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestCreateIssuer(t *testing.T) {
	ctx := context.Background()

	customIssuerName := "issuer-conf-name"

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.16.0",
			TLS: &api.TLSSpec{
				IssuerConf: &cmmeta.ObjectReference{
					Name: customIssuerName,
				},
			},
		},
	}

	r := buildFakeClient(cr)

	issuer := &cm.Issuer{}

	t.Run("Create issuer with custom name", func(t *testing.T) {
		if _, err := r.ApplyIssuer(ctx, cr); err != nil {
			t.Fatal(err)
		}

		err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: "psmdb", Name: customIssuerName}, issuer)
		if err != nil {
			t.Fatal(err)
		}

		if issuer.Name != customIssuerName {
			t.Fatalf("Expected issuer name %s, got %s", customIssuerName, issuer.Name)
		}
	})

	t.Run("Create issuer with default name", func(t *testing.T) {
		cr.Spec.CRVersion = "1.15.0"
		if _, err := r.ApplyIssuer(ctx, cr); err != nil {
			t.Fatal(err)
		}

		err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: "psmdb", Name: issuerName(cr)}, issuer)
		if err != nil {
			t.Fatal(err)
		}

		if issuer.Name != issuerName(cr) {
			t.Fatalf("Expected issuer name %s, got %s", issuerName(cr), issuer.Name)
		}
	})
}

func TestCreateCertificate(t *testing.T) {
	ctx := context.Background()

	customIssuerName := "issuer-conf-name"
	customIssuerKind := "issuer-conf-kind"
	customIssuerGroup := "issuer-conf-group"

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.16.0",
			Secrets: &api.SecretsSpec{
				SSL: "ssl",
			},
			TLS: &api.TLSSpec{
				IssuerConf: &cmmeta.ObjectReference{
					Name:  customIssuerName,
					Kind:  customIssuerKind,
					Group: customIssuerGroup,
				},
			},
		},
	}

	r := buildFakeClient(cr)

	cert := &cm.Certificate{}

	t.Run("Create certificate with custom issuer name", func(t *testing.T) {
		tlsCert := CertificateTLS(cr, false)
		if _, err := r.ApplyCertificate(ctx, cr, tlsCert); err != nil {
			t.Fatal(err)
		}

		err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: "psmdb", Name: tlsCert.Name()}, cert)
		if err != nil {
			t.Fatal(err)
		}

		if cert.Spec.IssuerRef.Name != customIssuerName {
			t.Fatalf("Expected issuer name %s, got %s", customIssuerName, cert.Spec.IssuerRef.Name)
		}
	})

	t.Run("Create certificate with default issuer name", func(t *testing.T) {
		cr.Name = "psmdb-mock-1"
		cr.Spec.CRVersion = "1.15.0"

		tlsCert := CertificateTLS(cr, false)
		if _, err := r.ApplyCertificate(ctx, cr, tlsCert); err != nil {
			t.Fatal(err)
		}

		err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: "psmdb", Name: tlsCert.Name()}, cert)
		if err != nil {
			t.Fatal(err)
		}

		if cert.Spec.IssuerRef.Name != issuerName(cr) {
			t.Fatalf("Expected issuer name %s, got %s", issuerName(cr), cert.Spec.IssuerRef.Name)
		}
	})
}

func TestWaitForCerts(t *testing.T) {
	ctx := context.Background()

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "default",
			UID:       "test-uid-123",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
		},
	}

	certName := CertificateCA(cr).SecretName()

	tests := map[string]struct {
		certificate *cm.Certificate
		secret      *corev1.Secret
	}{
		"with cert-manager managed secret": {
			certificate: &cm.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: cr.Namespace,
					UID:       "cert-uid-456",
				},
				Spec: cm.CertificateSpec{
					SecretName: certName,
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: cr.Namespace,
					Annotations: map[string]string{
						cm.CertificateNameKey: certName,
					},
					OwnerReferences: []metav1.OwnerReference{
						{
							APIVersion: cm.SchemeGroupVersion.String(),
							Kind:       cm.CertificateKind,
							Name:       certName,
							UID:        "cert-uid-456",
							Controller: ptr.To(true),
						},
					},
				},
				Data: map[string][]byte{
					"ca.crt":  []byte("fake-ca-cert"),
					"tls.crt": []byte("fake-tls-cert"),
					"tls.key": []byte("fake-tls-key"),
				},
			},
		},
		"with cert-manager managed secret but without OwnerReferences": {
			certificate: &cm.Certificate{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: cr.Namespace,
					UID:       "cert-uid-456",
				},
				Spec: cm.CertificateSpec{
					SecretName: certName,
				},
			},
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: cr.Namespace,
					Annotations: map[string]string{
						cm.CertificateNameKey: certName,
					},
				},
				Data: map[string][]byte{
					"ca.crt":  []byte("fake-ca-cert"),
					"tls.crt": []byte("fake-tls-cert"),
					"tls.key": []byte("fake-tls-key"),
				},
			},
		},
		"without cert-manager": {
			certificate: nil,
			secret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      certName,
					Namespace: cr.Namespace,
				},
				Data: map[string][]byte{
					"ca.crt":  []byte("fake-ca-cert"),
					"tls.crt": []byte("fake-tls-cert"),
					"tls.key": []byte("fake-tls-key"),
				},
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			s := scheme.Scheme
			s.AddKnownTypes(api.SchemeGroupVersion, new(api.PerconaServerMongoDB))
			s.AddKnownTypes(cm.SchemeGroupVersion, new(cm.Certificate))
			s.AddKnownTypes(corev1.SchemeGroupVersion, new(corev1.Secret))

			objects := []client.Object{cr, tc.secret}
			if tc.certificate != nil {
				objects = append(objects, tc.certificate)
			}

			cl := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(objects...).
				WithStatusSubresource(cr).
				Build()

			controller := &certManagerController{
				cl:     cl,
				scheme: s,
				dryRun: false,
			}

			err := controller.WaitForCerts(ctx, cr, CertificateCA(cr))
			assert.NoError(t, err)
		})
	}
}

// creates a fake client to mock API calls with the mock objects
func buildFakeClient(objs ...client.Object) CertManagerController {
	s := scheme.Scheme

	s.AddKnownTypes(api.SchemeGroupVersion,
		new(api.PerconaServerMongoDB),
	)

	s.AddKnownTypes(cm.SchemeGroupVersion,
		new(cm.Issuer),
		new(cm.Certificate),
	)

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(objs...).Build()

	return &certManagerController{
		cl:     cl,
		scheme: s,
	}
}
