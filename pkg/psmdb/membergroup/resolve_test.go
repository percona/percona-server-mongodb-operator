package membergroup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

var emptyTags = map[string]string{}

// wantGroup is the resolved shape of one group. Every field a call site branches
// on is listed, so a case that changes one thing also proves it changed nothing
// else.
type wantGroup struct {
	name        string
	component   string
	stsName     string
	container   string
	configName  string
	replicas    int32
	member      MemberConfig
	dataBearing bool
	primary     bool
	source      SourceRef
	hasVolume   bool
}

func assertGroups(t *testing.T, want []wantGroup, got []Group) {
	t.Helper()

	names := make([]string, 0, len(got))
	for i := range got {
		names = append(names, got[i].Name)
	}
	wantNames := make([]string, 0, len(want))
	for i := range want {
		wantNames = append(wantNames, want[i].name)
	}
	require.Equal(t, wantNames, names, "resolved group names, in order")

	for i := range want {
		w, g := want[i], got[i]
		t.Run(w.name, func(t *testing.T) {
			assert.Equal(t, w.component, g.Component, "component")
			assert.Equal(t, w.stsName, g.STSName, "statefulset name")
			assert.Equal(t, w.container, g.ContainerName, "container name")
			assert.Equal(t, w.configName, g.ConfigName, "config map name")
			assert.Equal(t, w.replicas, g.Replicas, "replicas")
			assert.Equal(t, w.member, g.Member, "member config")
			assert.Equal(t, w.dataBearing, g.DataBearing, "dataBearing")
			assert.Equal(t, w.primary, g.PrimaryEligible, "primaryEligible")
			assert.Equal(t, w.source, g.Source, "source ref")
			assert.Equal(t, w.hasVolume, g.VolumeSpec != nil, "has volume spec")

			// The selector labels of an existing StatefulSet are immutable, so
			// a group's labels must be exactly what the naming helpers derive.
			assert.Equal(t, g.Component, g.Labels[naming.LabelKubernetesComponent], "component label")
			assert.Equal(t, "cluster1", g.Labels[naming.LabelKubernetesInstance], "instance label")
		})
	}
}

// TestResolveLegacy pins the pre-instances[] topologies. Every group these
// produce already exists in running clusters, so the resolved identity is a
// compatibility contract: if any of it changes, an upgrade renames or orphans a
// live workload.
func TestResolveLegacy(t *testing.T) {
	nvVol := vol("5Gi")

	tests := map[string]struct {
		rs   *api.ReplsetSpec
		want []wantGroup
	}{
		"base only": {
			rs: legacyRS("rs0", 3),
			want: []wantGroup{{
				name: naming.GroupMongod, component: naming.ComponentMongod,
				stsName: "cluster1-rs0", container: naming.ContainerMongod,
				configName: "cluster1-rs0-mongod", replicas: 3,
				member:      MemberConfig{Votes: 1, Priority: 2},
				dataBearing: true, primary: true, hasVolume: true,
				source: SourceRef{ReplsetName: "rs0"},
			}},
		},
		"with arbiter": {
			rs: func() *api.ReplsetSpec {
				rs := legacyRS("rs0", 4)
				rs.Arbiter = api.Arbiter{Enabled: true, Size: 1}
				return rs
			}(),
			want: []wantGroup{
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 4,
					member:      MemberConfig{Votes: 1, Priority: 2},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0"},
				},
				{
					name: naming.GroupArbiter, component: naming.ComponentArbiter,
					stsName: "cluster1-rs0-arbiter", container: naming.ContainerArbiter,
					// An arbiter has no configuration of its own: it mounts and
					// hashes the base mongod config map.
					configName: "cluster1-rs0-mongod", replicas: 1,
					member:      MemberConfig{ArbiterOnly: true, Votes: 1, Priority: 0},
					dataBearing: false, primary: false,
					// No data volume is resolved; StatefulSpec injects an emptyDir.
					hasVolume: false,
					source:    SourceRef{ReplsetName: "rs0", LegacyRole: "arbiter"},
				},
			},
		},
		"with nonvoting": {
			rs: func() *api.ReplsetSpec {
				rs := legacyRS("rs0", 3)
				rs.NonVoting = api.NonVotingSpec{Enabled: true, Size: 2, VolumeSpec: nvVol}
				return rs
			}(),
			want: []wantGroup{
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 3,
					member:      MemberConfig{Votes: 1, Priority: 2},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0"},
				},
				{
					name: naming.GroupNonVoting, component: naming.ComponentNonVoting,
					// "nv", not "nonVoting": the StatefulSet suffix and the
					// component label deliberately differ.
					stsName: "cluster1-rs0-nv", container: naming.ContainerNonVoting,
					configName: "cluster1-rs0-nv", replicas: 2,
					// SetVotes reads this role tag, so it must survive.
					member: MemberConfig{
						Votes: 0, Priority: 0,
						Tags: map[string]string{naming.ComponentNonVoting: "true"},
					},
					dataBearing: true, primary: false, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", LegacyRole: "nonvoting"},
				},
			},
		},
		"with hidden": {
			rs: func() *api.ReplsetSpec {
				rs := legacyRS("rs0", 3)
				rs.Hidden = api.HiddenSpec{Enabled: true, Size: 1, VolumeSpec: vol("1Gi")}
				return rs
			}(),
			want: []wantGroup{
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 3,
					member:      MemberConfig{Votes: 1, Priority: 2},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0"},
				},
				{
					name: naming.GroupHidden, component: naming.ComponentHidden,
					stsName: "cluster1-rs0-hidden", container: naming.ContainerHidden,
					configName: "cluster1-rs0-hidden", replicas: 1,
					// A hidden member votes. It just cannot be elected.
					member: MemberConfig{
						Hidden: true, Votes: 1, Priority: 0,
						Tags: map[string]string{naming.ComponentHidden: "true"},
					},
					dataBearing: true, primary: false, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", LegacyRole: "hidden"},
				},
			},
		},
		"disabled roles produce no group": {
			// A size left behind by a previous topology must not resurrect the
			// group: `enabled` is the only switch.
			rs: func() *api.ReplsetSpec {
				rs := legacyRS("rs0", 3)
				rs.Arbiter = api.Arbiter{Enabled: false, Size: 1}
				rs.NonVoting = api.NonVotingSpec{Enabled: false, Size: 2}
				rs.Hidden = api.HiddenSpec{Enabled: false, Size: 1}
				return rs
			}(),
			want: []wantGroup{{
				name: naming.GroupMongod, component: naming.ComponentMongod,
				stsName: "cluster1-rs0", container: naming.ContainerMongod,
				configName: "cluster1-rs0-mongod", replicas: 3,
				member:      MemberConfig{Votes: 1, Priority: 2},
				dataBearing: true, primary: true, hasVolume: true,
				source: SourceRef{ReplsetName: "rs0"},
			}},
		},
		"config server": {
			rs: func() *api.ReplsetSpec {
				rs := legacyRS(api.ConfigReplSetName, 3)
				rs.ClusterRole = api.ClusterRoleConfigSvr
				return rs
			}(),
			want: []wantGroup{{
				// The group is still named mongod; only the component label
				// becomes "cfg". Call sites look groups up by component, so the
				// two must not be conflated.
				name: naming.GroupMongod, component: naming.ComponentConfigSrv,
				stsName: "cluster1-cfg", container: naming.ContainerMongod,
				configName: "cluster1-cfg-mongod", replicas: 3,
				member:      MemberConfig{Votes: 1, Priority: 2},
				dataBearing: true, primary: true, hasVolume: true,
				source: SourceRef{ReplsetName: api.ConfigReplSetName},
			}},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			set, err := Resolve(testCR(), tt.rs)
			require.NoError(t, err)

			assert.Equal(t, PolicyImplicit, set.GetPolicy(),
				"a replica set without instances[] always keeps the implicit vote policy")
			assertGroups(t, tt.want, set.GetAll())
		})
	}
}

// TestResolveLegacyGroupOrder pins the iteration order of a legacy topology.
//
// resolveLegacy does not sort: it appends mongod, arbiter, nonVoting, hidden in
// that fixed order. getEligibleMemberPod and downscaleTarget walk GetAll() and
// act on the first group that qualifies, so the order is behavior, not
// cosmetics. It is also deliberately *not* the alphabetical order that
// instances[] topologies get.
func TestResolveLegacyGroupOrder(t *testing.T) {
	rs := legacyRS("rs0", 4)
	rs.Arbiter = api.Arbiter{Enabled: true, Size: 1}
	rs.NonVoting = api.NonVotingSpec{Enabled: true, Size: 1, VolumeSpec: vol("1Gi")}
	rs.Hidden = api.HiddenSpec{Enabled: true, Size: 1, VolumeSpec: vol("1Gi")}

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	assert.Equal(t,
		[]string{naming.GroupMongod, naming.GroupArbiter, naming.GroupNonVoting, naming.GroupHidden},
		set.GetNames())
	assert.Equal(t,
		[]string{"cluster1-rs0", "cluster1-rs0-arbiter", "cluster1-rs0-nv", "cluster1-rs0-hidden"},
		set.GetStatefulSetNames())
}

// TestResolveLegacyGroupsOwnTheirConfig proves the per-role blocks are read
// from their own spec rather than inherited from the base replica set. Reading
// the wrong one silently gives a group the base group's storage class, probe or
// mongod configuration.
func TestResolveLegacyGroupsOwnTheirConfig(t *testing.T) {
	baseVol, nvVol, hiddenVol := vol("1Gi"), vol("5Gi"), vol("9Gi")

	rs := &api.ReplsetSpec{
		Name:          "rs0",
		Size:          new(int32(3)),
		VolumeSpec:    baseVol,
		Configuration: api.MongoConfiguration("base: true"),
		NonVoting: api.NonVotingSpec{
			Enabled: true, Size: 1, VolumeSpec: nvVol,
			Configuration: api.MongoConfiguration("nonvoting: true"),
		},
		Hidden: api.HiddenSpec{
			Enabled: true, Size: 1, VolumeSpec: hiddenVol,
			Configuration: api.MongoConfiguration("hidden: true"),
		},
		Arbiter: api.Arbiter{Enabled: true, Size: 1},
	}

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	storage := func(name string) string {
		g, ok := set.GetByName(name)
		require.True(t, ok)
		require.NotNil(t, g.VolumeSpec)
		return g.VolumeSpec.PersistentVolumeClaim.Resources.Requests.Storage().String()
	}
	config := func(name string) string {
		g, ok := set.GetByName(name)
		require.True(t, ok)
		return string(g.Configuration)
	}

	assert.Equal(t, "1Gi", storage(naming.GroupMongod))
	assert.Equal(t, "5Gi", storage(naming.GroupNonVoting))
	assert.Equal(t, "9Gi", storage(naming.GroupHidden))

	assert.Equal(t, "base: true", config(naming.GroupMongod))
	assert.Equal(t, "nonvoting: true", config(naming.GroupNonVoting))
	assert.Equal(t, "hidden: true", config(naming.GroupHidden))

	// An arbiter has no configuration block of its own and mounts the base one.
	assert.Equal(t, "base: true", config(naming.GroupArbiter))

	// Resolved volume specs are copies. A caller that rewrites a group's
	// requested size during a resize must not reach back into the CR.
	g, ok := set.GetByName(naming.GroupMongod)
	require.True(t, ok)
	assert.NotSame(t, baseVol, g.VolumeSpec, "resolved volume spec must be a copy of the CR's")
}

// TestResolveInstances covers the instances[] path: what each group resolves to
// and, above all, which vote policy the replica set lands in. The policy
// decides whether mgo.go runs SetVotes or ApplyMemberConfig, so two topologies
// that look nearly identical can produce entirely different rs.conf() members.
func TestResolveInstances(t *testing.T) {
	tests := map[string]struct {
		rs         *api.ReplsetSpec
		wantPolicy Policy
		want       []wantGroup
	}{
		"groups are sorted by name": {
			// Reordering the CR list must not change iteration order, and
			// therefore must not change member IDs or the rollout sequence.
			rs: instanceRS("rs0",
				inst("hot", 1, nil),
				arbiterInst("arb", 1),
				inst("cold", 1, nil),
			),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{
				{
					name: "arb", component: "arb",
					stsName: "cluster1-rs0-arb", container: naming.ContainerArbiter,
					configName: "cluster1-rs0-arb", replicas: 1,
					member:      MemberConfig{ArbiterOnly: true, Votes: 1, Priority: 0},
					dataBearing: false, primary: false, hasVolume: false,
					source: SourceRef{ReplsetName: "rs0", InstanceName: "arb"},
				},
				{
					name: "cold", component: "cold",
					stsName: "cluster1-rs0-cold", container: naming.ContainerMongod,
					configName: "cluster1-rs0-cold", replicas: 1,
					member:      MemberConfig{Votes: 1, Priority: 2, Tags: emptyTags},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: "cold"},
				},
				{
					name: "hot", component: "hot",
					stsName: "cluster1-rs0-hot", container: naming.ContainerMongod,
					configName: "cluster1-rs0-hot", replicas: 1,
					member:      MemberConfig{Votes: 1, Priority: 2, Tags: emptyTags},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: "hot"},
				},
			},
		},
		"explicit member settings are carried through": {
			rs: instanceRS("rs0", inst("hot", 2, &api.MemberConfigSpec{
				Priority: new(int32(10)),
				Votes:    new(int32(1)),
				Tags:     map[string]string{"workload": "hot"},
			})),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{{
				name: "hot", component: "hot",
				stsName: "cluster1-rs0-hot", container: naming.ContainerMongod,
				configName: "cluster1-rs0-hot", replicas: 2,
				member: MemberConfig{
					Votes: 1, Priority: 10,
					Tags: map[string]string{"workload": "hot"},
				},
				dataBearing: true, primary: true, hasVolume: true,
				source: SourceRef{ReplsetName: "rs0", InstanceName: "hot"},
			}},
		},
		"hidden is forced to priority 0": {
			// MongoDB rejects a hidden member with a nonzero priority, so a
			// stale priority must be flattened here rather than surfacing as a
			// reconfig failure.
			rs: instanceRS("rs0", inst("an", 1, &api.MemberConfigSpec{
				Hidden: new(true), Priority: new(int32(5)), Votes: new(int32(1)),
			})),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{{
				name: "an", component: "an",
				stsName: "cluster1-rs0-an", container: naming.ContainerMongod,
				configName: "cluster1-rs0-an", replicas: 1,
				member:      MemberConfig{Hidden: true, Votes: 1, Priority: 0, Tags: emptyTags},
				dataBearing: true, primary: false, hasVolume: true,
				source: SourceRef{ReplsetName: "rs0", InstanceName: "an"},
			}},
		},
		"votes 0 forces priority 0": {
			rs: instanceRS("rs0", inst("ro", 1, &api.MemberConfigSpec{
				Votes: new(int32(0)), Priority: new(int32(9)),
			})),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{{
				name: "ro", component: "ro",
				stsName: "cluster1-rs0-ro", container: naming.ContainerMongod,
				configName: "cluster1-rs0-ro", replicas: 1,
				member:      MemberConfig{Votes: 0, Priority: 0, Tags: emptyTags},
				dataBearing: true, primary: false, hasVolume: true,
				source: SourceRef{ReplsetName: "rs0", InstanceName: "ro"},
			}},
		},
		"an arbiter loses its volume and its tags": {
			// An arbiter holds no data and MongoDB carries no tags on one, so
			// both are dropped no matter what the user declared.
			rs: instanceRS("rs0", api.InstanceSpec{
				Name: "arb", Replicas: 1, VolumeSpec: vol("1Gi"),
				RSConfig: &api.MemberConfigSpec{
					ArbiterOnly: new(true), Votes: new(int32(1)),
					Tags: map[string]string{"workload": "ignored"},
				},
			}),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{{
				name: "arb", component: "arb",
				stsName: "cluster1-rs0-arb", container: naming.ContainerArbiter,
				configName: "cluster1-rs0-arb", replicas: 1,
				member:      MemberConfig{ArbiterOnly: true, Votes: 1, Priority: 0},
				dataBearing: false, primary: false, hasVolume: false,
				source: SourceRef{ReplsetName: "rs0", InstanceName: "arb"},
			}},
		},
		"a scaled-to-zero group stays declared": {
			// Replicas 0 is a live topology, not a removal: the group keeps its
			// identity so its workload is reconciled to zero rather than
			// retired. GetPrimaryEligible filters it out separately.
			rs:         instanceRS("rs0", inst("hot", 0, &api.MemberConfigSpec{Priority: new(int32(5))})),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{{
				name: "hot", component: "hot",
				stsName: "cluster1-rs0-hot", container: naming.ContainerMongod,
				configName: "cluster1-rs0-hot", replicas: 0,
				member:      MemberConfig{Votes: 1, Priority: 5, Tags: emptyTags},
				dataBearing: true, primary: true, hasVolume: true,
				source: SourceRef{ReplsetName: "rs0", InstanceName: "hot"},
			}},
		},
		"reserved names with no rsConfig keep the implicit policy": {
			// This is the format-only translation of a legacy CR. It must mean
			// exactly what the legacy blocks meant, role tags included.
			rs: instanceRS("rs0",
				implicitInst(api.ReservedGroupMongod, 3),
				implicitInst(api.ReservedGroupNonVoting, 1),
			),
			wantPolicy: PolicyImplicit,
			want: []wantGroup{
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 3,
					member:      MemberConfig{Votes: 1, Priority: 2},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupMongod},
				},
				{
					name: naming.GroupNonVoting, component: naming.ComponentNonVoting,
					stsName: "cluster1-rs0-nv", container: naming.ContainerNonVoting,
					configName: "cluster1-rs0-nv", replicas: 1,
					member: MemberConfig{
						Votes: 0, Priority: 0,
						Tags: map[string]string{naming.ComponentNonVoting: "true"},
					},
					dataBearing: true, primary: false, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupNonVoting},
				},
			},
		},
		"a reserved name with no rsConfig keeps legacy meaning even when the policy is explicit": {
			// The policy is per-replica-set (it picks the vote engine) but the
			// reserved-name fallback is per-instance. mongod carries an
			// rsConfig, so the replica set is explicit; nonVoting carries none,
			// so it still resolves to the legacy non-voting member.
			rs: instanceRS("rs0",
				inst(api.ReservedGroupMongod, 3, nil),
				implicitInst(api.ReservedGroupNonVoting, 1),
			),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 3,
					member:      MemberConfig{Votes: 1, Priority: 2, Tags: emptyTags},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupMongod},
				},
				{
					name: naming.GroupNonVoting, component: naming.ComponentNonVoting,
					stsName: "cluster1-rs0-nv", container: naming.ContainerNonVoting,
					configName: "cluster1-rs0-nv", replicas: 1,
					member: MemberConfig{
						Votes: 0, Priority: 0,
						Tags: map[string]string{naming.ComponentNonVoting: "true"},
					},
					dataBearing: true, primary: false, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupNonVoting},
				},
			},
		},
		"an empty rsConfig on a reserved name opts it out of legacy meaning": {
			// Declaring rsConfig at all, even empty, is the signal that the
			// user is stating member intent. nonVoting then stops meaning
			// "no vote" and takes the ordinary instance defaults.
			rs: instanceRS("rs0",
				inst(api.ReservedGroupMongod, 3, nil),
				inst(api.ReservedGroupNonVoting, 1, nil),
			),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 3,
					member:      MemberConfig{Votes: 1, Priority: 2, Tags: emptyTags},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupMongod},
				},
				{
					name: naming.GroupNonVoting, component: naming.ComponentNonVoting,
					stsName: "cluster1-rs0-nv", container: naming.ContainerNonVoting,
					configName: "cluster1-rs0-nv", replicas: 1,
					// Votes 1, priority 2 and no role tag: nothing about the
					// name is special any more.
					member:      MemberConfig{Votes: 1, Priority: 2, Tags: emptyTags},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupNonVoting},
				},
			},
		},
		"a custom group name makes the whole replica set explicit": {
			// mongod declares no rsConfig and still resolves to the legacy
			// member, but the presence of "hot" alone flips the vote engine
			// from SetVotes to ApplyMemberConfig for every member.
			rs: instanceRS("rs0",
				implicitInst(api.ReservedGroupMongod, 3),
				inst("hot", 1, &api.MemberConfigSpec{Priority: new(int32(10))}),
			),
			wantPolicy: PolicyExplicit,
			want: []wantGroup{
				{
					name: "hot", component: "hot",
					stsName: "cluster1-rs0-hot", container: naming.ContainerMongod,
					configName: "cluster1-rs0-hot", replicas: 1,
					member:      MemberConfig{Votes: 1, Priority: 10, Tags: emptyTags},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: "hot"},
				},
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 3,
					member:      MemberConfig{Votes: 1, Priority: 2},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupMongod},
				},
			},
		},
		"a reserved arbiter name with no rsConfig resolves to an arbiter": {
			// The name alone is enough: no arbiterOnly flag is declared, yet
			// the group holds no data and gets the arbiter container.
			rs: instanceRS("rs0",
				implicitInst(api.ReservedGroupMongod, 3),
				api.InstanceSpec{Name: api.ReservedGroupArbiter, Replicas: 1},
			),
			wantPolicy: PolicyImplicit,
			want: []wantGroup{
				{
					name: naming.GroupArbiter, component: naming.ComponentArbiter,
					stsName: "cluster1-rs0-arbiter", container: naming.ContainerArbiter,
					configName: "cluster1-rs0-mongod", replicas: 1,
					member:      MemberConfig{ArbiterOnly: true, Votes: 1, Priority: 0},
					dataBearing: false, primary: false, hasVolume: false,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupArbiter},
				},
				{
					name: naming.GroupMongod, component: naming.ComponentMongod,
					stsName: "cluster1-rs0", container: naming.ContainerMongod,
					configName: "cluster1-rs0-mongod", replicas: 3,
					member:      MemberConfig{Votes: 1, Priority: 2},
					dataBearing: true, primary: true, hasVolume: true,
					source: SourceRef{ReplsetName: "rs0", InstanceName: naming.GroupMongod},
				},
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			set, err := Resolve(testCR(), tt.rs)
			require.NoError(t, err)

			assert.Equal(t, tt.wantPolicy, set.GetPolicy(), "vote policy")
			assertGroups(t, tt.want, set.GetAll())
		})
	}
}

// TestResolveInstancesOwnTheirPodConfig proves each group's pod-level settings
// come from its own instance entry rather than from the replica set or from a
// sibling. This is the whole point of the feature: a hot group on NVMe next to
// a cold group on cheap disks.
func TestResolveInstancesOwnTheirPodConfig(t *testing.T) {
	hotVol, coldVol := vol("100Gi"), vol("500Gi")

	rs := instanceRS("rs0",
		api.InstanceSpec{Name: "cold", Replicas: 1, VolumeSpec: coldVol,
			RSConfig: &api.MemberConfigSpec{Priority: new(int32(0)), Votes: new(int32(1))}},
		api.InstanceSpec{Name: "hot", Replicas: 2, VolumeSpec: hotVol,
			RSConfig: &api.MemberConfigSpec{Priority: new(int32(10)), Votes: new(int32(1))}},
	)
	// Configuration is per group
	rs.Configuration = api.MongoConfiguration("shared: true")
	rs.Instances[1].Configuration = api.MongoConfiguration("hot: true")

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	hot, ok := set.GetByName("hot")
	require.True(t, ok)
	cold, ok := set.GetByName("cold")
	require.True(t, ok)

	assert.Equal(t, "100Gi", hot.VolumeSpec.PersistentVolumeClaim.Resources.Requests.Storage().String())
	assert.Equal(t, "500Gi", cold.VolumeSpec.PersistentVolumeClaim.Resources.Requests.Storage().String())
	assert.NotSame(t, hotVol, hot.VolumeSpec, "resolved volume spec must be a copy")

	assert.Equal(t, api.MongoConfiguration("hot: true"), hot.Configuration,
		"a group runs the mongod configuration it declares, not the replica set's")
	assert.Empty(t, cold.Configuration,
		"Resolve does not apply the replica-set fallback; SetDefaults does")

	// Only hot can be elected: cold votes but has priority 0.
	assert.True(t, hot.PrimaryEligible)
	assert.False(t, cold.PrimaryEligible)
	assert.Equal(t, []string{"hot"}, groupNames(set.GetPrimaryEligible()))
}

// TestResolveIdentity pins the derived names for every group shape. These feed
// StatefulSet names, config map names and immutable selector labels, so a
// change here renames or orphans a live workload.
func TestResolveIdentity(t *testing.T) {
	tests := map[string]struct {
		rs            *api.ReplsetSpec
		group         string
		wantSTS       string
		wantConfig    string
		wantComponent string
		wantContainer string
		wantHookCM    string
	}{
		"base mongod": {
			rs: legacyRS("rs0", 3), group: naming.GroupMongod,
			wantSTS: "cluster1-rs0", wantConfig: "cluster1-rs0-mongod",
			wantComponent: "mongod", wantContainer: "mongod",
			wantHookCM: "cluster1-rs0-mongod-hookscript",
		},
		"nonvoting uses the short suffix": {
			rs: func() *api.ReplsetSpec {
				rs := legacyRS("rs0", 3)
				rs.NonVoting = api.NonVotingSpec{Enabled: true, Size: 1, VolumeSpec: vol("1Gi")}
				return rs
			}(), group: naming.GroupNonVoting,
			wantSTS: "cluster1-rs0-nv", wantConfig: "cluster1-rs0-nv",
			wantComponent: "nonVoting", wantContainer: "mongod-nv",
			wantHookCM: "cluster1-rs0-nonvoting-hookscript",
		},
		"custom group": {
			rs: instanceRS("rs0", inst("hot", 1, nil)), group: "hot",
			wantSTS: "cluster1-rs0-hot", wantConfig: "cluster1-rs0-hot",
			wantComponent: "hot", wantContainer: "mongod",
			wantHookCM: "cluster1-rs0-hot-hookscript",
		},
		"custom arbiter-only group": {
			// The container name comes from arbiterOnly, not from the name, so
			// a custom arbiter still stays out of the mongod exec paths.
			rs: instanceRS("rs0", arbiterInst("witness", 1)), group: "witness",
			wantSTS: "cluster1-rs0-witness", wantConfig: "cluster1-rs0-witness",
			wantComponent: "witness", wantContainer: "mongod-arbiter",
			wantHookCM: "cluster1-rs0-witness-hookscript",
		},
		"config server base group": {
			rs: func() *api.ReplsetSpec {
				rs := legacyRS(api.ConfigReplSetName, 3)
				rs.ClusterRole = api.ClusterRoleConfigSvr
				return rs
			}(), group: naming.GroupMongod,
			wantSTS: "cluster1-cfg", wantConfig: "cluster1-cfg-mongod",
			wantComponent: "cfg", wantContainer: "mongod",
			wantHookCM: "cluster1-cfg-cfg-hookscript",
		},
	}

	cr := testCR()

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			set, err := Resolve(cr, tt.rs)
			require.NoError(t, err)

			g, ok := set.GetByName(tt.group)
			require.Truef(t, ok, "group %q not resolved (have %v)", tt.group, set.GetNames())

			assert.Equal(t, tt.wantSTS, g.STSName)
			assert.Equal(t, tt.wantConfig, g.ConfigName)
			assert.Equal(t, tt.wantComponent, g.Component)
			assert.Equal(t, tt.wantContainer, g.ContainerName)
			assert.Equal(t, tt.wantHookCM,
				naming.GroupHookScriptConfigMapName(cr, tt.rs, g.Component))

			// Lookup by component must find the same group: every controller
			// path that starts from a pod or StatefulSet label uses this.
			byComponent, ok := set.GetByComponent(tt.wantComponent)
			require.True(t, ok)
			assert.Equal(t, g.Name, byComponent.Name)

			byLabels, ok := set.GetByLabels(g.Labels)
			require.True(t, ok)
			assert.Equal(t, g.Name, byLabels.Name)
		})
	}
}

// TestResolveEmptyReplsetIsNotAnError covers the zero-size topologies the
// controller passes in during a pause, a shutdown or a restore. Resolve must
// still describe the groups so their workloads can be scaled to zero, rather
// than reporting the replica set as undeclared.
func TestResolveEmptyReplsetIsNotAnError(t *testing.T) {
	t.Run("legacy at size zero", func(t *testing.T) {
		set, err := Resolve(testCR(), legacyRS("rs0", 0))
		require.NoError(t, err)

		require.Equal(t, 1, set.Len())
		assert.Equal(t, int32(0), set.GetTotalMemberCount())
		assert.Empty(t, set.GetPrimaryEligible(),
			"a group with no members cannot hold the primary")

		g, ok := set.GetByName(naming.GroupMongod)
		require.True(t, ok)
		assert.Equal(t, "cluster1-rs0", g.STSName,
			"identity survives a scale to zero so the workload can still be found")
	})

	t.Run("instances at replicas zero", func(t *testing.T) {
		set, err := Resolve(testCR(), instanceRS("rs0",
			inst("hot", 0, nil), inst("cold", 0, nil)))
		require.NoError(t, err)

		assert.Equal(t, 2, set.Len())
		assert.Equal(t, int32(0), set.GetTotalMemberCount())
		assert.Equal(t, int32(0), set.GetVoterCount())
		assert.Empty(t, set.GetPrimaryEligible())
	})
}

func groupNames(groups []Group) []string {
	names := make([]string, 0, len(groups))
	for i := range groups {
		names = append(names, groups[i].Name)
	}
	return names
}

// TestResolveVoterCountUnderMixedInstanceConfig pins what the resolver actually
// puts in rs.conf() when reserved and custom member intent are mixed.
func TestResolveVoterCountUnderMixedInstanceConfig(t *testing.T) {
	rs := instanceRS("rs0",
		inst(api.ReservedGroupMongod, 3, nil),
		implicitInst(api.ReservedGroupNonVoting, 1),
	)

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	assert.Equal(t, PolicyExplicit, set.GetPolicy())
	assert.Equal(t, int32(4), set.GetTotalMemberCount())
	assert.Equal(t, int32(3), set.GetVoterCount(),
		"nonVoting declares no rsConfig, so it keeps the legacy zero-vote meaning")
	assert.Equal(t, int32(3), set.GetDataBearingVoterCount())
	assert.Equal(t, int32(1), set.GetNonVotingMemberCount())
}

// TestReservedNamesReproduceLegacyConfiguration tests that a legacy replica
// set rewritten as instances[] under the reserved names resolves to the same
// ConfigMap, holding the same content, for every role.
func TestReservedNamesReproduceLegacyConfiguration(t *testing.T) {
	const (
		rsConf     = "operationProfiling:\n  mode: slowOp\n"
		nonVoting  = "storage:\n  wiredTiger:\n    engineConfig:\n      cacheSizeGB: 1\n"
		hiddenConf = "operationProfiling:\n  mode: all\n"
	)

	for _, tt := range []struct {
		name string
		// per-role configuration the legacy CR declares
		nvConf, hidConf api.MongoConfiguration
	}{
		{name: "no per-role configuration"},
		{name: "per-role configuration on both roles", nvConf: nonVoting, hidConf: hiddenConf},
		{name: "per-role configuration on one role", nvConf: nonVoting},
	} {
		t.Run(tt.name, func(t *testing.T) {
			legacy := legacyRS("rs0", 3)
			legacy.Configuration = rsConf
			legacy.Arbiter = api.Arbiter{Enabled: true, Size: 1}
			legacy.NonVoting = api.NonVotingSpec{Enabled: true, Size: 2, Configuration: tt.nvConf, VolumeSpec: vol("1Gi")}
			legacy.Hidden = api.HiddenSpec{Enabled: true, Size: 1, Configuration: tt.hidConf, VolumeSpec: vol("1Gi")}

			rewritten := instanceRS("rs0",
				implicitInst(api.ReservedGroupMongod, 3),
				arbiterInst(api.ReservedGroupArbiter, 1),
				api.InstanceSpec{Name: api.ReservedGroupNonVoting, Replicas: 2,
					Configuration: tt.nvConf, VolumeSpec: vol("1Gi")},
				api.InstanceSpec{Name: api.ReservedGroupHidden, Replicas: 1,
					Configuration: tt.hidConf, VolumeSpec: vol("1Gi")},
			)
			rewritten.Configuration = rsConf
			// SetDefaults resolves each group's configuration; Resolve only
			// copies what it left behind.
			for i := range rewritten.Instances {
				require.NoError(t, rewritten.Instances[i].SetDefaults(testCR(), rewritten))
			}

			legacySet, err := Resolve(testCR(), legacy)
			require.NoError(t, err)
			rewrittenSet, err := Resolve(testCR(), rewritten)
			require.NoError(t, err)

			require.ElementsMatch(t, groupNames(legacySet.GetAll()), groupNames(rewrittenSet.GetAll()))

			for _, want := range legacySet.GetAll() {
				got, ok := rewrittenSet.GetByName(want.Name)
				require.Truef(t, ok, "group %s missing from the rewritten replica set", want.Name)

				assert.Equalf(t, want.ConfigName, got.ConfigName,
					"group %s mounts a different ConfigMap after the rewrite", want.Name)
				assert.Equalf(t, want.Configuration, got.Configuration,
					"group %s runs different mongod configuration after the rewrite", want.Name)
			}
		})
	}
}

// TestArbiterConfigurationWithoutMongodGroup covers the shape legacy cannot
// produce: an arbiter in a replica set with no mongod group. There is nothing
// to share a ConfigMap with, so it gets one of its own and may configure it.
func TestArbiterConfigurationWithoutMongodGroup(t *testing.T) {
	const own = "systemLog:\n  quiet: true\n"

	rs := instanceRS("rs0",
		inst("data", 3, nil),
		arbiterInst("arbiter", 1),
	)
	rs.Configuration = "operationProfiling:\n  mode: slowOp\n"
	rs.Instances[1].Configuration = own
	for i := range rs.Instances {
		require.NoError(t, rs.Instances[i].SetDefaults(testCR(), rs))
	}

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	arbiter, ok := set.GetByName("arbiter")
	require.True(t, ok)

	assert.Equal(t, "cluster1-rs0-arbiter", arbiter.ConfigName,
		"with no mongod group the arbiter owns its ConfigMap")
	assert.Equal(t, api.MongoConfiguration(own), arbiter.Configuration,
		"and may therefore configure it")
}

func TestArbiterFollowsMongodGroupConfiguration(t *testing.T) {
	const mongodOwn = "operationProfiling:\n  mode: all\n"

	rs := instanceRS("rs0",
		api.InstanceSpec{Name: api.ReservedGroupMongod, Replicas: 3,
			Configuration: mongodOwn, VolumeSpec: vol("1Gi")},
		arbiterInst(api.ReservedGroupArbiter, 1),
	)
	rs.Configuration = "operationProfiling:\n  mode: slowOp\n"
	// The arbiter asks for something of its own; it shares mongod's ConfigMap,
	// so it cannot have it.
	rs.Instances[1].Configuration = "systemLog:\n  quiet: true\n"

	for i := range rs.Instances {
		require.NoError(t, rs.Instances[i].SetDefaults(testCR(), rs))
	}

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	mongod, ok := set.GetByName(api.ReservedGroupMongod)
	require.True(t, ok)
	arbiter, ok := set.GetByName(api.ReservedGroupArbiter)
	require.True(t, ok)

	assert.Equal(t, mongod.ConfigName, arbiter.ConfigName, "the arbiter shares the mongod group's ConfigMap")
	assert.Equal(t, mongod.Configuration, arbiter.Configuration,
		"and therefore its content, whichever group writes it last")
	assert.Equal(t, api.MongoConfiguration(mongodOwn), arbiter.Configuration)
}
