package perconaservermongodb

import (
	"sort"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	faketls "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls/fake"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func newTestCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		Name:      "test-cluster",
		Namespace: "test-ns",
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
		Name:      "test-cluster-rs0",
		Namespace: "test-ns",
		Labels: map[string]string{
			naming.LabelKubernetesInstance: "test-cluster",
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
		Name:      "test-cluster-rs0",
		Namespace: "test-ns",
		Labels: map[string]string{
			naming.LabelKubernetesInstance: "test-cluster",
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
		Name:      "test-cluster-ssl",
		Namespace: "test-ns",
		Data: map[string][]byte{
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}
	sslInternalSecret := &corev1.Secret{
		Name:      "test-cluster-ssl-internal",
		Namespace: "test-ns",
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
		Name:      "test-cluster-ssl",
		Namespace: "test-ns",
		Data: map[string][]byte{
			"tls.crt": []byte("cert-data"),
			"tls.key": []byte("key-data"),
		},
	}
	sslInternalSecret := &corev1.Secret{
		Name:      "test-cluster-ssl-internal",
		Namespace: "test-ns",
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

func TestApplyCertManagerCertificatesClusterIssuer(t *testing.T) {
	newClusterIssuerCR := func(crVersion string) *api.PerconaServerMongoDB {
		cr := newTestCR()
		cr.Spec.CRVersion = crVersion
		cr.Spec.TLS = &api.TLSSpec{
			IssuerConf: cmmeta.IssuerReference{
				Name: "user-cluster-issuer",
				Kind: cm.ClusterIssuerKind,
			},
		}
		return cr
	}

	clusterIssuer := func(labels map[string]string) *cm.ClusterIssuer {
		return &cm.ClusterIssuer{
			Name:   "user-cluster-issuer",
			Labels: labels,
		}
	}

	t.Run("user created cluster issuer is used as is", func(t *testing.T) {
		cr := newClusterIssuerCR(version.Version())
		r := buildFakeClient(cr, clusterIssuer(nil))
		cmCtrl := faketls.NewCertManagerController(nil, nil, false).(*faketls.CertManagerController)

		_, err := r.applyCertManagerCertificates(t.Context(), cr, cmCtrl)
		require.NoError(t, err)

		assert.Zero(t, cmCtrl.ApplyCAIssuerCalls)
		assert.Zero(t, cmCtrl.ApplyIssuerCalls)
		assert.Equal(t, []string{"user-cluster-issuer", "user-cluster-issuer"}, cmCtrl.IssuerRefNames)
		assert.Equal(t, []string{cm.ClusterIssuerKind, cm.ClusterIssuerKind}, cmCtrl.IssuerRefKinds)
	})

	t.Run("operator owned cluster issuer is reconciled", func(t *testing.T) {
		cr := newClusterIssuerCR(version.Version())
		r := buildFakeClient(cr, clusterIssuer(naming.Labels()))
		cmCtrl := faketls.NewCertManagerController(nil, nil, false).(*faketls.CertManagerController)

		_, err := r.applyCertManagerCertificates(t.Context(), cr, cmCtrl)
		require.NoError(t, err)

		assert.Equal(t, 1, cmCtrl.ApplyCAIssuerCalls)
		assert.Equal(t, 1, cmCtrl.ApplyIssuerCalls)
	})

	t.Run("missing cluster issuer is created by the operator", func(t *testing.T) {
		cr := newClusterIssuerCR(version.Version())
		r := buildFakeClient(cr)
		cmCtrl := faketls.NewCertManagerController(nil, nil, false).(*faketls.CertManagerController)

		_, err := r.applyCertManagerCertificates(t.Context(), cr, cmCtrl)
		require.NoError(t, err)

		assert.Equal(t, 1, cmCtrl.ApplyCAIssuerCalls)
		assert.Equal(t, 1, cmCtrl.ApplyIssuerCalls)
	})

	t.Run("old cr version keeps ClusterIssuer in issuerRef", func(t *testing.T) {
		cr := newClusterIssuerCR("1.22.0")
		r := buildFakeClient(cr, clusterIssuer(nil))
		cmCtrl := faketls.NewCertManagerController(nil, nil, false).(*faketls.CertManagerController)

		_, err := r.applyCertManagerCertificates(t.Context(), cr, cmCtrl)
		require.NoError(t, err)

		assert.Zero(t, cmCtrl.ApplyIssuerCalls)
		assert.Equal(t, []string{cm.ClusterIssuerKind, cm.ClusterIssuerKind}, cmCtrl.IssuerRefKinds)
	})

	t.Run("old cr version does not fall back to namespaced issuers", func(t *testing.T) {
		cr := newClusterIssuerCR("1.22.0")
		r := buildFakeClient(cr)
		cmCtrl := faketls.NewCertManagerController(nil, nil, false).(*faketls.CertManagerController)

		_, err := r.applyCertManagerCertificates(t.Context(), cr, cmCtrl)
		require.NoError(t, err)

		assert.Zero(t, cmCtrl.ApplyCAIssuerCalls)
		assert.Zero(t, cmCtrl.ApplyIssuerCalls)
		assert.Equal(t, []string{cm.ClusterIssuerKind, cm.ClusterIssuerKind}, cmCtrl.IssuerRefKinds)
	})
}

func TestIsExternalIssuer(t *testing.T) {
	clusterIssuer := func(labels map[string]string) *cm.ClusterIssuer {
		return &cm.ClusterIssuer{
			Name:   "user-cluster-issuer",
			Labels: labels,
		}
	}

	issuer := func(labels map[string]string) *cm.Issuer {
		return &cm.Issuer{
			Name:      "user-issuer",
			Namespace: "test-ns",
			Labels:    labels,
		}
	}

	tests := []struct {
		name      string
		crVersion string
		tls       *api.TLSSpec
		objects   []client.Object
		want      bool
		wantErr   string
	}{
		{
			name: "no tls spec",
			tls:  nil,
		},
		{
			name: "namespaced issuer",
			tls:  &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "issuer", Kind: cm.IssuerKind}},
		},
		{
			name: "unknown issuer kind",
			tls:  &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "external-issuer", Kind: "AWSPCAIssuer"}},
			want: true,
		},
		{
			name:    "unknown issuer kind without name",
			tls:     &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Kind: "AWSPCAIssuer"}},
			wantErr: "external issuer requires tls.issuerConf.name",
		},
		{
			name:    "user created namespaced issuer",
			tls:     &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "user-issuer", Kind: cm.IssuerKind}},
			objects: []client.Object{issuer(nil)},
			want:    true,
		},
		{
			name:    "operator owned namespaced issuer",
			tls:     &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "user-issuer", Kind: cm.IssuerKind}},
			objects: []client.Object{issuer(naming.Labels())},
		},
		{
			name:      "user created namespaced issuer on old cr version",
			crVersion: "1.22.0",
			tls:       &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "user-issuer", Kind: cm.IssuerKind}},
			objects:   []client.Object{issuer(nil)},
			want:      true,
		},
		{
			name:    "user created cluster issuer",
			tls:     &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "user-cluster-issuer", Kind: cm.ClusterIssuerKind}},
			objects: []client.Object{clusterIssuer(nil)},
			want:    true,
		},
		{
			name:    "operator owned cluster issuer",
			tls:     &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "user-cluster-issuer", Kind: cm.ClusterIssuerKind}},
			objects: []client.Object{clusterIssuer(naming.Labels())},
		},
		{
			name: "missing cluster issuer",
			tls:  &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "user-cluster-issuer", Kind: cm.ClusterIssuerKind}},
		},
		{
			name:      "missing cluster issuer on old cr version",
			crVersion: "1.22.0",
			tls:       &api.TLSSpec{IssuerConf: cmmeta.IssuerReference{Name: "user-cluster-issuer", Kind: cm.ClusterIssuerKind}},
			want:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newTestCR()
			if tt.crVersion != "" {
				cr.Spec.CRVersion = tt.crVersion
			}
			cr.Spec.TLS = tt.tls
			r := buildFakeClient(append([]client.Object{cr}, tt.objects...)...)

			external, err := r.isExternalIssuer(t.Context(), cr)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, external)
		})
	}
}

func manualTLSSecret(t *testing.T, cr *api.PerconaServerMongoDB, name string, sans []string, caCrt, caKey []byte, owned bool) *corev1.Secret {
	t.Helper()

	tlsCrt, tlsKey, err := tls.IssueWithCA(sans, caCrt, caKey)
	require.NoError(t, err)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
		},
		Data: map[string][]byte{
			"ca.crt":  caCrt,
			"tls.crt": tlsCrt,
			"tls.key": tlsKey,
		},
		Type: corev1.SecretTypeTLS,
	}
	if owned {
		secret.OwnerReferences = []metav1.OwnerReference{
			*metav1.NewControllerRef(cr, api.SchemeGroupVersion.WithKind("PerconaServerMongoDB")),
		}
	}
	return secret
}

func manualCASecret(t *testing.T, cr *api.PerconaServerMongoDB) (*corev1.Secret, []byte, []byte) {
	t.Helper()

	caCrt, caKey, err := tls.IssueCA()
	require.NoError(t, err)

	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      tls.ManualCASecretName(cr),
			Namespace: cr.Namespace,
		},
		Data: map[string][]byte{
			"ca.crt": caCrt,
			"ca.key": caKey,
		},
	}, caCrt, caKey
}

func getTestSecret(t *testing.T, r *ReconcilePerconaServerMongoDB, cr *api.PerconaServerMongoDB, name string) *corev1.Secret {
	t.Helper()

	secret := new(corev1.Secret)
	require.NoError(t, r.client.Get(t.Context(), types.NamespacedName{Name: name, Namespace: cr.Namespace}, secret))
	return secret
}

func certSANs(t *testing.T, secret *corev1.Secret) []string {
	t.Helper()

	sans, err := tls.GetDNSNamesFromCert(secret.Data["tls.crt"])
	require.NoError(t, err)
	sort.Strings(sans)
	return sans
}

func withHorizons(cr *api.PerconaServerMongoDB) *api.PerconaServerMongoDB {
	cr = cr.DeepCopy()
	cr.Spec.Replsets[0].Horizons = api.HorizonsSpec{
		"test-cluster-rs0-0": {"external": "rs0-0.example.com"},
		"test-cluster-rs0-1": {"external": "rs0-1.example.com"},
	}
	return cr
}

func TestReconcileSSL_Manual(t *testing.T) {
	cr := newTestCR()
	cr.UID = "cr-uid"

	expectedSANs := func(cr *api.PerconaServerMongoDB) []string {
		sans := tls.GetCertificateSans(cr)
		sort.Strings(sans)
		return sans
	}

	t.Run("creates CA and secrets", func(t *testing.T) {
		r := buildFakeClient(cr)
		require.NoError(t, r.reconcileSSL(t.Context(), cr))

		ca := getTestSecret(t, r, cr, tls.ManualCASecretName(cr))
		assert.NotEmpty(t, ca.Data["ca.crt"])
		assert.NotEmpty(t, ca.Data["ca.key"])

		for _, name := range []string{api.SSLSecretName(cr), api.SSLInternalSecretName(cr)} {
			secret := getTestSecret(t, r, cr, name)
			assert.Equal(t, ca.Data["ca.crt"], secret.Data["ca.crt"])
			assert.Equal(t, expectedSANs(cr), certSANs(t, secret))
		}
	})

	t.Run("re-signs both secrets when horizons are added", func(t *testing.T) {
		caSecret, caCrt, caKey := manualCASecret(t, cr)
		r := buildFakeClient(cr, caSecret,
			manualTLSSecret(t, cr, api.SSLSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, true),
			manualTLSSecret(t, cr, api.SSLInternalSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, true),
		)

		updated := withHorizons(cr)
		require.NoError(t, r.reconcileSSL(t.Context(), updated))

		assert.Equal(t, caCrt, getTestSecret(t, r, cr, tls.ManualCASecretName(cr)).Data["ca.crt"])
		for _, name := range []string{api.SSLSecretName(cr), api.SSLInternalSecretName(cr)} {
			secret := getTestSecret(t, r, cr, name)
			assert.Equal(t, caCrt, secret.Data["ca.crt"])
			assert.Equal(t, expectedSANs(updated), certSANs(t, secret))
			assert.Contains(t, certSANs(t, secret), "rs0-0.example.com")
		}
	})

	t.Run("creates missing internal secret and re-signs existing one", func(t *testing.T) {
		caSecret, caCrt, caKey := manualCASecret(t, cr)
		r := buildFakeClient(cr, caSecret,
			manualTLSSecret(t, cr, api.SSLSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, true),
		)

		updated := withHorizons(cr)
		require.NoError(t, r.reconcileSSL(t.Context(), updated))

		for _, name := range []string{api.SSLSecretName(cr), api.SSLInternalSecretName(cr)} {
			secret := getTestSecret(t, r, cr, name)
			assert.Equal(t, caCrt, secret.Data["ca.crt"])
			assert.Equal(t, expectedSANs(updated), certSANs(t, secret))
		}
	})

	t.Run("skips re-signing without CA key", func(t *testing.T) {
		_, caCrt, caKey := manualCASecret(t, cr)
		ssl := manualTLSSecret(t, cr, api.SSLSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, true)
		internal := manualTLSSecret(t, cr, api.SSLInternalSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, true)
		r := buildFakeClient(cr, ssl, internal)

		require.NoError(t, r.reconcileSSL(t.Context(), withHorizons(cr)))

		assert.Equal(t, ssl.Data, getTestSecret(t, r, cr, api.SSLSecretName(cr)).Data)
		assert.Equal(t, internal.Data, getTestSecret(t, r, cr, api.SSLInternalSecretName(cr)).Data)
	})

	t.Run("leaves user-provided secret untouched", func(t *testing.T) {
		caSecret, caCrt, caKey := manualCASecret(t, cr)
		ssl := manualTLSSecret(t, cr, api.SSLSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, false)
		internal := manualTLSSecret(t, cr, api.SSLInternalSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, false)
		r := buildFakeClient(cr, caSecret, ssl, internal)

		require.NoError(t, r.reconcileSSL(t.Context(), withHorizons(cr)))

		assert.Equal(t, ssl.Data, getTestSecret(t, r, cr, api.SSLSecretName(cr)).Data)
		assert.Equal(t, internal.Data, getTestSecret(t, r, cr, api.SSLInternalSecretName(cr)).Data)
	})
}

func TestUpdateCertManagerCerts_OldSecretAlreadyExists(t *testing.T) {
	cr := newTestCR()
	cr.Spec.TLS = &api.TLSSpec{}
	_, caCrt, caKey := manualCASecret(t, cr)
	ssl := manualTLSSecret(t, cr, api.SSLSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, true)
	internal := manualTLSSecret(t, cr, api.SSLInternalSecretName(cr), tls.GetCertificateSans(cr), caCrt, caKey, true)
	leftover := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      api.SSLSecretName(cr) + "-old",
			Namespace: cr.Namespace,
		},
		Data: ssl.Data,
	}
	r := buildFakeClient(cr, ssl, internal, leftover)

	require.NoError(t, r.updateCertManagerCerts(t.Context(), cr))

	for _, name := range []string{api.SSLSecretName(cr), api.SSLInternalSecretName(cr)} {
		err := r.client.Get(t.Context(), types.NamespacedName{Name: name + "-old", Namespace: cr.Namespace}, new(corev1.Secret))
		assert.True(t, k8serrors.IsNotFound(err), "%s-old should be cleaned up", name)
	}
}
