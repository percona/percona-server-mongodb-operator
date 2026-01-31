package perconaservermongodb

import (
	"context"
	"os"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Save original env value
			originalValue, wasSet := os.LookupEnv("RECONCILE_INTERVAL")
			defer func() {
				if wasSet {
					os.Setenv("RECONCILE_INTERVAL", originalValue)
				} else {
					os.Unsetenv("RECONCILE_INTERVAL")
				}
			}()

			// Set test env value
			if tt.setEnv {
				os.Setenv("RECONCILE_INTERVAL", tt.envValue)
			} else {
				os.Unsetenv("RECONCILE_INTERVAL")
			}

			got := getReconcileInterval()
			if got != tt.want {
				t.Errorf("getReconcileInterval() = %v, want %v", got, tt.want)
			}
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
})
