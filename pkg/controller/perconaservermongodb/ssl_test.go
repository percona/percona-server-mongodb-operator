package perconaservermongodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func newTestCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster",
			Namespace: "test-ns",
		},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.23.0",
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

func TestCurrentSSLAnnotation_WithExistingStatefulSet(t *testing.T) {
	cr := newTestCR()
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
						"percona.com/ssl-hash":          "abc123",
						"percona.com/ssl-internal-hash": "def456",
					},
				},
			},
		},
	}

	r := buildFakeClient(cr, sts)
	result := r.currentSSLAnnotation(context.Background(), cr)

	assert.Equal(t, "abc123", result["percona.com/ssl-hash"])
	assert.Equal(t, "def456", result["percona.com/ssl-internal-hash"])
}

func TestCurrentSSLAnnotation_NoStatefulSet(t *testing.T) {
	cr := newTestCR()
	r := buildFakeClient(cr)
	result := r.currentSSLAnnotation(context.Background(), cr)

	assert.Equal(t, "", result["percona.com/ssl-hash"])
	assert.Equal(t, "", result["percona.com/ssl-internal-hash"])
}

func TestSSLAnnotation_UserProvidedOnly_SecretMissing(t *testing.T) {
	cr := newTestCR()
	cr.Spec.TLS = &api.TLSSpec{
		CertManagementPolicy: api.CertManagementUserProvidedOnly,
	}

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
						"percona.com/ssl-hash":          "existing-hash",
						"percona.com/ssl-internal-hash": "existing-internal-hash",
					},
				},
			},
		},
	}

	r := buildFakeClient(cr, sts)
	annotation, err := r.sslAnnotation(context.Background(), cr)

	require.NoError(t, err)
	assert.Equal(t, "existing-hash", annotation["percona.com/ssl-hash"])
	assert.Equal(t, "existing-internal-hash", annotation["percona.com/ssl-internal-hash"])

	// Verify TLSSecretsReady condition is false
	assert.False(t, cr.Status.IsStatusConditionTrue(api.ConditionTypeTLSSecretsReady))
}

func TestSSLAnnotation_UserProvidedOnly_SecretPresent(t *testing.T) {
	cr := newTestCR()
	cr.Spec.TLS = &api.TLSSpec{
		CertManagementPolicy: api.CertManagementUserProvidedOnly,
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

	r := buildFakeClient(cr, sslSecret, sslInternalSecret)
	annotation, err := r.sslAnnotation(context.Background(), cr)

	require.NoError(t, err)
	assert.NotEmpty(t, annotation["percona.com/ssl-hash"])
	assert.NotEmpty(t, annotation["percona.com/ssl-internal-hash"])

	// Verify TLSSecretsReady condition is true
	assert.True(t, cr.Status.IsStatusConditionTrue(api.ConditionTypeTLSSecretsReady))
}

func TestSSLAnnotation_UserProvidedOnly_ConditionRemovedAfterRestore(t *testing.T) {
	cr := newTestCR()
	cr.Spec.TLS = &api.TLSSpec{
		CertManagementPolicy: api.CertManagementUserProvidedOnly,
	}

	// First call without secrets - TLSSecretsReady should be false
	r := buildFakeClient(cr)
	_, err := r.sslAnnotation(context.Background(), cr)
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
	_, err = r2.sslAnnotation(context.Background(), cr)
	require.NoError(t, err)
	assert.True(t, cr.Status.IsStatusConditionTrue(api.ConditionTypeTLSSecretsReady))
}

func TestReconcileSSL_UserProvidedOnly_SkipsCertCreation(t *testing.T) {
	cr := newTestCR()
	cr.Spec.TLS = &api.TLSSpec{
		CertManagementPolicy: api.CertManagementUserProvidedOnly,
	}

	r := buildFakeClient(cr)
	err := r.reconcileSSL(context.Background(), cr)

	// Should return nil (no error) without attempting to create certificates
	assert.NoError(t, err)
}
