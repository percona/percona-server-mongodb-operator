package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// declared builds an instance with an rsConfig object. A non-nil rsConfig —
// even an empty one — is what takes the replica set out of the implicit vote
// policy, so the choice between declared and bare decides half of what these
// tests assert.
func declared(name string, replicas int32, cfg *MemberConfigSpec) InstanceSpec {
	if cfg == nil {
		cfg = &MemberConfigSpec{}
	}
	return InstanceSpec{Name: name, Replicas: replicas, RSConfig: cfg}
}

// bare builds an instance with no rsConfig at all.
func bare(name string, replicas int32) InstanceSpec {
	return InstanceSpec{Name: name, Replicas: replicas}
}

func TestUseImplicitVotePolicy(t *testing.T) {
	tests := map[string]struct {
		instances []InstanceSpec
		want      bool
	}{
		"no instances at all": {
			// A legacy replica set always keeps the historical vote algorithm.
			instances: nil,
			want:      true,
		},
		"every reserved name, none declaring rsConfig": {
			// The format-only translation of a legacy CR: it states no member
			// intent, so the reserved names keep meaning what they always meant.
			instances: []InstanceSpec{
				bare(ReservedGroupMongod, 3),
				bare(ReservedGroupArbiter, 1),
				bare(ReservedGroupNonVoting, 1),
				bare(ReservedGroupHidden, 1),
			},
			want: true,
		},
		"a reserved name with an empty rsConfig": {
			// Presence of the object is the signal, not its contents. Declaring
			// rsConfig at all means the user is stating member intent.
			instances: []InstanceSpec{declared(ReservedGroupMongod, 3, &MemberConfigSpec{})},
			want:      false,
		},
		"a reserved name with a populated rsConfig": {
			instances: []InstanceSpec{
				declared(ReservedGroupMongod, 3, &MemberConfigSpec{Votes: new(int32(1))}),
			},
			want: false,
		},
		"one custom name among reserved ones": {
			// A name the operator has no legacy meaning for cannot be resolved
			// implicitly, so the whole replica set becomes explicit.
			instances: []InstanceSpec{bare(ReservedGroupMongod, 3), bare("hot", 1)},
			want:      false,
		},
		"only custom names": {
			instances: []InstanceSpec{bare("hot", 2), bare("cold", 1)},
			want:      false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			rs := &ReplsetSpec{Name: "rs0", Instances: tt.instances}
			assert.Equal(t, tt.want, rs.UseImplicitVotePolicy())
		})
	}
}

func TestInstanceSpecMemberAccessors(t *testing.T) {
	tests := map[string]struct {
		inst InstanceSpec

		wantHasConfig   bool
		wantPriority    int32
		wantVotes       int32
		wantResolved    int32
		wantHidden      bool
		wantArbiterOnly bool
		wantEligible    bool
		wantDataBearing bool
	}{
		"no rsConfig takes the instance defaults": {
			inst:          bare("hot", 1),
			wantHasConfig: false,
			wantPriority:  defaultInstancePriority, wantVotes: defaultVotes,
			wantResolved: 2, wantEligible: true, wantDataBearing: true,
		},
		"an empty rsConfig takes the same defaults but is declared": {
			// Identical values, opposite HasMemberConfig. That difference alone
			// flips the replica set's vote policy.
			inst:          declared("hot", 1, &MemberConfigSpec{}),
			wantHasConfig: true,
			wantPriority:  defaultInstancePriority, wantVotes: defaultVotes,
			wantResolved: 2, wantEligible: true, wantDataBearing: true,
		},
		"explicit priority": {
			inst:          declared("hot", 1, &MemberConfigSpec{Priority: new(int32(10))}),
			wantHasConfig: true,
			wantPriority:  10, wantVotes: 1, wantResolved: 10,
			wantEligible: true, wantDataBearing: true,
		},
		"priority zero cannot be elected": {
			inst:          declared("cold", 1, &MemberConfigSpec{Priority: new(int32(0))}),
			wantHasConfig: true,
			wantPriority:  0, wantVotes: 1, wantResolved: 0,
			wantEligible: false, wantDataBearing: true,
		},
		"votes zero flattens the resolved priority": {
			inst: declared("an", 1, &MemberConfigSpec{
				Votes: new(int32(0)), Priority: new(int32(0)),
			}),
			wantHasConfig: true,
			wantPriority:  0, wantVotes: 0, wantResolved: 0,
			wantEligible: false, wantDataBearing: true,
		},
		"hidden with a stale priority still resolves to zero": {
			// MongoDB rejects a hidden member with a nonzero priority. The
			// declared value survives GetPriority so the user's input is not
			// silently rewritten, but ResolvedPriority is what reaches rs.conf().
			inst: declared("an", 1, &MemberConfigSpec{
				Hidden: new(true), Priority: new(int32(5)),
			}),
			wantHasConfig: true,
			wantPriority:  5, wantVotes: 1, wantResolved: 0,
			wantHidden: true, wantEligible: false, wantDataBearing: true,
		},
		"arbiterOnly holds no data and cannot be elected": {
			// Priority is unset here, so GetPriority returns the instance
			// default of 2 while ResolvedPriority returns 0. Anything reading
			// GetPriority directly for an arbiter would be wrong.
			inst: declared("arb", 1, &MemberConfigSpec{
				ArbiterOnly: new(true), Votes: new(int32(1)),
			}),
			wantHasConfig: true,
			wantPriority:  defaultInstancePriority, wantVotes: 1, wantResolved: 0,
			wantArbiterOnly: true, wantEligible: false, wantDataBearing: false,
		},
		"a group scaled to zero keeps its priority but cannot hold the primary": {
			// ResolvedPriority is a configuration property; eligibility also
			// needs a member to exist. bootstrapPod depends on the distinction.
			inst:          declared("hot", 0, &MemberConfigSpec{Priority: new(int32(5))}),
			wantHasConfig: true,
			wantPriority:  5, wantVotes: 1, wantResolved: 5,
			wantEligible: false, wantDataBearing: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.wantHasConfig, tt.inst.HasMemberConfig(), "HasMemberConfig")
			assert.Equal(t, tt.wantPriority, tt.inst.GetPriority(), "GetPriority")
			assert.Equal(t, tt.wantVotes, tt.inst.GetVotes(), "GetVotes")
			assert.Equal(t, tt.wantResolved, tt.inst.ResolvedPriority(), "ResolvedPriority")
			assert.Equal(t, tt.wantHidden, tt.inst.IsHidden(), "IsHidden")
			assert.Equal(t, tt.wantArbiterOnly, tt.inst.IsArbiterOnly(), "IsArbiterOnly")
			assert.Equal(t, tt.wantEligible, tt.inst.IsPrimaryEligible(), "IsPrimaryEligible")
			assert.Equal(t, tt.wantDataBearing, tt.inst.IsDataBearing(), "IsDataBearing")
		})
	}
}

func TestInstanceSpecGetTags(t *testing.T) {
	assert.Nil(t, bare("hot", 1).GetTags(),
		"no rsConfig means no tags, not an empty map")
	assert.Nil(t, declared("hot", 1, &MemberConfigSpec{}).GetTags())
	assert.Equal(t,
		map[string]string{"workload": "analytics"},
		declared("an", 1, &MemberConfigSpec{
			Tags: map[string]string{"workload": "analytics"},
		}).GetTags())
}

func TestInstanceCounts(t *testing.T) {
	tests := map[string]struct {
		rs *ReplsetSpec

		wantMembers     int32
		wantVoters      int32
		wantDataVoters  int32
		wantElectable   int32
		wantImplicitPol bool
	}{
		"every reserved role, nothing declared": {
			rs: &ReplsetSpec{Name: "rs0", Instances: []InstanceSpec{
				bare(ReservedGroupMongod, 3),
				bare(ReservedGroupArbiter, 1),
				bare(ReservedGroupNonVoting, 2),
				bare(ReservedGroupHidden, 1),
			}},
			wantMembers: 7, wantVoters: 5, wantDataVoters: 4, wantElectable: 3,
			wantImplicitPol: true,
		},
		"the same names and counts, every one declared": {
			// Identical topology on paper. Declaring rsConfig strips every
			// reserved name of its legacy meaning, so the arbiter becomes a
			// data-bearing voter and the non-voting group starts voting. Read
			// this row next to the one above: that is the whole hinge.
			rs: &ReplsetSpec{Name: "rs0", Instances: []InstanceSpec{
				declared(ReservedGroupMongod, 3, nil),
				declared(ReservedGroupArbiter, 1, nil),
				declared(ReservedGroupNonVoting, 2, nil),
				declared(ReservedGroupHidden, 1, nil),
			}},
			wantMembers: 7, wantVoters: 7, wantDataVoters: 7, wantElectable: 7,
			wantImplicitPol: false,
		},
		"a mixed explicit topology": {
			rs: &ReplsetSpec{Name: "rs0", Instances: []InstanceSpec{
				declared("hot", 2, &MemberConfigSpec{Priority: new(int32(10))}),
				declared("cold", 1, &MemberConfigSpec{Priority: new(int32(0)), Votes: new(int32(1))}),
				declared("an", 1, &MemberConfigSpec{Votes: new(int32(0)), Priority: new(int32(0))}),
				declared("arb", 1, &MemberConfigSpec{ArbiterOnly: new(true), Votes: new(int32(1))}),
			}},
			wantMembers: 5, wantVoters: 4, wantDataVoters: 3, wantElectable: 2,
			wantImplicitPol: false,
		},
		"external voters count toward the replica set": {
			// External nodes have no workload, but MongoDB's limits are over
			// the whole config, so they enter every count.
			rs: &ReplsetSpec{Name: "rs0",
				Instances: []InstanceSpec{bare(ReservedGroupMongod, 3)},
				ExternalNodes: []*ExternalNode{
					{Host: "ext1", Votes: 1, Priority: 2},
					{Host: "ext2", Votes: 1, Priority: 2},
				},
			},
			wantMembers: 5, wantVoters: 5, wantDataVoters: 5, wantElectable: 3,
			wantImplicitPol: true,
		},
		"an external arbiter votes without bearing data": {
			rs: &ReplsetSpec{Name: "rs0",
				Instances: []InstanceSpec{bare(ReservedGroupMongod, 3)},
				ExternalNodes: []*ExternalNode{
					{Host: "ext1", Votes: 1, ArbiterOnly: true},
				},
			},
			wantMembers: 4, wantVoters: 4, wantDataVoters: 3, wantElectable: 3,
			wantImplicitPol: true,
		},
		"a non-voting external node adds a member only": {
			rs: &ReplsetSpec{Name: "rs0",
				Instances:     []InstanceSpec{bare(ReservedGroupMongod, 3)},
				ExternalNodes: []*ExternalNode{{Host: "ext1", Votes: 0}},
			},
			wantMembers: 4, wantVoters: 3, wantDataVoters: 3, wantElectable: 3,
			wantImplicitPol: true,
		},
		"a nil external entry is skipped": {
			// The slice holds pointers, so a malformed CR can leave a nil in it.
			rs: &ReplsetSpec{Name: "rs0",
				Instances:     []InstanceSpec{bare(ReservedGroupMongod, 3)},
				ExternalNodes: []*ExternalNode{nil},
			},
			wantMembers: 3, wantVoters: 3, wantDataVoters: 3, wantElectable: 3,
			wantImplicitPol: true,
		},
		"a group scaled to zero contributes nothing": {
			rs: &ReplsetSpec{Name: "rs0", Instances: []InstanceSpec{
				declared("hot", 3, &MemberConfigSpec{Priority: new(int32(10))}),
				declared("cold", 0, &MemberConfigSpec{Priority: new(int32(1))}),
			}},
			wantMembers: 3, wantVoters: 3, wantDataVoters: 3, wantElectable: 3,
			wantImplicitPol: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, tt.wantImplicitPol, tt.rs.UseImplicitVotePolicy(),
				"the counts below depend on the policy, so pin it first")

			members, voters, dataVoters, electable := tt.rs.instanceCounts()
			assert.Equal(t, tt.wantMembers, members, "members")
			assert.Equal(t, tt.wantVoters, voters, "voters")
			assert.Equal(t, tt.wantDataVoters, dataVoters, "data-bearing voters")
			assert.Equal(t, tt.wantElectable, electable, "primary-eligible members")
		})
	}
}

func TestCheckSafeInstanceDefaults(t *testing.T) {
	tlsModeConf := MongoConfiguration("net:\n  tls:\n    mode: requireTLS")

	tests := map[string]struct {
		rs      *ReplsetSpec
		unsafe  UnsafeFlags
		wantErr string
	}{
		"a healthy three-member set": {
			rs: &ReplsetSpec{Instances: []InstanceSpec{declared("hot", 3, nil)}},
		},
		"no instance can hold the primary": {
			rs: &ReplsetSpec{Instances: []InstanceSpec{
				declared("hot", 3, &MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(0))}),
			}},
			wantErr: "no instance can hold the primary",
		},
		"too few data-bearing voters": {
			// Three voters, but two of them are arbiters, so a single node
			// failure loses the only copy of the data.
			rs: &ReplsetSpec{Instances: []InstanceSpec{
				declared("hot", 1, nil),
				declared("arb", 2, &MemberConfigSpec{ArbiterOnly: new(true), Votes: new(int32(1))}),
			}},
			wantErr: "a replica set needs at least 3 data-bearing voting members, got 1",
		},
		"beyond the voting ceiling": {
			rs:      &ReplsetSpec{Instances: []InstanceSpec{declared("hot", 9, nil)}},
			wantErr: "a replica set supports at most 7 voting members, got 9",
		},
		"beyond the voting ceiling under the implicit policy": {
			// The cap is enforced whichever vote engine will run. Before
			// cd7e79f81 this was only checked for explicit topologies, so a
			// legacy-shaped instances[] could reach rs.initiate with 9 voters.
			rs:      &ReplsetSpec{Instances: []InstanceSpec{bare(ReservedGroupMongod, 9)}},
			wantErr: "a replica set supports at most 7 voting members, got 9",
		},
		"external nodes count toward the voting ceiling": {
			rs: &ReplsetSpec{
				Instances: []InstanceSpec{declared("hot", 5, nil)},
				ExternalNodes: []*ExternalNode{
					{Host: "e1", Votes: 1}, {Host: "e2", Votes: 1}, {Host: "e3", Votes: 1},
				},
			},
			wantErr: "a replica set supports at most 7 voting members, got 8",
		},
		"an even voter count": {
			rs:      &ReplsetSpec{Instances: []InstanceSpec{declared("hot", 4, nil)}},
			wantErr: "the number of voting members must be odd, got 4",
		},
		"an even voter count is allowed when unsafe": {
			rs:     &ReplsetSpec{Instances: []InstanceSpec{declared("hot", 4, nil)}},
			unsafe: UnsafeFlags{ReplsetSize: true},
		},
		"parity is not enforced under the implicit policy": {
			// mongod 3 + hidden 1 is four voters, which the legacy SetVotes
			// engine resolves at runtime. Enforcing parity here would reject a
			// topology the operator has always accepted.
			rs: &ReplsetSpec{Instances: []InstanceSpec{
				bare(ReservedGroupMongod, 3),
				bare(ReservedGroupHidden, 1),
			}},
		},
		"beyond the member ceiling, even when unsafe": {
			// 50 members is a MongoDB hard limit, not a safety preference, so
			// unsafeFlags does not waive it.
			rs:      &ReplsetSpec{Instances: []InstanceSpec{declared("hot", 51, nil)}},
			unsafe:  UnsafeFlags{ReplsetSize: true},
			wantErr: "a replica set supports at most 50 members, got 51",
		},
		"unsafe does not waive electability": {
			// primaryEligible moved out of the unsafe block in cd7e79f81: a
			// replica set with nothing electable can never serve a write, so
			// there is no configuration in which it is merely "unsafe".
			rs: &ReplsetSpec{Instances: []InstanceSpec{
				declared("hot", 1, &MemberConfigSpec{Votes: new(int32(0)), Priority: new(int32(0))}),
			}},
			unsafe:  UnsafeFlags{ReplsetSize: true},
			wantErr: "no instance can hold the primary",
		},
		"unsafe waives only the quorum minimum": {
			rs:     &ReplsetSpec{Instances: []InstanceSpec{declared("hot", 1, nil)}},
			unsafe: UnsafeFlags{ReplsetSize: true},
		},
		"external data-bearing voters satisfy the quorum minimum": {
			// The minimum is about the replica set, not about the workloads the
			// operator owns.
			rs: &ReplsetSpec{
				Instances:     []InstanceSpec{declared("hot", 1, nil)},
				ExternalNodes: []*ExternalNode{{Host: "e1", Votes: 1}, {Host: "e2", Votes: 1}},
			},
		},
		"tlsMode in the mongod configuration": {
			rs: &ReplsetSpec{
				Configuration: tlsModeConf,
				Instances:     []InstanceSpec{declared("hot", 3, nil)},
			},
			wantErr: "tlsMode must be set using spec.tls.mode",
		},
		"tlsMode is rejected even when unsafe": {
			rs: &ReplsetSpec{
				Configuration: tlsModeConf,
				Instances:     []InstanceSpec{declared("hot", 3, nil)},
			},
			unsafe:  UnsafeFlags{ReplsetSize: true},
			wantErr: "tlsMode must be set using spec.tls.mode",
		},
		"an unparseable mongod configuration": {
			rs: &ReplsetSpec{
				Configuration: MongoConfiguration("net:\n  tls: requireTLS"),
				Instances:     []InstanceSpec{declared("hot", 3, nil)},
			},
			wantErr: "get tls mode",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.rs.checkSafeInstanceDefaults(tt.unsafe)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestDerivedStatefulSetName(t *testing.T) {
	tests := map[string]struct {
		group string
		want  string
	}{
		// The base group keeps the bare replica set name: renaming it would
		// orphan every existing StatefulSet.
		"mongod takes no suffix":       {ReservedGroupMongod, "cluster1-rs0"},
		"nonVoting uses a short alias": {ReservedGroupNonVoting, "cluster1-rs0-nv"},
		"arbiter uses its own name":    {ReservedGroupArbiter, "cluster1-rs0-arbiter"},
		"hidden uses its own name":     {ReservedGroupHidden, "cluster1-rs0-hidden"},
		"a custom group is appended":   {"hot", "cluster1-rs0-hot"},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, DerivedStatefulSetName("cluster1", "rs0", tt.group))
		})
	}

	// The collision the "nv" CEL rule exists to prevent: a custom group called
	// "nv" derives the same StatefulSet name as the reserved nonVoting group.
	assert.Equal(t,
		DerivedStatefulSetName("cluster1", "rs0", ReservedGroupNonVoting),
		DerivedStatefulSetName("cluster1", "rs0", "nv"))
}

func TestReplsetGetSize(t *testing.T) {
	tests := map[string]struct {
		rs   ReplsetSpec
		want int32
	}{
		"legacy sums every enabled role": {
			rs: ReplsetSpec{
				Size:      3,
				Arbiter:   Arbiter{Enabled: true, Size: 1},
				NonVoting: NonVotingSpec{Enabled: true, Size: 2},
				Hidden:    HiddenSpec{Enabled: true, Size: 1},
			},
			want: 7,
		},
		"a disabled role contributes nothing even with a size": {
			rs: ReplsetSpec{
				Size:      3,
				Arbiter:   Arbiter{Enabled: false, Size: 1},
				NonVoting: NonVotingSpec{Enabled: false, Size: 2},
			},
			want: 3,
		},
		"instance mode sums the instances": {
			rs:   ReplsetSpec{Instances: []InstanceSpec{bare("a", 2), bare("b", 3)}},
			want: 5,
		},
		"instance mode ignores a stray size": {
			// The CRD forbids setting both, but GetSize feeds member limits and
			// status, so it must not double-count if one slips through.
			rs: ReplsetSpec{
				Size:      9,
				Arbiter:   Arbiter{Enabled: true, Size: 4},
				Instances: []InstanceSpec{bare("a", 2), bare("b", 3)},
			},
			want: 5,
		},
		"instance mode at zero": {
			rs:   ReplsetSpec{Instances: []InstanceSpec{bare("a", 0)}},
			want: 0,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, tt.rs.GetSize())
		})
	}
}
func TestReplsetInstanceLookup(t *testing.T) {
	rs := &ReplsetSpec{Name: "rs0", Instances: []InstanceSpec{
		bare("hot", 3), bare("cold", 1),
	}}

	assert.True(t, rs.InstanceMode())
	assert.False(t, (&ReplsetSpec{Name: "rs0", Size: 3}).InstanceMode())

	hot := rs.Instance("hot")
	require.NotNil(t, hot)

	hot.Replicas = 2
	assert.Equal(t, int32(2), rs.Instances[0].Replicas,
		"the returned pointer must alias the slice element")

	assert.Nil(t, rs.Instance("gone"))
	assert.Nil(t, rs.Instance(""), "an empty name must never match")
	assert.Nil(t, (&ReplsetSpec{Name: "rs0", Size: 3}).Instance("hot"))
}
