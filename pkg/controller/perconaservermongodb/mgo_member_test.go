package perconaservermongodb

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func legacyCR(t *testing.T, name, ns string, mutate ...func(*api.PerconaServerMongoDB)) *api.PerconaServerMongoDB {
	t.Helper()

	cr, err := readDefaultCR(name, ns)
	require.NoError(t, err)

	cr.Spec.Sharding.Enabled = false
	for _, m := range mutate {
		m(cr)
	}

	require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

	return cr
}

func nodeFor(pod *corev1.Pod, region, zone string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: pod.Spec.NodeName,
			Labels: map[string]string{
				corev1.LabelTopologyRegion: region,
				corev1.LabelTopologyZone:   zone,
			},
		},
	}
}

// Test the member document a legacy CR produces
func TestGetConfigMemberForPodImplicit(t *testing.T) {
	const (
		podName = "member-cr-rs0-0"
		host    = "member-cr-rs0-0.member-cr-rs0.member.svc.cluster.local:27017"
	)

	baseTags := func(extra ...map[string]string) mongo.ReplsetTags {
		tags := mongo.ReplsetTags{
			"nodeName":    "node-" + podName,
			"podName":     podName,
			"serviceName": "member-cr",
		}
		for _, e := range extra {
			maps.Copy(tags, e)
		}
		return tags
	}

	for _, tt := range []struct {
		name    string
		mutate  func(*api.PerconaServerMongoDB)
		group   string
		withPod func(*corev1.Pod) // per-case pod tweaks
		objects func(*corev1.Pod) []client.Object
		want    mongo.ConfigMember
	}{
		{
			name:  "mongod",
			group: "mongod",
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true,
				Priority: 2, Votes: 1, Tags: baseTags(),
			},
		},
		{
			name: "config server mongod is an ordinary member",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ClusterRole = api.ClusterRoleConfigSvr
			},
			group: "mongod",
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true,
				Priority: 2, Votes: 1, Tags: baseTags(),
			},
		},
		{
			name: "arbiter carries no tags",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Size = new(int32(2))
				cr.Spec.Replsets[0].Arbiter = api.Arbiter{Enabled: true, Size: 1}
				cr.Spec.Unsafe.ReplsetSize = true
			},
			group: "arbiter",
			want: mongo.ConfigMember{
				Host:         "member-cr-rs0-arbiter-0.member-cr-rs0.member.svc.cluster.local:27017",
				BuildIndexes: true, ArbiterOnly: true, Priority: 0, Votes: 1,
			},
		},
		{
			name: "nonvoting is tagged and silent",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].NonVoting = api.NonVotingSpec{Enabled: true, Size: 1}
			},
			group: "nonVoting",
			want: mongo.ConfigMember{
				Host:         "member-cr-rs0-nv-0.member-cr-rs0.member.svc.cluster.local:27017",
				BuildIndexes: true, Priority: 0, Votes: 0,
				Tags: mongo.ReplsetTags{
					"nonVoting":   "true",
					"nodeName":    "node-member-cr-rs0-nv-0",
					"podName":     "member-cr-rs0-nv-0",
					"serviceName": "member-cr",
				},
			},
		},
		{
			name: "hidden votes but cannot be elected",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Hidden = api.HiddenSpec{Enabled: true, Size: 1}
			},
			group: "hidden",
			want: mongo.ConfigMember{
				Host:         "member-cr-rs0-hidden-0.member-cr-rs0.member.svc.cluster.local:27017",
				BuildIndexes: true, Hidden: true, Priority: 0, Votes: 1,
				Tags: mongo.ReplsetTags{
					"hidden":      "true",
					"nodeName":    "node-member-cr-rs0-hidden-0",
					"podName":     "member-cr-rs0-hidden-0",
					"serviceName": "member-cr",
				},
			},
		},
		{
			// A pod on a node the operator cannot read must still produce a
			// member document, just without the topology tags.
			name:  "a node that cannot be read is not an error",
			group: "mongod",
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true, Priority: 2, Votes: 1, Tags: baseTags(),
			},
		},
		{
			name: "split horizons reach the member document",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].Horizons = api.HorizonsSpec{
					podName: {"external": "rs0-0.example.net"},
				}
			},
			group: "mongod",
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true, Priority: 2, Votes: 1, Tags: baseTags(),
				Horizons: map[string]string{"external": "rs0-0.example.net:27017"},
			},
		},
		{
			name:  "node labels become region and zone tags",
			group: "mongod",
			objects: func(pod *corev1.Pod) []client.Object {
				return []client.Object{nodeFor(pod, "eu-west-1", "eu-west-1a")}
			},
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true, Priority: 2, Votes: 1,
				Tags: baseTags(map[string]string{"region": "eu-west-1", "zone": "eu-west-1a"}),
			},
		},
		{
			name: "priority override",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					podName: {Priority: new(7)},
				}
			},
			group: "mongod",
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true, Priority: 7, Votes: 1, Tags: baseTags(),
			},
		},
		{
			name: "a tag selector match sets priority absolutely",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					podName: {Tags: map[string]string{"rack": "a"}},
				}
				cr.Spec.Replsets[0].PrimaryPreferTagSelector = api.PrimaryPreferTagSelectorSpec{"rack": "a"}
			},
			group: "mongod",
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true, Priority: 3, Votes: 1,
				Tags: baseTags(map[string]string{"rack": "a"}),
			},
		},
		{
			// TODO: not sure this is expected behaviour. Why does the tag selector match overwrite an explicit priority?
			// pinned it here to come back to in the future.
			name: "a tag selector match overwrites an explicit override",
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					podName: {Priority: new(7), Tags: map[string]string{"rack": "a"}},
				}
				cr.Spec.Replsets[0].PrimaryPreferTagSelector = api.PrimaryPreferTagSelectorSpec{"rack": "a"}
			},
			group: "mongod",
			want: mongo.ConfigMember{
				Host: host, BuildIndexes: true, Priority: 3, Votes: 1,
				Tags: baseTags(map[string]string{"rack": "a"}),
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cr := legacyCR(t, "member-cr", "member", func(c *api.PerconaServerMongoDB) {
				if tt.mutate != nil {
					tt.mutate(c)
				}
			})
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)
			require.Equal(t, membergroup.PolicyImplicit, set.GetPolicy(),
				"these rows exercise the legacy vote engine")

			group := resolveGroup(t, cr, rs, tt.group)
			pod := groupPod(cr, rs, group, 0)
			pod.Spec.NodeName = "node-" + pod.Name

			objs := []client.Object{cr, pod}
			if tt.objects != nil {
				objs = append(objs, tt.objects(pod)...)
			}
			r := buildFakeClient(objs...)

			got, err := r.getConfigMemberForPod(ctx, cr, rs, set, 0, pod)
			require.NoError(t, err)

			assert.Equal(t, tt.want, got)
		})
	}
}

// Test the member document a instances[] CR produces
func TestGetConfigMemberForPodExplicit(t *testing.T) {
	dataVol := func() *api.VolumeSpec {
		return &api.VolumeSpec{PersistentVolumeClaim: api.PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			}}}
	}
	group := func(name string, replicas int32, cfg *api.MemberConfigSpec) api.InstanceSpec {
		i := api.InstanceSpec{Name: name, Replicas: replicas, RSConfig: cfg}
		if cfg == nil || cfg.ArbiterOnly == nil || !*cfg.ArbiterOnly {
			i.VolumeSpec = dataVol()
		}
		return i
	}
	// every topology keeps three data-bearing voters so the safety checks pass
	base := func(extra ...api.InstanceSpec) []api.InstanceSpec {
		return append([]api.InstanceSpec{
			group("data", 3, &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(2))}),
		}, extra...)
	}

	identity := func(pod string) mongo.ReplsetTags {
		return mongo.ReplsetTags{
			"nodeName": "node-" + pod, "podName": pod, "serviceName": "inst-cr",
		}
	}
	host := func(pod string) string {
		return pod + ".inst-cr-rs0.inst.svc.cluster.local:27017"
	}

	for _, tt := range []struct {
		name      string
		instances []api.InstanceSpec
		mutate    func(*api.PerconaServerMongoDB)
		group     string
		want      mongo.ConfigMember
		wantErr   string
	}{
		{
			name: "declared priority and votes reach the member",
			instances: base(group("hot", 1, &api.MemberConfigSpec{
				Priority: new(int32(10)), Votes: new(int32(1))})),
			group: "hot",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-hot-0"), BuildIndexes: true,
				Priority: 10, Votes: 1, Tags: identity("inst-cr-rs0-hot-0"),
			},
		},
		{
			name: "group tags merge with the identity tags",
			instances: base(group("hot", 1, &api.MemberConfigSpec{
				Priority: new(int32(2)), Votes: new(int32(1)),
				Tags: map[string]string{"workload": "hot"}})),
			group: "hot",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-hot-0"), BuildIndexes: true,
				Priority: 2, Votes: 1,
				Tags: mongo.ReplsetTags{
					"workload": "hot",
					"nodeName": "node-inst-cr-rs0-hot-0", "podName": "inst-cr-rs0-hot-0",
					"serviceName": "inst-cr",
				},
			},
		},
		{
			// MapMerge order is group, overrides, identity, so neither a group
			// tag nor an override can shadow the member's own identity. Both
			// collide here, which is what pins the order rather than just the
			// winner.
			name: "identity tags win over colliding group and override tags",
			instances: base(group("hot", 1, &api.MemberConfigSpec{
				Priority: new(int32(2)), Votes: new(int32(1)),
				Tags: map[string]string{"podName": "from-group", "serviceName": "from-group"}})),
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					"inst-cr-rs0-hot-0": {Tags: map[string]string{
						"podName": "from-override",
						"rack":    "a",
					}},
				}
			},
			group: "hot",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-hot-0"), BuildIndexes: true,
				Priority: 2, Votes: 1,
				Tags: mongo.ReplsetTags{
					"rack":     "a",
					"nodeName": "node-inst-cr-rs0-hot-0", "podName": "inst-cr-rs0-hot-0",
					"serviceName": "inst-cr",
				},
			},
		},
		{
			name: "an arbiter gets no tags even when it declares some",
			instances: base(group("arb", 1, &api.MemberConfigSpec{
				ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0)),
				Tags: map[string]string{"workload": "ignored"}})),
			group: "arb",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-arb-0"), BuildIndexes: true,
				ArbiterOnly: true, Votes: 1, Priority: 0,
			},
		},
		{
			name: "a priority override replaces the declared priority",
			instances: base(group("hot", 1, &api.MemberConfigSpec{
				Priority: new(int32(10)), Votes: new(int32(1))})),
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					"inst-cr-rs0-hot-0": {Priority: new(3)},
				}
			},
			group: "hot",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-hot-0"), BuildIndexes: true,
				Priority: 3, Votes: 1, Tags: identity("inst-cr-rs0-hot-0"),
			},
		},
		{
			name: "a nonzero priority override is rejected on a hidden member",
			instances: base(group("an", 1, &api.MemberConfigSpec{
				Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0))})),
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					"inst-cr-rs0-an-0": {Priority: new(5)},
				}
			},
			group:   "an",
			wantErr: `instance "an" requires priority 0`,
		},
		{
			name: "a nonzero priority override is rejected on a non-voter",
			instances: base(group("ro", 1, &api.MemberConfigSpec{
				Votes: new(int32(0)), Priority: new(int32(0))})),
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					"inst-cr-rs0-ro-0": {Priority: new(5)},
				}
			},
			group:   "ro",
			wantErr: `instance "ro" requires priority 0`,
		},
		{
			name: "a zero priority override is allowed on a hidden member",
			instances: base(group("an", 1, &api.MemberConfigSpec{
				Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0))})),
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].ReplsetOverrides = api.ReplsetOverrides{
					"inst-cr-rs0-an-0": {Priority: new(0)},
				}
			},
			group: "an",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-an-0"), BuildIndexes: true,
				Hidden: true, Votes: 1, Priority: 0, Tags: identity("inst-cr-rs0-an-0"),
			},
		},
		{
			name: "a tag selector match increments the declared priority",
			instances: base(group("hot", 1, &api.MemberConfigSpec{
				Priority: new(int32(10)), Votes: new(int32(1)),
				Tags: map[string]string{"rack": "a"}})),
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].PrimaryPreferTagSelector = api.PrimaryPreferTagSelectorSpec{"rack": "a"}
			},
			group: "hot",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-hot-0"), BuildIndexes: true,
				Priority: 11, Votes: 1,
				Tags: mongo.ReplsetTags{
					"rack":     "a",
					"nodeName": "node-inst-cr-rs0-hot-0", "podName": "inst-cr-rs0-hot-0",
					"serviceName": "inst-cr",
				},
			},
		},
		{
			name: "a tag selector match does not lift a priority-zero member",
			instances: base(group("an", 1, &api.MemberConfigSpec{
				Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0)),
				Tags: map[string]string{"rack": "a"}})),
			mutate: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].PrimaryPreferTagSelector = api.PrimaryPreferTagSelectorSpec{"rack": "a"}
			},
			group: "an",
			want: mongo.ConfigMember{
				Host: host("inst-cr-rs0-an-0"), BuildIndexes: true,
				Hidden: true, Votes: 1, Priority: 0,
				Tags: mongo.ReplsetTags{
					"rack":     "a",
					"nodeName": "node-inst-cr-rs0-an-0", "podName": "inst-cr-rs0-an-0",
					"serviceName": "inst-cr",
				},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			// These topologies are shaped to isolate one member setting each,
			// not to be production-safe voter counts.
			cr := instanceCR(t, "inst-cr", "inst", tt.instances, unsafeSize,
				func(c *api.PerconaServerMongoDB) {
					if tt.mutate != nil {
						tt.mutate(c)
					}
				})
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)
			require.Equal(t, membergroup.PolicyExplicit, set.GetPolicy())

			g := resolveGroup(t, cr, rs, tt.group)
			pod := groupPod(cr, rs, g, 0)
			pod.Spec.NodeName = "node-" + pod.Name

			r := buildFakeClient(cr, pod)

			got, err := r.getConfigMemberForPod(ctx, cr, rs, set, 0, pod)
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestGetConfigMemberForPodUndeclaredGroup covers a pod left behind by a group
// that has been deleted from instances[]: it belongs to no group in the
// resolved set, and there is no main group to fall back on.
func TestGetConfigMemberForPodUndeclaredGroup(t *testing.T) {
	ctx := t.Context()

	instances := []api.InstanceSpec{
		{Name: "data", Replicas: 3, VolumeSpec: &api.VolumeSpec{PersistentVolumeClaim: api.PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
				},
			}}}},
	}
	cr := instanceCR(t, "inst-cr", "inst", instances, unsafeSize)
	rs := cr.Spec.Replsets[0]

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	pod := groupPod(cr, rs, resolveGroup(t, cr, rs, "data"), 0)
	pod.Labels[naming.LabelKubernetesComponent] = "gone"

	r := buildFakeClient(cr, pod)

	_, err = r.getConfigMemberForPod(ctx, cr, rs, set, 0, pod)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "belongs to no declared member group")
}
