package perconaservermongodb

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
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
			Expect(err.Error()).To(ContainSubstring("exactly one of volumeSpec or instances[] must be set"))
		})

		It("should reject an additional replset without volumeSpec", func() {
			cr, err := readDefaultCR("psmdb-no-volumespec-rs1", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.Replsets = append(cr.Spec.Replsets, &psmdbv1.ReplsetSpec{
				Name: "rs1",
				Size: new(int32(3)),
			})

			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("exactly one of volumeSpec or instances[] must be set"))
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

	Context("PMM querySource validation", func() {
		It("should reject mongolog query source when logcollector is disabled", func() {
			cr, err := readDefaultCR("psmdb-mongolog-lc-disabled", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.PMM.Enabled = true
			cr.Spec.PMM.QuerySource = "mongolog"
			cr.Spec.LogCollector = &psmdbv1.LogCollectorSpec{Enabled: false}

			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("pmm.querySource 'mongolog' requires logcollector to be enabled"))
		})

		It("should reject mongolog query source when logcollector is not set", func() {
			cr, err := readDefaultCR("psmdb-mongolog-no-lc", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.PMM.Enabled = true
			cr.Spec.PMM.QuerySource = "mongolog"
			cr.Spec.LogCollector = nil

			err = k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("pmm.querySource 'mongolog' requires logcollector to be enabled"))
		})

		It("should allow mongolog query source when logcollector is enabled", func() {
			cr, err := readDefaultCR("psmdb-mongolog-lc-enabled", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.PMM.Enabled = true
			cr.Spec.PMM.QuerySource = "mongolog"
			cr.Spec.LogCollector = &psmdbv1.LogCollectorSpec{Enabled: true}

			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow profiler query source when logcollector is disabled", func() {
			cr, err := readDefaultCR("psmdb-profiler-lc-disabled", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.PMM.Enabled = true
			cr.Spec.PMM.QuerySource = "profiler"
			cr.Spec.LogCollector = &psmdbv1.LogCollectorSpec{Enabled: false}

			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow mongolog query source when pmm is disabled", func() {
			cr, err := readDefaultCR("psmdb-mongolog-pmm-disabled", ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.PMM.Enabled = false
			cr.Spec.PMM.QuerySource = "mongolog"
			cr.Spec.LogCollector = &psmdbv1.LogCollectorSpec{Enabled: false}

			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})

	Context("OCI storage credentials validation", func() {
		crWithOCIStorage := func(name string, creds psmdbv1.OCICredentialsSpec) *psmdbv1.PerconaServerMongoDB {
			cr, err := readDefaultCR(name, ns)
			Expect(err).NotTo(HaveOccurred())

			cr.Spec.Backup.Storages = map[string]psmdbv1.BackupStorageSpec{
				"oci-storage": {
					Type: psmdbv1.BackupStorageOCI,
					OCI: psmdbv1.BackupStorageOCISpec{
						Region:      "us-ashburn-1",
						Namespace:   "some-namespace",
						Bucket:      "operator-testing",
						Credentials: creds,
					},
				},
			}

			return cr
		}

		It("should reject userPrincipal credentials without secretName", func() {
			cr := crWithOCIStorage("psmdb-oci-no-secret-name", psmdbv1.OCICredentialsSpec{
				Type: psmdbv1.AuthTypeUserPrincipal,
			})

			err := k8sClient.Create(ctx, cr)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("secretName must be set when credentials type is userPrincipal"))
		})

		It("should allow userPrincipal credentials with secretName", func() {
			cr := crWithOCIStorage("psmdb-oci-with-secret-name", psmdbv1.OCICredentialsSpec{
				Type:       psmdbv1.AuthTypeUserPrincipal,
				SecretName: "oci-secret",
			})

			err := k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should allow instancePrincipal credentials without secretName", func() {
			cr := crWithOCIStorage("psmdb-oci-instance-principal", psmdbv1.OCICredentialsSpec{
				Type: psmdbv1.AuthTypeInstancePrincipal,
			})

			err := k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should not trigger for non-OCI storages", func() {
			cr, err := readDefaultCR("psmdb-s3-storage", ns)
			Expect(err).NotTo(HaveOccurred())

			// OCI is a value field in BackupStorageSpec, so an S3 storage still
			// serializes an empty oci.credentials object the CEL rule runs against.
			cr.Spec.Backup.Storages = map[string]psmdbv1.BackupStorageSpec{
				"s3-storage": {
					Type: psmdbv1.BackupStorageS3,
					S3: psmdbv1.BackupStorageS3Spec{
						Region:            "us-east-1",
						Bucket:            "operator-testing",
						CredentialsSecret: "s3-secret",
					},
				},
			}

			err = k8sClient.Create(ctx, cr)
			Expect(err).NotTo(HaveOccurred())
		})
	})
})

// voting and nonVotingInst build instance groups for the downscale tables.
func voting(name string, replicas int32) psmdbv1.InstanceSpec {
	return psmdbv1.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
		RSConfig: &psmdbv1.MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(2))}}
}

func nonVotingInst(name string, replicas int32) psmdbv1.InstanceSpec {
	return psmdbv1.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
		RSConfig: &psmdbv1.MemberConfigSpec{Votes: new(int32(0)), Priority: new(int32(0))}}
}

func TestDownscaleTarget(t *testing.T) {
	for _, tt := range []struct {
		name        string
		declared    []psmdbv1.InstanceSpec
		observed    map[string]int32
		nilReplicas map[string]bool
		want        map[string]int32
	}{
		{
			name:     "nothing to do",
			declared: []psmdbv1.InstanceSpec{voting("mongod", 3)},
			observed: map[string]int32{"mongod": 3},
			want:     map[string]int32{"ds-cr-rs0": 3},
		},
		{
			name:     "a gap of one is taken in a single pass",
			declared: []psmdbv1.InstanceSpec{voting("mongod", 2)},
			observed: map[string]int32{"mongod": 3},
			want:     map[string]int32{"ds-cr-rs0": 2},
		},
		{
			name:     "a larger gap sheds one member per pass",
			declared: []psmdbv1.InstanceSpec{voting("mongod", 2)},
			observed: map[string]int32{"mongod": 5},
			want:     map[string]int32{"ds-cr-rs0": 4},
		},
		{
			name:     "the second voting group is held at its observed size",
			declared: []psmdbv1.InstanceSpec{voting("a", 1), voting("b", 1)},
			observed: map[string]int32{"a": 3, "b": 3},
			want:     map[string]int32{"ds-cr-rs0-a": 2, "ds-cr-rs0-b": 3},
		},
		{
			name:     "a group declared non-voting is also rate limited",
			declared: []psmdbv1.InstanceSpec{voting("mongod", 3), nonVotingInst("nv", 0)},
			observed: map[string]int32{"mongod": 3, "nv": 3},
			want:     map[string]int32{"ds-cr-rs0": 3, "ds-cr-rs0-nv": 2},
		},
		{
			name: "votes and replicas dropped together still step down one at a time",
			declared: []psmdbv1.InstanceSpec{
				voting("inst1", 3), nonVotingInst("inst2", 0),
			},
			observed: map[string]int32{"inst1": 3, "inst2": 4},
			want:     map[string]int32{"ds-cr-rs0-inst1": 3, "ds-cr-rs0-inst2": 3},
		},
		{
			name:     "a group declared non-voting waits for the budget",
			declared: []psmdbv1.InstanceSpec{voting("mongod", 2), nonVotingInst("nv", 0)},
			observed: map[string]int32{"mongod": 5, "nv": 3},
			want:     map[string]int32{"ds-cr-rs0": 4, "ds-cr-rs0-nv": 3},
		},
		{
			name:     "a group with no statefulset yet is left at its declared count",
			declared: []psmdbv1.InstanceSpec{voting("hot", 3)},
			observed: nil,
			want:     map[string]int32{"ds-cr-rs0-hot": 3},
		},
		{
			name:        "a statefulset with no replicas set is left alone",
			declared:    []psmdbv1.InstanceSpec{voting("hot", 3)},
			observed:    map[string]int32{"hot": 3},
			nilReplicas: map[string]bool{"hot": true},
			want:        map[string]int32{"ds-cr-rs0-hot": 3},
		},
		{
			name:     "an upscale passes through",
			declared: []psmdbv1.InstanceSpec{voting("mongod", 5)},
			observed: map[string]int32{"mongod": 3},
			want:     map[string]int32{"ds-cr-rs0": 5},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cr := instanceCR(t, "ds-cr", "ds", tt.declared, unsafeSize)
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			objs := []client.Object{cr}
			for name, replicas := range tt.observed {
				g := resolveGroup(t, cr, rs, name)
				sts := groupSTS(cr, rs, g, replicas, replicas)
				if tt.nilReplicas[name] {
					sts.Spec.Replicas = nil
				}
				objs = append(objs, sts)
			}

			r := buildFakeClient(objs...)

			got, err := r.downscaleTarget(ctx, cr, set)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestDownscaleTargetBudgetsRetiringWorkloads(t *testing.T) {
	ctx := t.Context()

	declared := []psmdbv1.InstanceSpec{voting("data", 3), voting("analytics", 4)}
	cr := instanceCR(t, "ds-cr", "ds", declared, unsafeSize)
	rs := cr.Spec.Replsets[0]

	full, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	objs := []client.Object{cr}
	for _, g := range full.GetAll() {
		objs = append(objs, groupSTS(cr, rs, g, g.Replicas, g.Replicas))
	}
	r := buildFakeClient(objs...)

	// The user deletes analytics and shrinks data in the same edit.
	rs.Instances = rs.Instances[:1]
	rs.Instances[0].Replicas = 1
	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	got, err := r.downscaleTarget(ctx, cr, set)
	require.NoError(t, err)

	assert.Equal(t, int32(3), got["ds-cr-rs0-analytics"],
		"the retiring workload sheds one member")
	assert.Equal(t, int32(3), got["ds-cr-rs0-data"],
		"budget spent, so the declared group is held at its observed size")

	// Once the retiring workload is empty the budget frees up again.
	sts := new(appsv1.StatefulSet)
	require.NoError(t, r.client.Get(ctx,
		client.ObjectKey{Name: "ds-cr-rs0-analytics", Namespace: cr.Namespace}, sts))
	sts.Spec.Replicas = new(int32(0))
	require.NoError(t, r.client.Update(ctx, sts))

	got, err = r.downscaleTarget(ctx, cr, set)
	require.NoError(t, err)

	assert.Equal(t, int32(0), got["ds-cr-rs0-analytics"])
	assert.Equal(t, int32(2), got["ds-cr-rs0-data"], "the declared group resumes shrinking")
}

func TestSafeDownscale(t *testing.T) {
	t.Run("legacy", func(t *testing.T) {
		ctx := t.Context()

		cr := legacyCR(t, "ds-cr", "ds", func(c *psmdbv1.PerconaServerMongoDB) {
			c.Spec.Unsafe.ReplsetSize = true
			c.Spec.Replsets[0].Size = new(int32(2))
			c.Spec.Replsets[0].NonVoting = psmdbv1.NonVotingSpec{Enabled: true, Size: 1}
			c.Spec.Replsets[0].Hidden = psmdbv1.HiddenSpec{Enabled: true, Size: 1}
		})
		rs := cr.Spec.Replsets[0]

		set, err := membergroup.Resolve(cr, rs)
		require.NoError(t, err)

		objs := []client.Object{cr}
		for _, g := range set.GetAll() {
			// every workload is larger than its declared size
			objs = append(objs, groupSTS(cr, rs, g, g.Replicas+3, g.Replicas+3))
		}
		r := buildFakeClient(objs...)

		isDownscale, err := r.safeDownscale(ctx, cr)
		require.NoError(t, err)
		assert.True(t, isDownscale)

		assert.Equal(t, int32(4), rs.GetMongodSize(),
			"the base group is stepped down by one, into rs.size")
		assert.Equal(t, int32(4), rs.NonVoting.Size,
			"a non-voting role is rate limited too, and this pass's budget is spent")
		assert.Equal(t, int32(4), rs.Hidden.Size,
			"hidden is pinned at its observed size until the budget frees up")
	})

	t.Run("instances", func(t *testing.T) {
		ctx := t.Context()

		cr := instanceCR(t, "ds-cr", "ds", []psmdbv1.InstanceSpec{voting("hot", 1)}, unsafeSize)
		rs := cr.Spec.Replsets[0]

		g := resolveGroup(t, cr, rs, "hot")
		r := buildFakeClient(cr, groupSTS(cr, rs, g, 5, 5))

		isDownscale, err := r.safeDownscale(ctx, cr)
		require.NoError(t, err)
		assert.True(t, isDownscale)

		assert.Equal(t, int32(4), rs.Instance("hot").Replicas,
			"the instance entry carries the intermediate count")
		assert.Equal(t, int32(0), rs.GetMongodSize(),
			"rs.size is not written in instance mode")
	})
}

var _ = Describe("instances[] CRD validation", func() {
	const ns = "default"

	ctx := context.Background()

	var counter int
	// applyCR builds a valid default CR, hands it to mutate, and returns the
	// error from the API server. Each spec gets its own name so a rejected
	// create cannot collide with an accepted one.
	applyCR := func(mutate func(*psmdbv1.PerconaServerMongoDB)) error {
		counter++
		cr, err := readDefaultCR(fmt.Sprintf("cel-%d", counter), ns)
		Expect(err).NotTo(HaveOccurred())

		mutate(cr)

		return k8sClient.Create(ctx, cr)
	}

	// instanceMode rewrites rs0 as instances[], clearing everything the CRD
	// requires absent in that mode.
	instanceMode := func(cr *psmdbv1.PerconaServerMongoDB, instances ...psmdbv1.InstanceSpec) {
		rs := cr.Spec.Replsets[0]
		rs.Size = new(int32(0))
		rs.VolumeSpec = nil
		rs.Arbiter = psmdbv1.Arbiter{}
		rs.NonVoting = psmdbv1.NonVotingSpec{}
		rs.Hidden = psmdbv1.HiddenSpec{}
		rs.Instances = instances
	}

	vol := func() *psmdbv1.VolumeSpec {
		return cloneVolumeSpec()
	}
	dataInstance := func(name string, replicas int32) psmdbv1.InstanceSpec {
		return psmdbv1.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: vol()}
	}
	arbiterInstance := func(name string) psmdbv1.InstanceSpec {
		return psmdbv1.InstanceSpec{Name: name, Replicas: 1,
			RSConfig: &psmdbv1.MemberConfigSpec{ArbiterOnly: new(true)}}
	}

	DescribeTable("rejects a malformed topology",
		func(mutate func(*psmdbv1.PerconaServerMongoDB), wantMessage string) {
			err := applyCR(mutate)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring(wantMessage))
		},

		// --- the two formats are exclusive -------------------------------
		Entry("both volumeSpec and instances[]",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3))
				cr.Spec.Replsets[0].VolumeSpec = vol()
			},
			"exactly one of volumeSpec or instances[] must be set"),
		Entry("size alongside instances[]",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3))
				cr.Spec.Replsets[0].Size = new(int32(3))
			},
			"size must be absent when instances[] is set"),
		Entry("arbiter alongside instances[]",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3))
				cr.Spec.Replsets[0].Arbiter = psmdbv1.Arbiter{Enabled: true, Size: 1}
			},
			"arbiter must be absent when instances[] is set"),
		Entry("nonvoting alongside instances[]",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3))
				cr.Spec.Replsets[0].NonVoting = psmdbv1.NonVotingSpec{Enabled: true, Size: 1}
			},
			"nonvoting must be absent when instances[] is set"),
		Entry("hidden alongside instances[]",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3))
				cr.Spec.Replsets[0].Hidden = psmdbv1.HiddenSpec{Enabled: true, Size: 1}
			},
			"hidden must be absent when instances[] is set"),

		// --- the config server is homogeneous ----------------------------
		Entry("the config server declares instances[]",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				cr.Spec.Sharding.Enabled = true
				cr.Spec.Sharding.ConfigsvrReplSet.Instances = []psmdbv1.InstanceSpec{dataInstance("hot", 3)}
			},
			"cannot declare configsvrReplSet using instances[]"),
		Entry("a shard with clusterRole configsvr declares instances[]",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3))
				cr.Spec.Replsets[0].ClusterRole = psmdbv1.ClusterRoleConfigSvr
			},
			"configServer cannot have instances[]"),

		// --- storage ownership -------------------------------------------
		Entry("a data-bearing instance without volumeSpec",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, psmdbv1.InstanceSpec{Name: "hot", Replicas: 3})
			},
			"volumeSpec is required for a data-bearing instance"),
		Entry("an arbiter instance with a volumeSpec",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				arb := arbiterInstance("arb")
				arb.VolumeSpec = vol()
				instanceMode(cr, dataInstance("hot", 3), arb)
			},
			"arbiterOnly instance must not declare volumeSpec"),

		// --- reserved names ----------------------------------------------
		Entry("a name that collides with a generated object",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("nv", 3))
			},
			"instance name is reserved by the operator"),
		Entry("a name that collides with a component label",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("mongos", 3))
			},
			"instance name is reserved by the operator"),

		// --- member configuration coherence -------------------------------
		Entry("an arbiter that is also hidden",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				arb := arbiterInstance("arb")
				arb.RSConfig.Hidden = new(true)
				instanceMode(cr, dataInstance("hot", 3), arb)
			},
			"arbiterOnly instance must not be hidden"),
		Entry("an arbiter that does not vote",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				arb := arbiterInstance("arb")
				arb.RSConfig.Votes = new(int32(0))
				instanceMode(cr, dataInstance("hot", 3), arb)
			},
			"arbiterOnly instance must have votes=1"),
		Entry("an arbiter with a nonzero priority",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				arb := arbiterInstance("arb")
				arb.RSConfig.Priority = new(int32(1))
				instanceMode(cr, dataInstance("hot", 3), arb)
			},
			"arbiterOnly instance must have priority=0"),
		Entry("an arbiter with tags",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				arb := arbiterInstance("arb")
				arb.RSConfig.Tags = map[string]string{"rack": "a"}
				instanceMode(cr, dataInstance("hot", 3), arb)
			},
			"arbiterOnly instance must not have tags"),
		Entry("a non-voter with a nonzero priority",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				inst := dataInstance("ro", 1)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{
					Votes: new(int32(0)), Priority: new(int32(1))}
				instanceMode(cr, dataInstance("hot", 3), inst)
			},
			"votes=0 requires priority=0"),
		Entry("a hidden member with a nonzero priority",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				inst := dataInstance("an", 1)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{
					Hidden: new(true), Priority: new(int32(1))}
				instanceMode(cr, dataInstance("hot", 3), inst)
			},
			"hidden=true requires priority=0"),

		// --- bounds -------------------------------------------------------
		Entry("a name longer than the derived-name budget",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance(strings.Repeat("a", 55), 3))
			},
			"may not be longer than 54"),
		Entry("more instances than a replica set can hold",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				insts := make([]psmdbv1.InstanceSpec, 0, 51)
				for i := range 51 {
					insts = append(insts, dataInstance(fmt.Sprintf("g%d", i), 1))
				}
				instanceMode(cr, insts...)
			},
			"Too many: 51: must have at most 50 items"),
		Entry("a negative replica count",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", -1))
			},
			"should be greater than or equal to 0"),
		Entry("a priority above the MongoDB maximum",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				inst := dataInstance("hot", 3)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{Priority: new(int32(1001))}
				instanceMode(cr, inst)
			},
			"should be less than or equal to 1000"),
		Entry("more than one vote",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				inst := dataInstance("hot", 3)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{Votes: new(int32(2))}
				instanceMode(cr, inst)
			},
			"should be less than or equal to 1"),
		Entry("more tags than a member document may carry",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				tags := make(map[string]string, 33)
				for i := range 33 {
					tags[fmt.Sprintf("k%d", i)] = "v"
				}
				inst := dataInstance("hot", 3)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{Tags: tags}
				instanceMode(cr, inst)
			},
			"Too many: 33: must have at most 32 items"),
	)

	createRaw := func(mutate func(rs map[string]any)) error {
		counter++
		cr, err := readDefaultCR(fmt.Sprintf("cel-%d", counter), ns)
		Expect(err).NotTo(HaveOccurred())
		instanceMode(cr, dataInstance("hot", 3))

		raw, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cr)
		Expect(err).NotTo(HaveOccurred())

		u := &unstructured.Unstructured{Object: raw}
		replsets, _, err := unstructured.NestedSlice(u.Object, "spec", "replsets")
		Expect(err).NotTo(HaveOccurred())

		mutate(replsets[0].(map[string]any))
		Expect(unstructured.SetNestedSlice(u.Object, replsets, "spec", "replsets")).To(Succeed())
		u.SetGroupVersionKind(psmdbv1.SchemeGroupVersion.WithKind("PerconaServerMongoDB"))

		return k8sClient.Create(ctx, u)
	}

	It("rejects an empty instances[] list", func() {
		err := createRaw(func(rs map[string]any) {
			rs["instances"] = []any{}
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("should have at least 1 items"))
	})

	It("rejects a replica set that declares neither a size nor instances[]", func() {
		err := applyCR(func(cr *psmdbv1.PerconaServerMongoDB) {
			cr.Spec.Replsets[0].Size = nil
		})
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("a replicaset must either declare a size or set named instances"))
	})

	It("accepts an explicit size of zero, which is not the same as unset", func() {
		Expect(applyCR(func(cr *psmdbv1.PerconaServerMongoDB) {
			cr.Spec.Replsets[0].Size = new(int32(0))
		})).To(Succeed())
	})

	DescribeTable("accepts a well-formed topology",
		func(mutate func(*psmdbv1.PerconaServerMongoDB)) {
			Expect(applyCR(mutate)).To(Succeed())
		},

		Entry("a single data-bearing group",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3))
			}),
		Entry("a data group and an arbiter",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 2), arbiterInstance("arb"))
			}),
		Entry("the reserved nonVoting name, which is not the reserved-collision one",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3), dataInstance(psmdbv1.ReservedGroupNonVoting, 1))
			}),
		Entry("a name at the derived-name budget",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance(strings.Repeat("a", 54), 3))
			}),
		Entry("a group scaled to zero",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				instanceMode(cr, dataInstance("hot", 3), dataInstance("cold", 0))
			}),
		Entry("the maximum number of groups",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				insts := make([]psmdbv1.InstanceSpec, 0, 50)
				for i := range 50 {
					insts = append(insts, dataInstance(fmt.Sprintf("g%d", i), 1))
				}
				instanceMode(cr, insts...)
			}),
		Entry("member settings at their bounds",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				tags := make(map[string]string, 32)
				for i := range 32 {
					tags[fmt.Sprintf("k%d", i)] = "v"
				}
				inst := dataInstance("hot", 3)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{
					Priority: new(int32(1000)), Votes: new(int32(1)), Tags: tags}
				instanceMode(cr, inst)
			}),
		Entry("a hidden member at priority zero",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				inst := dataInstance("an", 1)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{
					Hidden: new(true), Priority: new(int32(0))}
				instanceMode(cr, dataInstance("hot", 3), inst)
			}),
		Entry("a non-voter at priority zero",
			func(cr *psmdbv1.PerconaServerMongoDB) {
				inst := dataInstance("ro", 1)
				inst.RSConfig = &psmdbv1.MemberConfigSpec{
					Votes: new(int32(0)), Priority: new(int32(0))}
				instanceMode(cr, dataInstance("hot", 3), inst)
			}),
	)
})

func cloneVolumeSpec() *psmdbv1.VolumeSpec {
	return &psmdbv1.VolumeSpec{PersistentVolumeClaim: psmdbv1.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		}}}
}
