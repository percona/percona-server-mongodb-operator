package perconaservermongodb

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"

	cm "github.com/cert-manager/cert-manager/pkg/apis/certmanager/v1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls"
	faketls "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/tls/fake"
	"github.com/percona/percona-server-mongodb-operator/pkg/util"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

type sslCertManagerController struct {
	tls.CertManagerController
	checks  int
	applies int
	err     error
}

func (c *sslCertManagerController) Check(context.Context, *rest.Config, string) error {
	c.checks++
	return c.err
}

func (c *sslCertManagerController) ApplyIssuer(context.Context, *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	c.applies++
	return "", c.err
}

func (c *sslCertManagerController) ApplyCAIssuer(context.Context, *api.PerconaServerMongoDB) (util.ApplyStatus, error) {
	c.applies++
	return "", c.err
}

func TestReconcileSSLCertManagerCheck(t *testing.T) {
	for _, tt := range []struct {
		name                            string
		external, internal, certManager bool
		checks, applies                 int
	}{
		{name: "existing manual secrets", external: true, internal: true},
		{name: "existing cert-manager secrets", external: true, internal: true, certManager: true, applies: 1},
		{name: "missing external secret", internal: true, checks: 1},
		{name: "missing internal secret", external: true, checks: 1},
		{name: "missing both secrets", checks: 1},
		{name: "missing cert-manager internal secret", external: true, certManager: true, checks: 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cr, err := readDefaultCR("cluster", "namespace")
			require.NoError(t, err)
			cr.UID = "cluster-uid"
			cr.Spec.Secrets.SSL = cr.Name + "-ssl"
			cr.Spec.Secrets.SSLInternal = cr.Name + "-ssl-internal"
			controller := true
			var objects []client.Object
			for name, exists := range map[string]bool{
				api.SSLSecretName(cr):         tt.external,
				api.SSLInternalSecretName(cr): tt.internal,
			} {
				if !exists {
					continue
				}
				secret := &corev1.Secret{
					Name: name, Namespace: cr.Namespace,
					OwnerReferences: []metav1.OwnerReference{{UID: cr.UID, Controller: &controller}},
				}
				if tt.certManager {
					secret.Labels = map[string]string{cm.PartOfCertManagerControllerLabelKey: "true"}
				}
				objects = append(objects, secret)
			}
			r := buildFakeClient(objects...)
			stop := errors.New("stop cert-manager operation")
			c := &sslCertManagerController{err: stop}
			r.newCertManagerCtrlFunc = func(client.Client, *runtime.Scheme, bool) tls.CertManagerController {
				return c
			}
			err = r.reconcileSSL(t.Context(), cr)
			if tt.checks > 0 || tt.applies > 0 {
				require.ErrorIs(t, err, stop)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.checks, c.checks)
			require.Equal(t, tt.applies, c.applies)
		})
	}
}

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

func TestReconcileSSLCustomTLSRequiresPBMSecret(t *testing.T) {
	tests := []struct {
		name                 string
		crVersion            string
		backupsEnabled       bool
		tlsMode              api.TLSMode
		pbmSecretExists      bool
		certManagementPolicy api.CertManagementPolicy
		expectError          bool
	}{
		{
			name:           "missing PBM secret returns an error",
			backupsEnabled: true,
			tlsMode:        api.TLSModeRequire,
			expectError:    true,
		},
		{
			name:            "existing PBM secret is preserved",
			backupsEnabled:  true,
			tlsMode:         api.TLSModeRequire,
			pbmSecretExists: true,
		},
		{
			name:                 "userProvidedOnly requires a PBM secret",
			backupsEnabled:       true,
			tlsMode:              api.TLSModeRequire,
			certManagementPolicy: api.CertManagementUserProvidedOnly,
			expectError:          true,
		},
		{
			name:                 "userProvidedOnly accepts an existing PBM secret",
			backupsEnabled:       true,
			tlsMode:              api.TLSModeRequire,
			pbmSecretExists:      true,
			certManagementPolicy: api.CertManagementUserProvidedOnly,
		},
		{
			name:           "CR versions before 1.24 do not require a PBM secret",
			crVersion:      "1.23.0",
			backupsEnabled: true,
			tlsMode:        api.TLSModeRequire,
		},
		{
			name:    "disabled backups do not require a PBM secret",
			tlsMode: api.TLSModeRequire,
		},
		{
			name:           "disabled TLS does not require a PBM secret",
			backupsEnabled: true,
			tlsMode:        api.TLSModeDisabled,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.crVersion == "" {
				tt.crVersion = version.Version()
			}

			cr := newTestCR()
			cr.Spec.CRVersion = tt.crVersion
			cr.Spec.Backup.Enabled = tt.backupsEnabled
			cr.Spec.TLS = &api.TLSSpec{
				Mode:                 tt.tlsMode,
				CertManagementPolicy: tt.certManagementPolicy,
			}
			cr.Spec.Secrets.SSLPBM = "custom-pbm-tls"

			serverSecret := &corev1.Secret{
				Name:        api.SSLSecretName(cr),
				Namespace:   cr.Namespace,
				Labels:      map[string]string{cm.PartOfCertManagerControllerLabelKey: "true"},
				Annotations: map[string]string{cm.CertificateNameKey: "user-server-cert"},
			}
			r := buildFakeClient(cr, serverSecret)
			pbmSecret := &corev1.Secret{
				Name:      cr.Spec.Secrets.SSLPBM,
				Namespace: cr.Namespace,
				Data: map[string][]byte{
					"tls.crt": []byte("client-cert"),
					"tls.key": []byte("client-key"),
				},
			}
			if tt.pbmSecretExists {
				require.NoError(t, r.client.Create(t.Context(), pbmSecret))
			}

			err := r.reconcileSSL(t.Context(), cr)
			if tt.expectError {
				require.EqualError(t, err, "PBM client TLS secret custom-pbm-tls is required when custom SSL secret test-cluster-ssl is provided and backups are enabled")
			} else {
				require.NoError(t, err)
			}

			actualPBMSecret, err := r.getSecret(t.Context(), cr, tls.CertificatePBMClient(cr).SecretName())
			if tt.pbmSecretExists {
				require.NoError(t, err)
				require.Equal(t, pbmSecret, actualPBMSecret, "reconciliation must preserve the supplied PBM secret")
			} else {
				require.True(t, k8serrors.IsNotFound(err), "reconciliation must not create a PBM secret for custom TLS")
			}
		})
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

func TestSSLAnnotation(t *testing.T) {
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
						naming.AnnotationPBMTLSHash:      "existing-pbm-hash",
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

	pbmSecret := &corev1.Secret{
		Name:      "custom-pbm-tls",
		Namespace: "test-ns",
		Data:      map[string][]byte{"tls.crt": []byte("pbm-cert")},
	}

	tests := []struct {
		name                 string
		objects              []client.Object
		checkAnnotation      func(t *testing.T, ann map[string]string)
		wantSecretsReadyCond bool
		backupsEnabled       bool
		tlsDisabled          bool
		policy               api.CertManagementPolicy
		wantTLSNotReady      bool
		wantMissingSecret    string
	}{
		{
			name:    "secrets missing — preserves existing sts annotations",
			objects: []client.Object{sts},
			checkAnnotation: func(t *testing.T, ann map[string]string) {
				assert.Equal(t, "existing-hash", ann[naming.AnnotationSSLHash])
				assert.Equal(t, "existing-internal-hash", ann[naming.AnnotationSSLInternalHash])
				assert.Equal(t, "existing-pbm-hash", ann[naming.AnnotationPBMTLSHash])
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
		{
			name:              "missing PBM secret preserves existing annotations",
			objects:           []client.Object{sts, sslSecret, sslInternalSecret},
			backupsEnabled:    true,
			wantMissingSecret: pbmSecret.Name,
			checkAnnotation: func(t *testing.T, ann map[string]string) {
				assert.Equal(t, sts.Spec.Template.Annotations, ann)
			},
		},
		{
			name:                 "existing PBM secret computes fresh hashes",
			objects:              []client.Object{sslSecret, sslInternalSecret, pbmSecret},
			backupsEnabled:       true,
			wantSecretsReadyCond: true,
			checkAnnotation: func(t *testing.T, ann map[string]string) {
				assert.NotEmpty(t, ann[naming.AnnotationSSLHash])
				assert.NotEmpty(t, ann[naming.AnnotationSSLInternalHash])
				assert.NotEmpty(t, ann[naming.AnnotationPBMTLSHash])
				assert.NotEqual(t, "existing-pbm-hash", ann[naming.AnnotationPBMTLSHash])
			},
		},
		{
			name:                 "disabled TLS does not require PBM secret",
			objects:              []client.Object{sslSecret, sslInternalSecret},
			backupsEnabled:       true,
			tlsDisabled:          true,
			wantSecretsReadyCond: true,
			checkAnnotation: func(t *testing.T, ann map[string]string) {
				assert.NotContains(t, ann, naming.AnnotationPBMTLSHash)
			},
		},
		{
			name:            "auto waits for missing PBM secret",
			objects:         []client.Object{sslSecret, sslInternalSecret},
			backupsEnabled:  true,
			policy:          api.CertManagementAuto,
			wantTLSNotReady: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newTestCR()
			cr.Spec.Backup.Enabled = tt.backupsEnabled
			cr.Spec.Secrets.SSLPBM = pbmSecret.Name
			cr.Spec.TLS = &api.TLSSpec{
				Mode:                 api.TLSModeRequire,
				CertManagementPolicy: api.CertManagementUserProvidedOnly,
			}

			if tt.policy != "" {
				cr.Spec.TLS.CertManagementPolicy = tt.policy
			}
			if tt.tlsDisabled {
				cr.Spec.TLS.Mode = api.TLSModeDisabled
				cr.Spec.Unsafe.TLS = true
			}

			objs := append([]client.Object{cr}, tt.objects...)
			r := buildFakeClient(objs...)
			annotation, err := r.sslAnnotation(t.Context(), cr)
			if tt.wantTLSNotReady {
				require.ErrorIs(t, err, errTLSNotReady)
				return
			}
			require.NoError(t, err)

			if tt.wantMissingSecret != "" {
				require.Len(t, cr.Status.Conditions, 1)
				condition := cr.Status.Conditions[0]
				assert.Equal(t, api.ConditionTypeTLSSecretsReady, condition.Type)
				assert.Equal(t, api.ConditionFalse, condition.Status)
				assert.Equal(t, "TLSSecretNotFound", condition.Reason)
				assert.Contains(t, condition.Message, tt.wantMissingSecret)
			}
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

func TestNeedsManualSSLUpdateVersionGate(t *testing.T) {
	sslSecret := func(t *testing.T, cr *api.PerconaServerMongoDB, sans []string) *corev1.Secret {
		t.Helper()

		caCert, caKey, err := tls.IssueCA()
		require.NoError(t, err)

		tlsCert, _, err := tls.IssueWithCA(sans, caCert, caKey)
		require.NoError(t, err)

		return &corev1.Secret{
			Name: api.SSLSecretName(cr), Namespace: cr.Namespace,
			Data: map[string][]byte{"tls.crt": tlsCert},
		}
	}

	tests := map[string]struct {
		crVersion string
		extraSan  string
		dropSan   string
		expected  bool
	}{
		"1.23.0 reissues on extra san":      {crVersion: "1.23.0", extraSan: "stale.example.com", expected: true},
		"1.24.0 keeps cert with extra san":  {crVersion: "1.24.0", extraSan: "stale.example.com"},
		"1.23.0 reissues on missing san":    {crVersion: "1.23.0", dropSan: "test-cluster-rs0", expected: true},
		"1.24.0 reissues on missing san":    {crVersion: "1.24.0", dropSan: "test-cluster-rs0", expected: true},
		"1.23.0 keeps cert with exact sans": {crVersion: "1.23.0"},
		"1.24.0 keeps cert with exact sans": {crVersion: "1.24.0"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newTestCR()
			cr.Spec.CRVersion = tt.crVersion

			sans := tls.GetCertificateSans(cr)
			if tt.extraSan != "" {
				sans = append(sans, tt.extraSan)
			}
			if tt.dropSan != "" {
				sans = slices.DeleteFunc(sans, func(san string) bool { return san == tt.dropSan })
			}

			r := &ReconcilePerconaServerMongoDB{}

			assert.Equal(t, tt.expected, r.needsManualSSLUpdate(t.Context(), cr, sslSecret(t, cr, sans)))
		})
	}
}

func TestUpdateMongosFirstIfTLSOutdated(t *testing.T) {
	newCR := func(allowInvalidCerts bool, mongosSize int32) *api.PerconaServerMongoDB {
		cr := newTestCR()
		cr.Spec.UpdateStrategy = api.SmartUpdateStatefulSetStrategyType
		cr.Spec.TLS = &api.TLSSpec{
			Mode:                     api.TLSModeRequire,
			AllowInvalidCertificates: new(allowInvalidCerts),
		}
		cr.Spec.Sharding = api.Sharding{
			Enabled: true,
			Mongos:  &api.MongosSpec{Size: mongosSize},
		}
		return cr
	}
	current := map[string]string{
		naming.AnnotationSSLHash:         "current-hash",
		naming.AnnotationSSLInternalHash: "current-internal-hash",
	}

	tests := []struct {
		name              string
		allowInvalidCerts bool
		mongosSize        int32
		podHashes         []string
		wantMongosFirst   bool
	}{
		{
			name:            "mongos runs an outdated certificate",
			mongosSize:      2,
			podHashes:       []string{"current", "outdated"},
			wantMongosFirst: true,
		},
		{
			name:            "mongos runs an outdated internal certificate",
			mongosSize:      2,
			podHashes:       []string{"current", "outdated-internal"},
			wantMongosFirst: true,
		},
		{
			name:            "mongos runs the current certificate",
			mongosSize:      2,
			podHashes:       []string{"current", "current"},
			wantMongosFirst: false,
		},
		{
			name:            "no mongos pods",
			mongosSize:      2,
			wantMongosFirst: false,
		},
		{
			name:            "mongos is scaled to zero",
			podHashes:       []string{"outdated", "outdated"},
			wantMongosFirst: false,
		},
		{
			name:              "hostname verification is disabled",
			allowInvalidCerts: true,
			mongosSize:        2,
			podHashes:         []string{"outdated", "outdated"},
			wantMongosFirst:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newCR(tt.allowInvalidCerts, tt.mongosSize)
			objs := []client.Object{cr}

			for i, hash := range tt.podHashes {
				ann := maps.Clone(current)
				switch hash {
				case "outdated":
					ann[naming.AnnotationSSLHash] = "outdated-hash"
				case "outdated-internal":
					ann[naming.AnnotationSSLInternalHash] = "outdated-internal-hash"
				}
				objs = append(objs, &corev1.Pod{
					Name:        fmt.Sprintf("test-cluster-mongos-%d", i),
					Namespace:   "test-ns",
					Labels:      naming.MongosLabels(cr),
					Annotations: ann,
				})
			}

			r := buildFakeClient(objs...)

			cfg := &appsv1.StatefulSet{}
			cfg.Spec.Template.Annotations = maps.Clone(current)

			require.NoError(t, r.updateMongosFirstIfTLSOutdated(t.Context(), cr, cfg))

			mongosFirst, err := r.shouldUpdateMongosFirst(t.Context(), cr)
			require.NoError(t, err)
			assert.Equal(t, tt.wantMongosFirst, mongosFirst)

			stored := new(api.PerconaServerMongoDB)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), stored))
			_, ok := stored.Annotations[api.AnnotationUpdateMongosFirst]
			assert.Equal(t, tt.wantMongosFirst, ok, "annotation must be stored in the API server")
		})
	}
}
