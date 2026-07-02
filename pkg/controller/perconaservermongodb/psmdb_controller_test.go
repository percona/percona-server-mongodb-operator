package perconaservermongodb

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestGetReconcileInterval(t *testing.T) {
	tests := []struct {
		name     string
		envValue string
		setEnv   bool
		want     time.Duration
	}{
		{
			name:   "unset",
			setEnv: false,
			want:   5 * time.Second,
		},
		{
			name:     "valid duration",
			envValue: "30s",
			setEnv:   true,
			want:     30 * time.Second,
		},
		{
			name:     "invalid duration falls back to default",
			envValue: "invalid",
			setEnv:   true,
			want:     5 * time.Second,
		},
		{
			name:     "zero duration falls back to default",
			envValue: "0s",
			setEnv:   true,
			want:     5 * time.Second,
		},
		{
			name:     "negative duration falls back to default",
			envValue: "-5s",
			setEnv:   true,
			want:     5 * time.Second,
		},
		{
			name:     "duration less than 5s falls back to default",
			envValue: "1s",
			setEnv:   true,
			want:     5 * time.Second,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				err := os.Unsetenv("RECONCILE_INTERVAL")
				require.NoError(t, err)
			}()
			if tt.setEnv {
				err := os.Setenv("RECONCILE_INTERVAL", tt.envValue)
				require.NoError(t, err)
			}

			got := getReconcileInterval()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestEnsureSecurityKeys(t *testing.T) {
	tests := []struct {
		name                    string
		crVersion               string
		vaultSecret             string
		wantEncryptionKeySecret bool
	}{
		{
			name:                    "creates security keys without vault",
			crVersion:               "1.23.0",
			wantEncryptionKeySecret: true,
		},
		{
			name:                    "creates security keys with vault before 1.23.0",
			crVersion:               "1.22.0",
			vaultSecret:             "vault-secret",
			wantEncryptionKeySecret: true,
		},
		{
			name:        "skips encryption key with vault since 1.23.0",
			crVersion:   "1.23.0",
			vaultSecret: "vault-secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := &psmdbv1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "some-cluster",
					Namespace: "some-ns",
				},
				Spec: psmdbv1.PerconaServerMongoDBSpec{
					CRVersion: tt.crVersion,
					Secrets: &psmdbv1.SecretsSpec{
						EncryptionKey: "cluster1-mongodb-encryption-key",
						Vault:         tt.vaultSecret,
					},
				},
			}
			s := runtime.NewScheme()
			require.NoError(t, corev1.AddToScheme(s))
			require.NoError(t, psmdbv1.SchemeBuilder.AddToScheme(s))
			cl := fake.NewClientBuilder().WithScheme(s).Build()
			r := &ReconcilePerconaServerMongoDB{
				client: cl,
				scheme: s,
			}

			require.NoError(t, r.ensureSecurityKeys(t.Context(), cr))

			err := cl.Get(t.Context(), types.NamespacedName{Name: cr.Spec.Secrets.GetInternalKey(cr), Namespace: cr.Namespace}, &corev1.Secret{})
			assert.NoError(t, err)

			err = cl.Get(t.Context(), types.NamespacedName{Name: cr.Spec.Secrets.EncryptionKey, Namespace: cr.Namespace}, &corev1.Secret{})
			if tt.wantEncryptionKeySecret {
				assert.NoError(t, err)
				return
			}
			assert.True(t, k8serrors.IsNotFound(err), "expected %s to be absent, got %v", cr.Spec.Secrets.EncryptionKey, err)
		})
	}
}

var _ = Describe("PerconaServerMongoDB", Ordered, func() {
	ctx := context.Background()
	const ns = "psmdb"
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ns,
			Namespace: ns,
		},
	}
	crName := ns + "-reconciler"
	crNamespacedName := types.NamespacedName{Name: crName, Namespace: ns}

	BeforeAll(func() {
		By("Creating the Namespace to perform the tests")
		err := k8sClient.Create(ctx, namespace)
		Expect(err).To(Not(HaveOccurred()))
	})

	AfterAll(func() {
		By("Deleting the Namespace to perform the tests")
		_ = k8sClient.Delete(ctx, namespace)
	})

	Context("Create PerconaServerMongoDB", func() {
		cr, err := readDefaultCR(crName, ns)
		It("should read defautl cr.yaml", func() {
			Expect(err).NotTo(HaveOccurred())
		})

		It("Should create PerconaServerMongoDB", func() {
			Expect(k8sClient.Create(ctx, cr)).Should(Succeed())
		})
	})

	It("Should reconcile PerconaServerMongoDB", func() {
		_, err := reconciler().Reconcile(ctx, reconcile.Request{
			NamespacedName: crNamespacedName,
		})
		Expect(err).To(Succeed())
	})
})

var _ = Describe("PerconaServerMongoDB CRD Validation", Ordered, func() {
	ctx := context.Background()
	const ns = "psmdb-validation"
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ns,
			Namespace: ns,
		},
	}

	BeforeAll(func() {
		By("Creating the Namespace to perform the tests")
		err := k8sClient.Create(ctx, namespace)
		Expect(err).To(Not(HaveOccurred()))
	})

	AfterAll(func() {
		By("Deleting the Namespace to perform the tests")
		_ = k8sClient.Delete(ctx, namespace)
	})

	Context("StorageScaling validation", func() {
		It("should reject autoscaling enabled when enableVolumeScaling is disabled", func() {
			cr, err := readDefaultCR("psmdb-invalid-autoscaling", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.StorageScaling = &psmdbv1.StorageScalingSpec{
				EnableVolumeScaling: false,
				Autoscaling: &psmdbv1.AutoscalingSpec{
					Enabled: true,
				},
			}

			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("autoscaling cannot be enabled when enableVolumeScaling is disabled"))
		})

		It("should allow autoscaling enabled when enableVolumeScaling is enabled", func() {
			cr, err := readDefaultCR("psmdb-valid-autoscaling", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.StorageScaling = &psmdbv1.StorageScalingSpec{
				EnableVolumeScaling: true,
				Autoscaling: &psmdbv1.AutoscalingSpec{
					Enabled: true,
				},
			}

			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should reject autoscaling enabled when enableExternalAutoscaling is enabled", func() {
			cr, err := readDefaultCR("psmdb-invalid-external-autoscaling", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.StorageScaling = &psmdbv1.StorageScalingSpec{
				EnableVolumeScaling:       true,
				EnableExternalAutoscaling: true,
				Autoscaling: &psmdbv1.AutoscalingSpec{
					Enabled: true,
				},
			}

			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("autoscaling cannot be enabled when enableExternalAutoscaling is enabled"))
		})

		It("should allow autoscaling enabled when enableExternalAutoscaling is disabled", func() {
			cr, err := readDefaultCR("psmdb-valid-external-autoscaling", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.StorageScaling = &psmdbv1.StorageScalingSpec{
				EnableVolumeScaling:       true,
				EnableExternalAutoscaling: false,
				Autoscaling: &psmdbv1.AutoscalingSpec{
					Enabled: true,
				},
			}

			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("Replset volumeSpec validation", func() {
		It("should reject a replset without volumeSpec", func() {
			cr, err := readDefaultCR("psmdb-no-volumespec-rs0", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.Replsets[0].VolumeSpec = nil

			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("volumeSpec: Required value"))
		})

		It("should reject an additional replset without volumeSpec", func() {
			cr, err := readDefaultCR("psmdb-no-volumespec-rs1", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.Replsets = append(cr.Spec.Replsets, &psmdbv1.ReplsetSpec{
				Name: "rs1",
				Size: 3,
			})

			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("volumeSpec: Required value"))
		})

		It("should allow an additional replset that specifies volumeSpec", func() {
			cr, err := readDefaultCR("psmdb-rs1-with-volumespec", ns)
			Expect(err).NotTo(HaveOccurred())

			rs1 := cr.Spec.Replsets[0].DeepCopy()
			rs1.Name = "rs1"
			cr.Spec.Replsets = append(cr.Spec.Replsets, rs1)

			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})
