package perconaservermongodb

import (
	"context"
	"encoding/pem"
	"errors"
	"testing"

	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	faketls "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls/fake"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func newTestCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			Secrets: &api.SecretsSpec{
				SSL:         "test-cluster-ssl",
				SSLInternal: "test-cluster-ssl-internal",
			},
			Replsets: []*api.ReplsetSpec{
				{
					Name: "rs0",
					Size: 3,
				},
			},
		},
	}
}

func TestCurrentSSLAnnotation(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-rs0",
			Namespace: "test-ns",
			Labels: map[string]string{
				naming.LabelKubernetesInstance: "test-cluster",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						naming.AnnotationSSLHash:         "abc123",
						naming.AnnotationSSLInternalHash: "def456",
					},
				},
			},
		},
	}

	tests := []struct {
		name             string
		objects          []client.Object
		wantSSLHash      string
		wantInternalHash string
	}{
		{
			name:             "with existing statefulset",
			objects:          []client.Object{sts},
			wantSSLHash:      "abc123",
			wantInternalHash: "def456",
		},
		{
			name:             "no statefulset",
			objects:          nil,
			wantSSLHash:      "",
			wantInternalHash: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newTestCR()
			objs := append([]client.Object{cr}, tt.objects...)
			r := buildFakeClient(objs...)
			result, err := r.currentSSLAnnotation(t.Context(), cr)
			require.NoError(t, err)

			assert.Equal(t, tt.wantSSLHash, result[naming.AnnotationSSLHash])
			assert.Equal(t, tt.wantInternalHash, result[naming.AnnotationSSLInternalHash])
		})
	}
}

func TestSSLAnnotation_UserProvidedOnly(t *testing.T) {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-rs0",
			Namespace: "test-ns",
			Labels: map[string]string{
				naming.LabelKubernetesInstance: "test-cluster",
			},
		},
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						naming.AnnotationSSLHash:         "existing-hash",
						naming.AnnotationSSLInternalHash: "existing-internal-hash",
					},
				},
			},
		},
	}

	sslSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-ssl",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}
	sslInternalSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-ssl-internal",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"tls.crt": []byte("internal-cert-data"),
			"tls.key": []byte("internal-key-data"),
		},
	}

	tests := []struct {
		name                 string
		objects              []client.Object
		checkAnnotation      func(t *testing.T, ann map[string]string)
		wantSecretsReadyCond bool
	}{
		{
			name:    "secrets missing — preserves existing sts annotations",
			objects: []client.Object{sts},
			checkAnnotation: func(t *testing.T, ann map[string]string) {
				assert.Equal(t, "existing-hash", ann[naming.AnnotationSSLHash])
				assert.Equal(t, "existing-internal-hash", ann[naming.AnnotationSSLInternalHash])
			},
			wantSecretsReadyCond: false,
		},
		{
			name:    "secrets present — computes fresh hashes",
			objects: []client.Object{sslSecret, sslInternalSecret},
			checkAnnotation: func(t *testing.T, ann map[string]string) {
				assert.NotEmpty(t, ann[naming.AnnotationSSLHash])
				assert.NotEmpty(t, ann[naming.AnnotationSSLInternalHash])
			},
			wantSecretsReadyCond: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newTestCR()
			cr.Spec.TLS = &api.TLSSpec{
				CertManagementPolicy: api.CertManagementUserProvidedOnly,
			}

			objs := append([]client.Object{cr}, tt.objects...)
			r := buildFakeClient(objs...)
			annotation, err := r.sslAnnotation(t.Context(), cr)
			require.NoError(t, err)

			tt.checkAnnotation(t, annotation)
			assert.Equal(t, tt.wantSecretsReadyCond, cr.Status.IsStatusConditionTrue(api.ConditionTypeTLSSecretsReady))
		})
	}
}

func TestSSLAnnotation_UserProvidedOnly_ConditionRemovedAfterRestore(t *testing.T) {
	cr := newTestCR()
	cr.Spec.TLS = &api.TLSSpec{
		CertManagementPolicy: api.CertManagementUserProvidedOnly,
	}

	// First call without secrets - TLSSecretsReady should be false
	r := buildFakeClient(cr)
	_, err := r.sslAnnotation(t.Context(), cr)
	require.NoError(t, err)
	assert.False(t, cr.Status.IsStatusConditionTrue(api.ConditionTypeTLSSecretsReady))

	// Now create secrets and call again - TLSSecretsReady should be true
	sslSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-ssl",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}
	sslInternalSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-ssl-internal",
			Namespace: "test-ns",
		},
		Data: map[string][]byte{
			"tls.crt": []byte("internal-cert-data"),
			"tls.key": []byte("internal-key-data"),
		},
	}

	r2 := buildFakeClient(cr, sslSecret, sslInternalSecret)
	_, err = r2.sslAnnotation(t.Context(), cr)
	require.NoError(t, err)
	assert.True(t, cr.Status.IsStatusConditionTrue(api.ConditionTypeTLSSecretsReady))
}

func TestReconcileSSL_UserProvidedOnly_SkipsCertCreation(t *testing.T) {
	cr := newTestCR()
	cr.Spec.TLS = &api.TLSSpec{
		CertManagementPolicy: api.CertManagementUserProvidedOnly,
	}

	r := buildFakeClient(cr)
	err := r.reconcileSSL(t.Context(), cr)

	// With certManagementPolicy userProvidedOnly and no TLS secret yet, the operator
	// must not create certificates and should wait for the user to provide them.
	assert.ErrorIs(t, err, errTLSNotReady)
}

func TestApplyCertManagerCertificatesExternalIssuer(t *testing.T) {
	newExternalIssuerCR := func() *api.PerconaServerMongoDB {
		cr := newTestCR()
		cr.Spec.TLS = &api.TLSSpec{
			IssuerConf: cmmeta.IssuerReference{
				Name:  "external-issuer",
				Kind:  "AWSPCAIssuer",
				Group: "awspca.cert-manager.io",
			},
		}
		return cr
	}

	t.Run("uses existing external issuer", func(t *testing.T) {
		cr := newExternalIssuerCR()
		cm := faketls.NewCertManagerController(nil, nil, false).(*faketls.CertManagerController)
		r := &ReconcilePerconaServerMongoDB{}

		status, err := r.applyCertManagerCertificates(t.Context(), cr, cm)
		require.NoError(t, err)
		assert.Equal(t, util.ApplyStatusUnchanged, status)

		assert.Zero(t, cm.ApplyCAIssuerCalls)
		assert.Zero(t, cm.ApplyIssuerCalls)
		assert.Equal(t, 2, cm.ApplyCertificateCalls)
		assert.Equal(t, 1, cm.WaitForCertsCalls)
		assert.Equal(t, []string{"test-cluster-ssl", "test-cluster-ssl-internal"}, cm.CertNames)
		assert.Equal(t, []string{"external-issuer", "external-issuer"}, cm.IssuerRefNames)
		assert.Equal(t, []string{"AWSPCAIssuer", "AWSPCAIssuer"}, cm.IssuerRefKinds)
		assert.Equal(t, []string{"awspca.cert-manager.io", "awspca.cert-manager.io"}, cm.IssuerRefGroups)
		assert.Equal(t, [][]string{{"test-cluster-ssl", "test-cluster-ssl-internal"}}, cm.WaitForCertNames)
	})

	t.Run("requires name", func(t *testing.T) {
		cr := newExternalIssuerCR()
		cr.Spec.TLS.IssuerConf.Name = ""
		cm := faketls.NewCertManagerController(nil, nil, false).(*faketls.CertManagerController)
		r := &ReconcilePerconaServerMongoDB{}

		_, err := r.applyCertManagerCertificates(t.Context(), cr, cm)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "external issuer requires tls.issuerConf.name")

		assert.Zero(t, cm.ApplyCAIssuerCalls)
		assert.Zero(t, cm.ApplyIssuerCalls)
		assert.Zero(t, cm.ApplyCertificateCalls)
		assert.Zero(t, cm.WaitForCertsCalls)
	})
}

// mergeNewCA deletes the "-old" secret once the merged CA is already in the
// live one. The client cache can be behind the API server there, so a secret
// that is already gone is the state the cleanup wants, not a failure.
func TestMergeNewCADeletesAnAlreadyDeletedOldSecret(t *testing.T) {
	pemBlock := func(body string) []byte {
		return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte(body)})
	}
	oldCA := pemBlock("old-ca")
	newCA := pemBlock("new-ca")
	mergedCA := append(append([]byte{}, oldCA...), newCA...)

	secretsFor := func(cr *api.PerconaServerMongoDB) []client.Object {
		objs := []client.Object{cr}
		for _, name := range []string{api.SSLInternalSecretName(cr), api.SSLSecretName(cr)} {
			objs = append(objs,
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cr.Namespace},
					Data:       map[string][]byte{"ca.crt": mergedCA},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{Name: name + "-old", Namespace: cr.Namespace},
					Data:       map[string][]byte{"ca.crt": oldCA},
				},
			)
		}
		return objs
	}

	t.Run("delete returns NotFound", func(t *testing.T) {
		cr := newTestCR()
		r := buildFakeClient(secretsFor(cr)...)
		r.client = interceptorClient(r.client, func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			return k8serrors.NewNotFound(corev1.Resource("secrets"), obj.GetName())
		})

		require.NoError(t, r.mergeNewCA(t.Context(), cr))
	})

	t.Run("delete fails for another reason", func(t *testing.T) {
		cr := newTestCR()
		r := buildFakeClient(secretsFor(cr)...)
		r.client = interceptorClient(r.client, func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return errors.New("boom")
		})

		require.Error(t, r.mergeNewCA(t.Context(), cr))
	})
}

func interceptorClient(c client.Client, del func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error) client.Client {
	return interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{Delete: del})
}
