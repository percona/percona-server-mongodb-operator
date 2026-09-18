package perconaservermongodb

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/logcollector/logrotate"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestReconcileStatefulSet(t *testing.T) {
	ctx := context.Background()

	const (
		ns     = "reconcile-statefulset"
		crName = ns + "-cr"
	)

	defaultCR, err := readDefaultCR(crName, ns)
	if err != nil {
		t.Fatal(err)
	}

	defaultCR.Spec.Replsets[0].NonVoting.Enabled = true
	defaultCR.Spec.Replsets[0].Hidden.Enabled = true
	// The arbiter StatefulSet is only built for a group the resolver produces,
	// and resolveLegacy only produces one when the role is enabled. Enabling it
	// needs the size check relaxed: deploy/cr.yaml has size 3, and an arbiter
	// requires an even size >= 4. unsafeFlags.replsetSize is read only by
	// unsafePSA in mgo.go, never in the StatefulSet build path, so this does not
	// affect the generated objects.
	defaultCR.Spec.Replsets[0].Arbiter.Enabled = true
	// The same for the config server, which supports non-voting and hidden
	// members. Not an arbiter: CheckNSetDefaults forces
	// sharding.configsvrReplSet.arbiter.enabled to false, so no such workload
	// is ever built.
	// deploy/cr.yaml carries sizes for rs0's roles but not the config
	// server's, so set them here or the workloads generate with zero replicas.
	defaultCR.Spec.Sharding.ConfigsvrReplSet.NonVoting.Enabled = true
	defaultCR.Spec.Sharding.ConfigsvrReplSet.NonVoting.Size = 3
	defaultCR.Spec.Sharding.ConfigsvrReplSet.Hidden.Enabled = true
	defaultCR.Spec.Sharding.ConfigsvrReplSet.Hidden.Size = 2
	defaultCR.Spec.Unsafe.ReplsetSize = true
	defaultCR.Spec.LogCollector.Configuration = "config"
	if err := defaultCR.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
		t.Fatal(err)
	}

	defaultCR.Spec.Replsets[0].Env = []corev1.EnvVar{
		{Name: "TEST_ENV1", Value: "test-value1"},
		{Name: "TEST_ENV2", Value: "test-value2"},
	}
	defaultCR.Spec.Replsets[0].EnvFrom = []corev1.EnvFromSource{
		{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "test-configmap",
				},
				Optional: new(true),
			},
		},
	}
	defaultCR.Spec.Sharding.ConfigsvrReplSet.Env = []corev1.EnvVar{
		{Name: "CFG_TEST_ENV1", Value: "cfg-test-value1"},
		{Name: "CFG_TEST_ENV2", Value: "cfg-test-value2"},
	}
	defaultCR.Spec.Sharding.ConfigsvrReplSet.EnvFrom = []corev1.EnvFromSource{
		{
			ConfigMapRef: &corev1.ConfigMapEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: "test-configmap-cfg",
				},
				Optional: new(true),
			},
		},
	}

	tests := []struct {
		name           string
		cr             *api.PerconaServerMongoDB
		rsName         string
		group          string
		crUpdate       func(cr *api.PerconaServerMongoDB)
		additionalObjs []client.Object

		expectedSts *appsv1.StatefulSet
	}{
		{
			name:        "rs0-mongod",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			group:       naming.GroupMongod,
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-mongod.yaml"),
		},
		{
			name:        "rs0-arbiter",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			group:       naming.GroupArbiter,
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-arbiter.yaml"),
		},
		{
			name:        "rs0-non-voting",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			group:       naming.GroupNonVoting,
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-nv.yaml"),
		},
		{
			name:        "rs0-hidden",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			group:       naming.GroupHidden,
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-hidden.yaml"),
		},
		{
			name:        "cfg-mongod",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			group:       naming.GroupMongod,
			expectedSts: expectedSts(t, "reconcile-statefulset/cfg-mongod.yaml"),
		},
		{
			name:        "cfg-non-voting",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			group:       naming.GroupNonVoting,
			expectedSts: expectedSts(t, "reconcile-statefulset/cfg-nv.yaml"),
		},
		{
			name:        "cfg-hidden",
			cr:          defaultCR.DeepCopy(),
			rsName:      "cfg",
			group:       naming.GroupHidden,
			expectedSts: expectedSts(t, "reconcile-statefulset/cfg-hidden.yaml"),
		},
		{
			name:        "rs0-instance-hot",
			cr:          instanceModeCR(t, defaultCR),
			rsName:      "rs0",
			group:       "hot",
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-instance-hot.yaml"),
		},
		{
			name:        "rs0-instance-arbiter",
			cr:          instanceModeCR(t, defaultCR),
			rsName:      "rs0",
			group:       "arb",
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-instance-arbiter.yaml"),
		},
		{
			name:        "rs0-logrotate",
			cr:          defaultCR.DeepCopy(),
			rsName:      "rs0",
			group:       naming.GroupMongod,
			expectedSts: expectedSts(t, "reconcile-statefulset/rs0-logrotate.yaml"),
			crUpdate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.LogCollector.LogRotate = &api.LogRotateSpec{
					Configuration: "test-config",
					ExtraConfig: corev1.LocalObjectReference{
						Name: "extra-config",
					},
					Schedule: "0 0 */2 * *",
				}
			},
			additionalObjs: []client.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      logrotate.ConfigMapName(crName),
						Namespace: ns,
					},
					Data: map[string]string{
						logrotate.MongodbConfig: "custom-config",
					},
				},
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "extra-config",
						Namespace: ns,
					},
					Data: map[string]string{
						"custom.conf": "custom-config",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {

			mockObjs := []client.Object{
				tt.cr,
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      crName + "-ssl",
						Namespace: tt.cr.Namespace,
					},
					Data: map[string][]byte{
						"ca.crt":  []byte("fake-ca-cert"),
						"tls.crt": []byte("fake-tls-cert"),
						"tls.key": []byte("fake-tls-key"),
					},
				},
				&corev1.Secret{
					ObjectMeta: metav1.ObjectMeta{
						Name:      crName + "-ssl-internal",
						Namespace: tt.cr.Namespace,
					},
				},

				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      logcollector.ConfigMapName(tt.cr.Name),
						Namespace: tt.cr.Namespace,
					},
					Data: map[string]string{
						"fluentbit_custom.conf": "config",
					},
				},
			}

			mockObjs = append(mockObjs, tt.additionalObjs...)
			r := buildFakeClient(mockObjs...)

			if tt.crUpdate != nil {
				tt.crUpdate(tt.cr)
			}

			rs := tt.cr.Spec.Replset(tt.rsName)

			set, err := membergroup.Resolve(tt.cr, rs)
			if err != nil {
				t.Fatalf("resolve member groups: %v", err)
			}

			group, ok := set.GetByName(tt.group)
			if !ok {
				t.Fatalf("no member group %q in replset %s (have %v)", tt.group, rs.Name, set.GetNames())
			}

			sts, err := r.reconcileStatefulSet(ctx, tt.cr, rs, group)
			if err != nil {
				t.Fatalf("reconcileStatefulSet() error = %v", err)
			}

			// Since version v0.22.0 of the runtime controller, it does not return the GVK for a type.
			// Github issue: https://github.com/kubernetes-sigs/controller-runtime/issues/3302
			gvk, err := apiutil.GVKForObject(sts, scheme.Scheme)
			require.NoError(t, err)
			require.False(t, gvk.Empty())

			sts.Kind = gvk.Kind
			sts.APIVersion = gvk.GroupVersion().String()

			compareSts(t, sts, tt.expectedSts)
		})
	}
}

// instanceModeCR rewrites the default fixture's rs0 as instances[], keeping
// everything else identical so the generated workloads are comparable.
func instanceModeCR(t *testing.T, base *api.PerconaServerMongoDB) *api.PerconaServerMongoDB {
	t.Helper()

	cr := base.DeepCopy()
	rs := cr.Spec.Replsets[0]

	rs.Size = 0
	rs.VolumeSpec = nil
	rs.Arbiter = api.Arbiter{}
	rs.NonVoting = api.NonVotingSpec{}
	rs.Hidden = api.HiddenSpec{}
	rs.Instances = []api.InstanceSpec{
		{
			Name: "hot", Replicas: 2,
			RSConfig:   &api.MemberConfigSpec{Priority: new(int32(10)), Votes: new(int32(1))},
			VolumeSpec: instanceVolumeSpec("fast-nvme", "42Gi"),
			MultiAZ: api.MultiAZ{Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("2"),
					corev1.ResourceMemory: resource.MustParse("4G"),
				},
			}},
		},
		{
			Name: "arb", Replicas: 1,
			RSConfig: &api.MemberConfigSpec{
				ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))},
		},
	}
	cr.Spec.Unsafe.ReplsetSize = true

	require.NoError(t, cr.CheckNSetDefaults(context.Background(), version.PlatformKubernetes))

	return cr
}

func instanceVolumeSpec(storageClass, size string) *api.VolumeSpec {
	return &api.VolumeSpec{PersistentVolumeClaim: api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			StorageClassName: &storageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			},
		}}}
}

func expectedSts(t *testing.T, filename string) *appsv1.StatefulSet {
	t.Helper()

	data, err := os.ReadFile("testdata/" + filename)
	if err != nil {
		t.Fatal(err)
	}
	sts := new(appsv1.StatefulSet)

	if err := yaml.Unmarshal(data, sts); err != nil {
		t.Fatal(err)
	}

	return sts
}

func compareSts(t *testing.T, got, want *appsv1.StatefulSet) {
	t.Helper()

	if !reflect.DeepEqual(got.TypeMeta, want.TypeMeta) {
		t.Fatal(cmp.Diff(want.TypeMeta, got.TypeMeta))
	}
	compareObjectMeta := func(got, want metav1.ObjectMeta) {
		delete(got.Annotations, "percona.com/last-config-hash")
		gotBytes, err := yaml.Marshal(got)
		if err != nil {
			t.Fatalf("error marshaling got: %v", err)
		}
		wantBytes, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("error marshaling want: %v", err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Fatal(cmp.Diff(string(wantBytes), string(gotBytes)))
		}
	}
	compareObjectMeta(got.ObjectMeta, want.ObjectMeta)

	compareSpec := func(got, want appsv1.StatefulSetSpec) {
		delete(got.Template.Annotations, naming.AnnotationSSLHash)
		delete(got.Template.Annotations, naming.AnnotationSSLInternalHash)
		gotBytes, err := yaml.Marshal(got)
		if err != nil {
			t.Fatalf("error marshaling got: %v", err)
		}
		wantBytes, err := yaml.Marshal(want)
		if err != nil {
			t.Fatalf("error marshaling want: %v", err)
		}
		if string(gotBytes) != string(wantBytes) {
			t.Fatal(cmp.Diff(string(wantBytes), string(gotBytes)))
		}
	}
	compareSpec(got.Spec, want.Spec)

	if !reflect.DeepEqual(got.Status, want.Status) {
		t.Fatal(cmp.Diff(want.Status, got.Status))
	}
}

func TestInstanceModeEquivalence(t *testing.T) {
	ctx := context.Background()

	const (
		ns     = "reconcile-statefulset"
		crName = ns + "-cr"
	)

	build := func(t *testing.T, mutate func(*api.PerconaServerMongoDB)) *appsv1.StatefulSet {
		t.Helper()

		cr, err := readDefaultCR(crName, ns)
		require.NoError(t, err)
		cr.Spec.Sharding.Enabled = false
		mutate(cr)
		require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))

		r := buildFakeClient(cr,
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: crName + "-ssl", Namespace: ns,
			}},
			&corev1.Secret{ObjectMeta: metav1.ObjectMeta{
				Name: crName + "-ssl-internal", Namespace: ns,
			}},
		)

		rs := cr.Spec.Replsets[0]
		set, err := membergroup.Resolve(cr, rs)
		require.NoError(t, err)

		group, ok := set.GetByName(naming.GroupMongod)
		require.Truef(t, ok, "no mongod group (have %v)", set.GetNames())

		sts, err := r.reconcileStatefulSet(ctx, cr, rs, group)
		require.NoError(t, err)

		return sts
	}

	legacy := build(t, func(cr *api.PerconaServerMongoDB) {
		cr.Spec.Replsets[0].Size = 3
	})

	rewritten := build(t, func(cr *api.PerconaServerMongoDB) {
		rs := cr.Spec.Replsets[0]
		// Everything the legacy replica set declared, moved onto the group.
		// volumeSpec has to come along: in instance mode the replica set owns
		// no storage.
		vol := rs.VolumeSpec
		rs.Size = 0
		rs.VolumeSpec = nil
		rs.Instances = []api.InstanceSpec{
			{Name: naming.GroupMongod, Replicas: 3, VolumeSpec: vol},
		}
	})

	assert.Equal(t, legacy.Name, rewritten.Name, "the workload keeps its name")
	assert.Equal(t, legacy.Labels, rewritten.Labels)

	// The SSL hash annotations are computed from secrets, not from the
	// topology, and the config hash covers the same ConfigMap either way.
	stripVolatile := func(sts *appsv1.StatefulSet) appsv1.StatefulSetSpec {
		spec := *sts.Spec.DeepCopy()
		delete(spec.Template.Annotations, naming.AnnotationSSLHash)
		delete(spec.Template.Annotations, naming.AnnotationSSLInternalHash)
		return spec
	}

	if diff := cmp.Diff(stripVolatile(legacy), stripVolatile(rewritten)); diff != "" {
		t.Fatalf("a legacy replica set and its instances[] rewrite must build the same workload:\n%s", diff)
	}
}
func TestStatefulSetRejectsADataBearingGroupWithoutStorage(t *testing.T) {
	ctx := context.Background()

	const (
		ns     = "reconcile-statefulset"
		crName = ns + "-cr"
	)

	cr, err := readDefaultCR(crName, ns)
	require.NoError(t, err)
	cr.Spec.Sharding.Enabled = false
	require.NoError(t, cr.CheckNSetDefaults(ctx, version.PlatformKubernetes))

	rs := cr.Spec.Replsets[0]
	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	group, ok := set.GetByName(naming.GroupMongod)
	require.True(t, ok)
	require.True(t, group.DataBearing)
	group.VolumeSpec = nil

	r := buildFakeClient(cr)

	_, err = r.reconcileStatefulSet(ctx, cr, rs, group)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "has no resolved volumeSpec")
}
