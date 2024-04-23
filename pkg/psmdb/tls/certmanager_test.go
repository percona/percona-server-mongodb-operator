package tls

import (
	"context"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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
		if _, err := r.ApplyCertificate(ctx, cr, false); err != nil {
			t.Fatal(err)
		}

		err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: "psmdb", Name: certificateName(cr, false)}, cert)
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

		if _, err := r.ApplyCertificate(ctx, cr, false); err != nil {
			t.Fatal(err)
		}

		err := r.GetClient().Get(ctx, types.NamespacedName{Namespace: "psmdb", Name: certificateName(cr, false)}, cert)
		if err != nil {
			t.Fatal(err)
		}

		if cert.Spec.IssuerRef.Name != issuerName(cr) {
			t.Fatalf("Expected issuer name %s, got %s", issuerName(cr), cert.Spec.IssuerRef.Name)
		}
	})
}

// creates a fake client to mock API calls with the mock objects
func buildFakeClient(objs ...client.Object) CertManagerController {
	s := scheme.Scheme

	s.AddKnownTypes(api.SchemeGroupVersion,
		new(api.PerconaServerMongoDB),
		new(cm.Issuer),
		new(cm.Certificate),
	)

	cl := fake.NewClientBuilder().WithScheme(s).WithObjects(objs...).WithStatusSubresource(objs...).Build()

	return &certManagerController{
		cl:     cl,
		scheme: s,
	}
}
