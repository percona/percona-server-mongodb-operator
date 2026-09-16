package membergroup

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

// mixedSet is one replica set exercising every counter branch at once:
//
//	analytics  2 members  votes 0            data-bearing, not electable
//	arb        1 member   arbiterOnly        votes, holds no data
//	data       3 members  votes 1, prio 2    data-bearing, electable
//	reporting  1 member   hidden, votes 1    data-bearing, votes, not electable
//
// Groups come back sorted by name, so the order is analytics, arb, data,
// reporting — deliberately not the order they are declared in.
//
// The counters feed isUnsafePSA, shouldSetDefaultRWConcern, smartUpdate's
// force-step-down decision and expectedExternalServiceNames. Each is trivial
// alone; the value is checking them against one topology where the right answer
// differs for every one of them.
func mixedSet(t *testing.T) (*Set, *api.ReplsetSpec) {
	t.Helper()

	rs := instanceRS("rs0",
		inst("data", 3, &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(2))}),
		inst("analytics", 2, &api.MemberConfigSpec{Votes: new(int32(0)), Priority: new(int32(0))}),
		arbiterInst("arb", 1),
		inst("reporting", 1, &api.MemberConfigSpec{
			Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0)),
		}),
	)

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	return set, rs
}

func TestSetCounters(t *testing.T) {
	set, _ := mixedSet(t)

	tests := map[string]struct {
		got  int32
		want int32
		why  string
	}{
		"total members": {
			got: set.GetTotalMemberCount(), want: 7,
			why: "every group's replicas, arbiters included",
		},
		"voters": {
			got: set.GetVoterCount(), want: 5,
			why: "data 3 + arb 1 + reporting 1; analytics votes 0",
		},
		"data-bearing voters": {
			got: set.GetDataBearingVoterCount(), want: 4,
			why: "the arbiter votes but holds no data, so quorum-with-data is one lower",
		},
		"arbiter members": {
			got: set.GetArbiterMemberCount(), want: 1,
			why: "any arbiter at all changes the implicit default write concern",
		},
		"non-voting members": {
			got: set.GetNonVotingMemberCount(), want: 2,
			why: "analytics only; an arbiter is never counted as non-voting even though it holds no data",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equalf(t, tt.want, tt.got, tt.why)
		})
	}

	assert.Equal(t, 4, set.Len())
	assert.Equal(t, "rs0", set.GetReplsetName())
	assert.Equal(t, PolicyExplicit, set.GetPolicy())
}

func TestSetNonVotingCountExcludesArbiters(t *testing.T) {
	set, err := Resolve(testCR(), instanceRS("rs0",
		inst("data", 3, &api.MemberConfigSpec{Votes: new(int32(1))}),
		inst("witness", 1, &api.MemberConfigSpec{
			ArbiterOnly: new(true), Votes: new(int32(0)), Priority: new(int32(0)),
		}),
	))
	require.NoError(t, err)

	assert.Equal(t, int32(0), set.GetNonVotingMemberCount(),
		"an arbiter is excluded by ArbiterOnly, not by its vote count")
	assert.Equal(t, int32(1), set.GetArbiterMemberCount())
	assert.Equal(t, int32(3), set.GetVoterCount())
	assert.Equal(t, int32(3), set.GetDataBearingVoterCount())
}

func TestSetFilters(t *testing.T) {
	set, _ := mixedSet(t)

	t.Run("names are sorted, not declaration-ordered", func(t *testing.T) {
		assert.Equal(t, []string{"analytics", "arb", "data", "reporting"}, set.GetNames())
	})

	t.Run("statefulset names", func(t *testing.T) {
		assert.Equal(t, []string{
			"cluster1-rs0-analytics",
			"cluster1-rs0-arb",
			"cluster1-rs0-data",
			"cluster1-rs0-reporting",
		}, set.GetStatefulSetNames())
	})

	t.Run("data-bearing excludes only the arbiter", func(t *testing.T) {
		assert.Equal(t, []string{"analytics", "data", "reporting"},
			groupNames(set.GetDataBearing()))
	})

	t.Run("primary-eligible excludes hidden, non-voting and arbiter groups", func(t *testing.T) {
		// reporting votes and holds data but is hidden, so its resolved
		// priority is 0; analytics has no vote; the arbiter has neither.
		assert.Equal(t, []string{"data"}, groupNames(set.GetPrimaryEligible()))
	})
}

func TestSetPrimaryEligibleRequiresMembers(t *testing.T) {
	set, err := Resolve(testCR(), instanceRS("rs0",
		inst("hot", 0, &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(10))}),
		inst("cold", 1, &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(0))}),
	))
	require.NoError(t, err)

	hot, ok := set.GetByName("hot")
	require.True(t, ok)
	assert.True(t, hot.PrimaryEligible,
		"the group's configuration still permits an election")

	assert.Empty(t, set.GetPrimaryEligible(),
		"but no group has a member that could hold the primary")
}

func TestSetLookups(t *testing.T) {
	set, _ := mixedSet(t)

	t.Run("by name", func(t *testing.T) {
		g, ok := set.GetByName("analytics")
		require.True(t, ok)
		assert.Equal(t, "analytics", g.Name)

		_, ok = set.GetByName("nope")
		assert.False(t, ok)

		_, ok = set.GetByName("")
		assert.False(t, ok)
	})

	t.Run("by component", func(t *testing.T) {
		g, ok := set.GetByComponent("reporting")
		require.True(t, ok)
		assert.Equal(t, "reporting", g.Name)

		_, ok = set.GetByComponent("nope")
		assert.False(t, ok)
	})

	t.Run("by labels", func(t *testing.T) {
		g, ok := set.GetByName("data")
		require.True(t, ok)

		found, ok := set.GetByLabels(g.Labels)
		require.True(t, ok)
		assert.Equal(t, "data", found.Name)
	})

	t.Run("labels without a usable component miss", func(t *testing.T) {
		for name, ls := range map[string]map[string]string{
			"nil map":         nil,
			"empty map":       {},
			"empty component": {naming.LabelKubernetesComponent: ""},
			"other labels":    {naming.LabelKubernetesReplset: "rs0"},
			"unknown group":   {naming.LabelKubernetesComponent: "retired"},
		} {
			t.Run(name, func(t *testing.T) {
				g, ok := set.GetByLabels(ls)
				assert.False(t, ok)
				assert.Equal(t, Group{}, g, "no group is returned alongside a miss")
			})
		}
	})
}

func TestSetLookupsFindLegacyComponents(t *testing.T) {
	rs := legacyRS("rs0", 3)
	rs.NonVoting = api.NonVotingSpec{Enabled: true, Size: 1, VolumeSpec: vol("1Gi")}
	rs.Arbiter = api.Arbiter{Enabled: true, Size: 1}

	set, err := Resolve(testCR(), rs)
	require.NoError(t, err)

	for component, wantGroup := range map[string]string{
		naming.ComponentMongod:    naming.GroupMongod,
		naming.ComponentNonVoting: naming.GroupNonVoting,
		naming.ComponentArbiter:   naming.GroupArbiter,
	} {
		g, ok := set.GetByComponent(component)
		require.Truef(t, ok, "component %q not found", component)
		assert.Equal(t, wantGroup, g.Name)
	}

	// "nv" is the StatefulSet suffix
	_, ok := set.GetByComponent(naming.ComponentNonVotingShort)
	assert.False(t, ok, "the short suffix must not resolve as a component")

	cfgRS := legacyRS(api.ConfigReplSetName, 3)
	cfgRS.ClusterRole = api.ClusterRoleConfigSvr
	cfgSet, err := Resolve(testCR(), cfgRS)
	require.NoError(t, err)

	g, ok := cfgSet.GetByComponent(naming.ComponentConfigSrv)
	require.True(t, ok)
	assert.Equal(t, naming.GroupMongod, g.Name,
		"the config server's group is still named mongod")

	_, ok = cfgSet.GetByComponent(naming.ComponentMongod)
	assert.False(t, ok,
		"a config server declares no mongod component, so a pod labelled that way is foreign")
}

func TestSetTotalMemberCountWithExternal(t *testing.T) {
	set, rs := mixedSet(t)

	require.Equal(t, int32(7), set.GetTotalMemberCount())

	t.Run("no external nodes", func(t *testing.T) {
		assert.Equal(t, int32(7), set.GetTotalMemberCountWithExternal(rs))
	})

	t.Run("external nodes are added whatever they are", func(t *testing.T) {
		rs.ExternalNodes = []*api.ExternalNode{
			{Host: "ext1", Port: 27017, Votes: 1, Priority: 2},
			{Host: "ext2", Port: 27017, Votes: 0, Priority: 0},
			{Host: "ext3", Port: 27017, Votes: 1, ArbiterOnly: true},
		}

		// The count is over the whole replica set, so votes and arbiter status
		// do not filter it. MaxMembers is what this feeds.
		assert.Equal(t, int32(10), set.GetTotalMemberCountWithExternal(rs))

		// The group-derived counters are unaffected: an external node has no
		// StatefulSet, no pod and no group.
		assert.Equal(t, int32(7), set.GetTotalMemberCount())
		assert.Equal(t, int32(5), set.GetVoterCount())
	})
}
func TestSetAccessorsOnEmptySet(t *testing.T) {
	var s Set

	assert.Equal(t, 0, s.Len())
	assert.Empty(t, s.GetAll())
	assert.Empty(t, s.GetNames())
	assert.Empty(t, s.GetStatefulSetNames())
	assert.Empty(t, s.GetDataBearing())
	assert.Empty(t, s.GetPrimaryEligible())
	assert.Equal(t, int32(0), s.GetTotalMemberCount())
	assert.Equal(t, int32(0), s.GetVoterCount())
	assert.Equal(t, int32(0), s.GetDataBearingVoterCount())
	assert.Equal(t, int32(0), s.GetArbiterMemberCount())
	assert.Equal(t, int32(0), s.GetNonVotingMemberCount())

	_, ok := s.GetByName("anything")
	assert.False(t, ok)
	_, ok = s.GetByComponent("anything")
	assert.False(t, ok)
	_, ok = s.GetByLabels(map[string]string{naming.LabelKubernetesComponent: "anything"})
	assert.False(t, ok)
}
