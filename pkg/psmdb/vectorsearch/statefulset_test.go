package vectorsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func int32Ptr(i int32) *int32 { return &i }

func TestGetSearchSpec_NilClusterSpec_ReturnsDisabled(t *testing.T) {
	cr := newTestCR()
	// cr.Spec.Search is nil
	rs := newTestRS()

	got := getSearchSpec(cr, rs)
	require.NotNil(t, got)
	assert.False(t, got.Enabled)
}

func TestGetSearchSpec_NilReplset_ReturnsClusterSpec(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = &api.SearchSpec{
		Enabled: true,
		Image:   "percona/mongot:latest",
		Size:    1,
	}

	got := getSearchSpec(cr, nil)
	assert.Same(t, cr.Spec.Search, got, "must return the cluster spec when rs is nil")
}

func TestGetSearchSpec_NoReplsetOverride_ReturnsClusterSpec(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = &api.SearchSpec{
		Enabled: true,
		Image:   "percona/mongot:latest",
		Size:    1,
	}
	rs := newTestRS()
	// rs.Search is nil

	got := getSearchSpec(cr, rs)
	assert.Same(t, cr.Spec.Search, got, "must return the cluster spec when rs has no override")
}

func TestGetSearchSpec_AppliesEachOverride(t *testing.T) {
	storage := &api.VolumeSpec{
		EmptyDir: &corev1.EmptyDirVolumeSource{},
	}
	resources := corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}
	affinity := &api.PodAffinity{
		TopologyKey: ptr("kubernetes.io/hostname"),
	}
	nodeSelector := map[string]string{"disktype": "ssd"}
	tolerations := []corev1.Toleration{{Key: "search", Operator: corev1.TolerationOpExists}}
	annotations := map[string]string{"team": "search"}
	labels := map[string]string{"tier": "mongot"}
	csc := &corev1.SecurityContext{RunAsUser: int64Ptr(1001)}
	psc := &corev1.PodSecurityContext{RunAsUser: int64Ptr(1002)}

	tests := map[string]struct {
		override *api.SearchReplsetOverride
		check    func(t *testing.T, got *api.SearchSpec)
	}{
		"size override": {
			override: &api.SearchReplsetOverride{Size: int32Ptr(2)},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Equal(t, int32(2), got.Size)
			},
		},
		"storage override": {
			override: &api.SearchReplsetOverride{Storage: storage},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Same(t, storage, got.Storage)
			},
		},
		"resources override": {
			override: &api.SearchReplsetOverride{Resources: &resources},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Equal(t, resources, got.Resources)
			},
		},
		"jvm flags override": {
			override: &api.SearchReplsetOverride{JVMFlags: []string{"-Xmx4g"}},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Equal(t, []string{"-Xmx4g"}, got.JVMFlags)
			},
		},
		"affinity override": {
			override: &api.SearchReplsetOverride{Affinity: affinity},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Same(t, affinity, got.Affinity)
			},
		},
		"node selector override": {
			override: &api.SearchReplsetOverride{NodeSelector: nodeSelector},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Equal(t, nodeSelector, got.NodeSelector)
			},
		},
		"tolerations override": {
			override: &api.SearchReplsetOverride{Tolerations: tolerations},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Equal(t, tolerations, got.Tolerations)
			},
		},
		"annotations override": {
			override: &api.SearchReplsetOverride{Annotations: annotations},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Equal(t, annotations, got.Annotations)
			},
		},
		"labels override": {
			override: &api.SearchReplsetOverride{Labels: labels},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Equal(t, labels, got.Labels)
			},
		},
		"container security context override": {
			override: &api.SearchReplsetOverride{ContainerSecurityContext: csc},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Same(t, csc, got.ContainerSecurityContext)
			},
		},
		"pod security context override": {
			override: &api.SearchReplsetOverride{PodSecurityContext: psc},
			check: func(t *testing.T, got *api.SearchSpec) {
				assert.Same(t, psc, got.PodSecurityContext)
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newTestCR()
			cr.Spec.Search = &api.SearchSpec{
				Enabled: true,
				Image:   "percona/mongot:latest",
				Size:    1,
			}
			rs := newTestRS()
			rs.Search = tt.override

			got := getSearchSpec(cr, rs)
			require.NotNil(t, got)
			tt.check(t, got)

			// fields the override didn't touch must keep their cluster-wide value
			assert.True(t, got.Enabled)
			assert.Equal(t, "percona/mongot:latest", got.Image)
		})
	}
}

func TestGetSearchSpec_DoesNotMutateClusterSpec(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = &api.SearchSpec{
		Enabled:      true,
		Image:        "percona/mongot:latest",
		Size:         1,
		JVMFlags:     []string{"-Xmx1g"},
		NodeSelector: map[string]string{"disktype": "ssd"},
	}

	// Apply overrides for the first replset.
	rs1 := newTestRS()
	rs1.Name = "rs0"
	rs1.Search = &api.SearchReplsetOverride{
		Size:         int32Ptr(3),
		JVMFlags:     []string{"-Xmx4g"},
		NodeSelector: map[string]string{"disktype": "nvme"},
	}
	got1 := getSearchSpec(cr, rs1)
	assert.Equal(t, int32(3), got1.Size)
	assert.Equal(t, []string{"-Xmx4g"}, got1.JVMFlags)
	assert.Equal(t, map[string]string{"disktype": "nvme"}, got1.NodeSelector)

	// Cluster-wide spec must be untouched.
	assert.NotSame(t, cr.Spec.Search, got1, "must return a copy, not the cluster spec")
	assert.Equal(t, int32(1), cr.Spec.Search.Size)
	assert.Equal(t, []string{"-Xmx1g"}, cr.Spec.Search.JVMFlags)
	assert.Equal(t, map[string]string{"disktype": "ssd"}, cr.Spec.Search.NodeSelector)

	// A second replset without overrides must see the cluster-wide values,
	// not whatever the first replset set.
	rs2 := newTestRS()
	rs2.Name = "rs1"
	got2 := getSearchSpec(cr, rs2)
	assert.Equal(t, int32(1), got2.Size)
	assert.Equal(t, []string{"-Xmx1g"}, got2.JVMFlags)
	assert.Equal(t, map[string]string{"disktype": "ssd"}, got2.NodeSelector)
}

func TestGetSearchSpec_CombinedOverrides(t *testing.T) {
	cr := newTestCR()
	cr.Spec.Search = &api.SearchSpec{
		Enabled: true,
		Image:   "percona/mongot:latest",
		Size:    1,
	}
	rs := newTestRS()
	rs.Search = &api.SearchReplsetOverride{
		Size:         int32Ptr(3),
		NodeSelector: map[string]string{"disktype": "ssd"},
		Annotations:  map[string]string{"team": "search"},
	}

	got := getSearchSpec(cr, rs)
	require.NotNil(t, got)
	assert.Equal(t, int32(3), got.Size)
	assert.Equal(t, map[string]string{"disktype": "ssd"}, got.NodeSelector)
	assert.Equal(t, map[string]string{"team": "search"}, got.Annotations)
	// untouched
	assert.True(t, got.Enabled)
	assert.Equal(t, "percona/mongot:latest", got.Image)
}

func ptr[T any](v T) *T       { return &v }
func int64Ptr(i int64) *int64 { return &i }

func TestJVMFlags_DefaultsHalfOfMemoryRequest(t *testing.T) {
	tests := map[string]struct {
		memRequest string
		want       []string
	}{
		"2Gi request → 1024m heap": {
			memRequest: "2Gi",
			want:       []string{"-Xmx1024m", "-Xms1024m"},
		},
		"4Gi request → 2048m heap": {
			memRequest: "4Gi",
			want:       []string{"-Xmx2048m", "-Xms2048m"},
		},
		"1Gi request → 512m heap": {
			memRequest: "1Gi",
			want:       []string{"-Xmx512m", "-Xms512m"},
		},
		"512Mi request → 256m heap": {
			memRequest: "512Mi",
			want:       []string{"-Xmx256m", "-Xms256m"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec := &api.SearchSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse(tt.memRequest),
					},
				},
			}

			got := jvmFlags(spec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJVMFlags_UserHeapFlagsSuppressDefaults(t *testing.T) {
	tests := map[string]struct {
		userFlags []string
		want      []string
	}{
		"user sets -Xmx only": {
			userFlags: []string{"-Xmx4g"},
			want:      []string{"-Xmx4g"},
		},
		"user sets -Xms only": {
			userFlags: []string{"-Xms1g"},
			want:      []string{"-Xms1g"},
		},
		"user sets both -Xmx and -Xms": {
			userFlags: []string{"-Xmx4g", "-Xms4g"},
			want:      []string{"-Xmx4g", "-Xms4g"},
		},
		"user heap flag among non-heap flags": {
			userFlags: []string{"-XX:+UseG1GC", "-Xmx4g", "-XX:MaxGCPauseMillis=200"},
			want:      []string{"-XX:+UseG1GC", "-Xmx4g", "-XX:MaxGCPauseMillis=200"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			spec := &api.SearchSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
				JVMFlags: tt.userFlags,
			}

			got := jvmFlags(spec)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJVMFlags_NonHeapUserFlagsKeepDefaults(t *testing.T) {
	spec := &api.SearchSpec{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		JVMFlags: []string{"-XX:+UseG1GC", "-XX:MaxGCPauseMillis=200"},
	}

	got := jvmFlags(spec)
	assert.Equal(t,
		[]string{"-Xmx1024m", "-Xms1024m", "-XX:+UseG1GC", "-XX:MaxGCPauseMillis=200"},
		got,
		"defaults must be prepended and user flags appended in order",
	)
}

func TestJVMFlags_DoesNotMutateSpec(t *testing.T) {
	userFlags := []string{"-XX:+UseG1GC"}
	spec := &api.SearchSpec{
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("2Gi"),
			},
		},
		JVMFlags: userFlags,
	}

	_ = jvmFlags(spec)
	assert.Equal(t, []string{"-XX:+UseG1GC"}, spec.JVMFlags,
		"jvmFlags must not mutate spec.JVMFlags")
	assert.Equal(t, []string{"-XX:+UseG1GC"}, userFlags,
		"jvmFlags must not mutate the caller's slice")
}

func TestMongotContainer_JVMFlagsArg(t *testing.T) {
	cr := newTestCR()
	// TLSEnabled() walks through CompareVersion → semver parse,
	// which panics on an empty CRVersion.
	cr.Spec.CRVersion = "1.22.0"

	tests := map[string]struct {
		search      *api.SearchSpec
		wantJVMArgs []string
	}{
		"no memory request": {
			search:      &api.SearchSpec{},
			wantJVMArgs: []string{},
		},
		"memory request emits default heap flags": {
			search: &api.SearchSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			wantJVMArgs: []string{"--jvm-flags", "-Xmx1024m -Xms1024m"},
		},
		"memory limit emits default heap flags": {
			search: &api.SearchSpec{
				Resources: corev1.ResourceRequirements{
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
			},
			wantJVMArgs: []string{"--jvm-flags", "-Xmx1024m -Xms1024m"},
		},
		"memory requests are used when both request and limit are configured": {
			search: &api.SearchSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
					Limits: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("4Gi"),
					},
				},
			},
			wantJVMArgs: []string{"--jvm-flags", "-Xmx1024m -Xms1024m"},
		},
		"user heap flag suppresses defaults and is passed through": {
			search: &api.SearchSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
				JVMFlags: []string{"-Xmx4g", "-Xms4g"},
			},
			wantJVMArgs: []string{"--jvm-flags", "-Xmx4g -Xms4g"},
		},
		"non-heap user flags appended after defaults": {
			search: &api.SearchSpec{
				Resources: corev1.ResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceMemory: resource.MustParse("2Gi"),
					},
				},
				JVMFlags: []string{"-XX:+UseG1GC"},
			},
			wantJVMArgs: []string{"--jvm-flags", "-Xmx1024m -Xms1024m -XX:+UseG1GC"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := mongotContainer(cr, tt.search)

			wantArgs := append(
				[]string{
					"mongot-community/mongot",
					"--config=" + ConfigMountPath + "/" + ConfigFileName,
				},
				tt.wantJVMArgs...,
			)
			assert.Equal(t, wantArgs, c.Args)
		})
	}
}
