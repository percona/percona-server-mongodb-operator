package vectorsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

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
	assert.NotSame(t, cr.Spec.Search, got, "must return a copy, not the cluster spec itself")
	assert.Equal(t, cr.Spec.Search, got, "the copy must equal the cluster spec when rs is nil")
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
	assert.NotSame(t, cr.Spec.Search, got, "must return a copy, not the cluster spec itself")
	assert.Equal(t, cr.Spec.Search, got, "the copy must equal the cluster spec when rs has no override")
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
		TopologyKey: new("kubernetes.io/hostname"),
	}
	nodeSelector := map[string]string{"disktype": "ssd"}
	tolerations := []corev1.Toleration{{Key: "search", Operator: corev1.TolerationOpExists}}
	annotations := map[string]string{"team": "search"}
	labels := map[string]string{"tier": "mongot"}
	csc := &corev1.SecurityContext{RunAsUser: new(int64(1001))}
	psc := &corev1.PodSecurityContext{RunAsUser: new(int64(1002))}

	tests := map[string]struct {
		override *api.SearchReplsetOverride
		check    func(t *testing.T, got *api.SearchSpec)
	}{
		"size override": {
			override: &api.SearchReplsetOverride{Size: new(int32(2))},
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
		Size:         new(int32(3)),
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
		Size:         new(int32(3)),
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
	cr.Spec.CRVersion = version.Version()

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

func searchCR(search *api.SearchSpec) *api.PerconaServerMongoDB {
	cr := newTestCR()
	cr.Spec.CRVersion = version.Version()
	cr.Spec.Secrets = &api.SecretsSpec{}
	cr.Spec.Search = search
	return cr
}

func volumeByName(volumes []corev1.Volume, name string) *corev1.Volume {
	for i := range volumes {
		if volumes[i].Name == name {
			return &volumes[i]
		}
	}
	return nil
}

func mountByName(mounts []corev1.VolumeMount, name string) *corev1.VolumeMount {
	for i := range mounts {
		if mounts[i].Name == name {
			return &mounts[i]
		}
	}
	return nil
}

func TestStatefulSet_Metadata(t *testing.T) {
	cr := searchCR(&api.SearchSpec{
		Enabled: true,
		Image:   "percona/mongot:latest",
		Size:    3,
	})
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)

	assert.Equal(t, "apps/v1", sts.APIVersion)
	assert.Equal(t, "StatefulSet", sts.Kind)
	assert.Equal(t, naming.SearchStatefulSetName(cr, rs), sts.Name)
	assert.Equal(t, "psmdb-rs0-search", sts.Name)
	assert.Equal(t, "default", sts.Namespace)
	assert.Equal(t, naming.SearchLabels(cr, rs), sts.Labels)

	assert.Equal(t, naming.SearchServiceName(cr, rs), sts.Spec.ServiceName)
	require.NotNil(t, sts.Spec.Replicas)
	assert.Equal(t, int32(3), *sts.Spec.Replicas)

	require.NotNil(t, sts.Spec.Selector)
	assert.Equal(t, naming.SearchLabels(cr, rs), sts.Spec.Selector.MatchLabels)

	assert.Equal(t, appsv1.RollingUpdateStatefulSetStrategyType, sts.Spec.UpdateStrategy.Type)
}

func TestStatefulSet_ReplicasFollowSpecSize(t *testing.T) {
	cr := searchCR(&api.SearchSpec{Enabled: true, Size: 1})
	rs := newTestRS()
	rs.Search = &api.SearchReplsetOverride{Size: new(int32(5))}

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)

	require.NotNil(t, sts.Spec.Replicas)
	assert.Equal(t, int32(5), *sts.Spec.Replicas,
		"replicas must follow the effective (replset-overridden) spec size")
}

func TestStatefulSet_PodTemplate(t *testing.T) {
	cr := searchCR(&api.SearchSpec{
		Enabled:            true,
		Image:              "percona/mongot:latest",
		Size:               1,
		NodeSelector:       map[string]string{"disktype": "ssd"},
		Tolerations:        []corev1.Toleration{{Key: "search", Operator: corev1.TolerationOpExists}},
		PodSecurityContext: &corev1.PodSecurityContext{RunAsUser: new(int64(1002))},
	})
	cr.Spec.SchedulerName = "custom-scheduler"
	cr.Spec.ImagePullSecrets = []corev1.LocalObjectReference{{Name: "pull-secret"}}
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)
	podSpec := sts.Spec.Template.Spec

	assert.Equal(t, map[string]string{"disktype": "ssd"}, podSpec.NodeSelector)
	assert.Equal(t, []corev1.Toleration{{Key: "search", Operator: corev1.TolerationOpExists}}, podSpec.Tolerations)
	assert.Equal(t, "custom-scheduler", podSpec.SchedulerName)
	assert.Equal(t, []corev1.LocalObjectReference{{Name: "pull-secret"}}, podSpec.ImagePullSecrets)
	assert.Equal(t, &corev1.PodSecurityContext{RunAsUser: new(int64(1002))}, podSpec.SecurityContext)

	require.Len(t, podSpec.InitContainers, 1)
	require.Len(t, podSpec.Containers, 1)
	assert.Equal(t, naming.ContainerMongot, podSpec.Containers[0].Name)
}

func TestStatefulSet_PodLabelsMergeSpecLabels(t *testing.T) {
	cr := searchCR(&api.SearchSpec{
		Enabled: true,
		Size:    1,
		Labels:  map[string]string{"tier": "mongot"},
	})
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)
	podLabels := sts.Spec.Template.ObjectMeta.Labels

	// object labels must be present
	for k, v := range naming.SearchLabels(cr, rs) {
		assert.Equal(t, v, podLabels[k], "object label %q must be on the pod", k)
	}
	// custom spec label must be merged in
	assert.Equal(t, "mongot", podLabels["tier"])
}

func TestStatefulSet_ObjectLabelsWinOverSpecLabels(t *testing.T) {
	objectLabels := naming.SearchLabels(newTestCR(), newTestRS())
	require.Contains(t, objectLabels, naming.LabelKubernetesComponent)

	cr := searchCR(&api.SearchSpec{
		Enabled: true,
		Size:    1,
		// try to clobber an operator-managed label
		Labels: map[string]string{naming.LabelKubernetesComponent: "hijacked"},
	})
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)
	podLabels := sts.Spec.Template.ObjectMeta.Labels

	assert.Equal(t, naming.ComponentSearch, podLabels[naming.LabelKubernetesComponent],
		"operator-managed labels must not be overridden by user-provided spec labels")
}

func TestStatefulSet_ConfigHashAnnotation(t *testing.T) {
	cr := searchCR(&api.SearchSpec{Enabled: true, Size: 1})
	rs := newTestRS()

	t.Run("set when configHash provided", func(t *testing.T) {
		sts := StatefulSet(cr, rs, "percona/init:latest", "abc123", nil)
		assert.Equal(t, "abc123", sts.Spec.Template.ObjectMeta.Annotations[naming.AnnotationConfigHash])
	})

	t.Run("absent when configHash empty", func(t *testing.T) {
		sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)
		_, ok := sts.Spec.Template.ObjectMeta.Annotations[naming.AnnotationConfigHash]
		assert.False(t, ok, "configuration-hash annotation must be absent when configHash is empty")
	})
}

func TestStatefulSet_SSLHashAnnotations(t *testing.T) {
	cr := searchCR(&api.SearchSpec{Enabled: true, Size: 1})
	rs := newTestRS()

	t.Run("set when sslHashes provided", func(t *testing.T) {
		sslHashes := map[string]string{
			naming.AnnotationSSLHash:         "ssl-hash-value",
			naming.AnnotationSSLInternalHash: "ssl-internal-hash-value",
		}

		sts := StatefulSet(cr, rs, "percona/init:latest", "", sslHashes)
		annotations := sts.Spec.Template.ObjectMeta.Annotations

		assert.Equal(t, "ssl-hash-value", annotations[naming.AnnotationSSLHash])
		assert.Equal(t, "ssl-internal-hash-value", annotations[naming.AnnotationSSLInternalHash])
	})

	t.Run("absent when sslHashes nil", func(t *testing.T) {
		sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)
		annotations := sts.Spec.Template.ObjectMeta.Annotations

		_, ok := annotations[naming.AnnotationSSLHash]
		assert.False(t, ok, "ssl-hash annotation must be absent when sslHashes is nil")
		_, ok = annotations[naming.AnnotationSSLInternalHash]
		assert.False(t, ok, "ssl-internal-hash annotation must be absent when sslHashes is nil")
	})

	t.Run("absent when key missing from sslHashes", func(t *testing.T) {
		sslHashes := map[string]string{
			naming.AnnotationSSLHash: "ssl-hash-value",
		}

		sts := StatefulSet(cr, rs, "percona/init:latest", "", sslHashes)
		annotations := sts.Spec.Template.ObjectMeta.Annotations

		assert.Equal(t, "ssl-hash-value", annotations[naming.AnnotationSSLHash])
		_, ok := annotations[naming.AnnotationSSLInternalHash]
		assert.False(t, ok, "ssl-internal-hash annotation must be absent when missing from sslHashes")
	})

	t.Run("absent when sslHash value empty", func(t *testing.T) {
		sslHashes := map[string]string{
			naming.AnnotationSSLHash:         "",
			naming.AnnotationSSLInternalHash: "ssl-internal-hash-value",
		}

		sts := StatefulSet(cr, rs, "percona/init:latest", "", sslHashes)
		annotations := sts.Spec.Template.ObjectMeta.Annotations

		_, ok := annotations[naming.AnnotationSSLHash]
		assert.False(t, ok, "ssl-hash annotation must be absent when its value is empty")
		assert.Equal(t, "ssl-internal-hash-value", annotations[naming.AnnotationSSLInternalHash])
	})
}

func TestStatefulSet_Volumes_PVCTemplate(t *testing.T) {
	cr := searchCR(&api.SearchSpec{
		Enabled: true,
		Size:    1,
		Storage: &api.VolumeSpec{
			PersistentVolumeClaim: api.PVCSpec{
				PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: resource.MustParse("10Gi"),
						},
					},
				},
			},
		},
	})
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)

	require.Len(t, sts.Spec.VolumeClaimTemplates, 1)
	pvc := sts.Spec.VolumeClaimTemplates[0]
	assert.Equal(t, DataVolumeName, pvc.Name)
	assert.Equal(t, "default", pvc.Namespace)
	assert.Equal(t, resource.MustParse("10Gi"), pvc.Spec.Resources.Requests[corev1.ResourceStorage])

	assert.Nil(t, volumeByName(sts.Spec.Template.Spec.Volumes, DataVolumeName),
		"data volume must be a PVC template, not an inline pod volume")
}

func TestStatefulSet_TLSEnabled_AddsSSLVolumeAndMount(t *testing.T) {
	cr := searchCR(&api.SearchSpec{Enabled: true, Size: 1})
	require.True(t, cr.TLSEnabled())
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)

	ssl := volumeByName(sts.Spec.Template.Spec.Volumes, "ssl")
	require.NotNil(t, ssl, "ssl volume must be present when TLS is enabled")
	require.NotNil(t, ssl.Secret)
	assert.Equal(t, api.SSLInternalSecretName(cr), ssl.Secret.SecretName)

	mount := mountByName(sts.Spec.Template.Spec.Containers[0].VolumeMounts, "ssl")
	require.NotNil(t, mount, "ssl mount must be present when TLS is enabled")
	assert.True(t, mount.ReadOnly)
}

func TestStatefulSet_TLSDisabled_NoSSLVolumeOrMount(t *testing.T) {
	cr := searchCR(&api.SearchSpec{Enabled: true, Size: 1})
	cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeDisabled}
	require.False(t, cr.TLSEnabled())
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)

	assert.Nil(t, volumeByName(sts.Spec.Template.Spec.Volumes, "ssl"),
		"ssl volume must be absent when TLS is disabled")
	assert.Nil(t, mountByName(sts.Spec.Template.Spec.Containers[0].VolumeMounts, "ssl"),
		"ssl mount must be absent when TLS is disabled")
}

func TestStatefulSet_Affinity_TopologyKey(t *testing.T) {
	cr := searchCR(&api.SearchSpec{
		Enabled:  true,
		Size:     1,
		Affinity: &api.PodAffinity{TopologyKey: new("kubernetes.io/hostname")},
	})
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)
	aff := sts.Spec.Template.Spec.Affinity

	require.NotNil(t, aff)
	require.NotNil(t, aff.PodAntiAffinity)
	terms := aff.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
	require.Len(t, terms, 1)
	assert.Equal(t, "kubernetes.io/hostname", terms[0].TopologyKey)
	assert.Equal(t, sts.Spec.Template.ObjectMeta.Labels, terms[0].LabelSelector.MatchLabels)
}

func TestStatefulSet_Affinity_Off(t *testing.T) {
	cr := searchCR(&api.SearchSpec{
		Enabled:  true,
		Size:     1,
		Affinity: &api.PodAffinity{TopologyKey: new(api.AffinityOff)},
	})
	rs := newTestRS()

	sts := StatefulSet(cr, rs, "percona/init:latest", "", nil)
	assert.Nil(t, sts.Spec.Template.Spec.Affinity,
		"affinity must be nil when the topology key is the off sentinel")
}
