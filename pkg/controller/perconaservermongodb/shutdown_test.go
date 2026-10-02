package perconaservermongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

// shutdownPod describes one pod fixture for a shutdownTarget case.
type shutdownPod struct {
	group       string
	ordinal     int
	notReady    bool
	terminating bool
}

// tests the ordering of a replica set shutdown.
func TestShutdownTarget(t *testing.T) {
	data := func(name string, replicas int32, priority int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(priority)}}
	}
	nonVoting := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Votes: new(int32(0)), Priority: new(int32(0))}}
	}
	hidden := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0))}}
	}
	arbiter := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas,
			RSConfig: &api.MemberConfigSpec{ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}}
	}

	for _, tt := range []struct {
		name      string
		instances []api.InstanceSpec
		pods      []shutdownPod
		primary   string // pod name, empty for none
		// configVoters is how many voting members rs.conf() still lists. Zero
		// leaves it unreadable, which the shutdown treats as "in step with the
		// pods" -- the right default for cases about ordering, not quorum.
		configVoters int
		// configUnreadable are pods whose config read fails.
		configUnreadable []string
		want             map[string]int32
		wantDone         bool
		// wantStepDown is the pod the primary is expected to be handed over
		// from, recorded as a StepDown against it.
		wantStepDown   string
		wantNoStepDown bool
	}{
		{
			name:      "nothing running is already done",
			instances: []api.InstanceSpec{data("mongod", 3, 2)},
			pods:      nil,
			want:      map[string]int32{"mongod": 0},
			wantDone:  true,
		},
		{
			name:      "non-voting groups drain immediately and do not cost a pass",
			instances: []api.InstanceSpec{data("mongod", 3, 2), nonVoting("ro", 2)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
				{group: "ro", ordinal: 0}, {group: "ro", ordinal: 1},
			},
			primary: "sd-cr-rs0-0",
			want:    map[string]int32{"mongod": 2, "ro": 0},
		},
		{
			// Hidden members vote, so they go one per pass like any voter.
			name:      "a hidden group drains in phase 2",
			instances: []api.InstanceSpec{data("mongod", 3, 2), hidden("hid", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
				{group: "hid", ordinal: 0},
			},
			primary: "sd-cr-rs0-0",
			want:    map[string]int32{"mongod": 3, "hid": 0},
		},
		{
			name:      "an arbiter drains in phase 2",
			instances: []api.InstanceSpec{data("mongod", 3, 2), arbiter("arb", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
				{group: "arb", ordinal: 0},
			},
			primary: "sd-cr-rs0-0",
			want:    map[string]int32{"mongod": 3, "arb": 0},
		},
		{
			name:      "terminating pods are not counted",
			instances: []api.InstanceSpec{data("mongod", 3, 2), hidden("hid", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1},
				{group: "mongod", ordinal: 2, terminating: true},
				{group: "hid", ordinal: 0},
			},
			primary: "sd-cr-rs0-0",
			want:    map[string]int32{"mongod": 2, "hid": 0},
		},
		{
			name:      "the primary's group is drained last",
			instances: []api.InstanceSpec{data("mongod", 1, 2), hidden("hid", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "hid", ordinal: 0},
			},
			primary: "sd-cr-rs0-0",
			want:    map[string]int32{"mongod": 1, "hid": 0},
		},
		{
			name:      "a primary on a high ordinal is stepped down first",
			instances: []api.InstanceSpec{data("mongod", 3, 2)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
			},
			primary:      "sd-cr-rs0-2",
			want:         map[string]int32{"mongod": 3},
			wantStepDown: "sd-cr-rs0-2",
		},
		{
			name:      "the step-down waits for terminating pods",
			instances: []api.InstanceSpec{data("mongod", 3, 2)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0},
				{group: "mongod", ordinal: 1, terminating: true},
				{group: "mongod", ordinal: 2},
			},
			primary:        "sd-cr-rs0-2",
			want:           map[string]int32{"mongod": 2},
			wantNoStepDown: true,
		},
		{
			name:      "a primary on the lowest ordinal lets the group shrink",
			instances: []api.InstanceSpec{data("mongod", 3, 2)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
			},
			primary: "sd-cr-rs0-0",
			want:    map[string]int32{"mongod": 2},
		},
		{
			name:      "the last member goes to zero",
			instances: []api.InstanceSpec{data("mongod", 1, 2)},
			pods:      []shutdownPod{{group: "mongod", ordinal: 0}},
			primary:   "sd-cr-rs0-0",
			want:      map[string]int32{"mongod": 0},
		},
		{
			name:      "no primary yet still drains one voter",
			instances: []api.InstanceSpec{data("mongod", 3, 2)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
			},
			primary: "",
			want:    map[string]int32{"mongod": 2},
		},
		{
			name:      "the last voter is held until a primary is elected",
			instances: []api.InstanceSpec{data("mongod", 1, 2)},
			pods:      []shutdownPod{{group: "mongod", ordinal: 0}},
			primary:   "",
			want:      map[string]int32{"mongod": 1},
		},
		{
			name:      "nothing ready takes it all down",
			instances: []api.InstanceSpec{data("mongod", 1, 2)},
			pods:      []shutdownPod{{group: "mongod", ordinal: 0, notReady: true}},
			primary:   "",
			want:      map[string]int32{"mongod": 0},
		},
		{
			name:      "a primary in a group that cannot be elected is still drained last",
			instances: []api.InstanceSpec{data("mongod", 1, 2), hidden("hot", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "hot", ordinal: 0},
			},
			primary: "sd-cr-rs0-hot-0",
			want:    map[string]int32{"hot": 1, "mongod": 0},
		},
		{
			name:      "a lagging config holds the drain instead of breaking quorum",
			instances: []api.InstanceSpec{data("mongod", 3, 2), arbiter("arb", 1), hidden("hid", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
			},
			primary:      "sd-cr-rs0-0",
			configVoters: 5,
			want:         map[string]int32{"mongod": 3, "arb": 0, "hid": 0},
		},
		{
			name:      "a lagging config waits for an election while quorum survives",
			instances: []api.InstanceSpec{data("mongod", 3, 2), arbiter("arb", 1), hidden("hid", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
			},
			primary:      "",
			configVoters: 5,
			want:         map[string]int32{"mongod": 3, "arb": 0, "hid": 0},
		},
		{
			name:      "an unreadable member does not disable the quorum check",
			instances: []api.InstanceSpec{data("mongod", 3, 2), arbiter("arb", 1), hidden("hid", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "mongod", ordinal: 1}, {group: "mongod", ordinal: 2},
			},
			primary:          "",
			configVoters:     5,
			configUnreadable: []string{"sd-cr-rs0-0"},
			want:             map[string]int32{"mongod": 3, "arb": 0, "hid": 0},
		},
		{
			name:      "an unrecoverable quorum shuts every group down",
			instances: []api.InstanceSpec{data("mongod", 3, 2), arbiter("arb", 1), hidden("hid", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0},
			},
			primary:      "",
			configVoters: 5,
			want:         map[string]int32{"mongod": 0, "arb": 0, "hid": 0},
		},
		{
			name:      "a config in step with the pods still drains one per pass",
			instances: []api.InstanceSpec{data("mongod", 1, 2), arbiter("arb", 1)},
			pods: []shutdownPod{
				{group: "mongod", ordinal: 0}, {group: "arb", ordinal: 0},
			},
			primary:      "sd-cr-rs0-0",
			configVoters: 2,
			want:         map[string]int32{"mongod": 1, "arb": 0},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cr := instanceCR(t, "sd-cr", "sd", tt.instances, unsafeSize)
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			objs := []client.Object{cr}
			// setPrimary reaches the replica set's pods through
			// GetOutdatedRSPods, which enumerates StatefulSets first. Without
			// them a step-down silently finds no candidates.
			for _, g := range set.GetAll() {
				objs = append(objs, groupSTS(cr, rs, g, g.Replicas, g.Replicas))
			}
			for _, p := range tt.pods {
				pod := groupPod(cr, rs, resolveGroup(t, cr, rs, p.group), p.ordinal)
				if p.notReady {
					pod = notReady(pod)
				}
				if p.terminating {
					pod = terminating(pod)
				}
				objs = append(objs, pod)
			}

			r := buildFakeClient(objs...)
			provider := newPrimaryProvider()
			if tt.primary != "" {
				provider = newPrimaryProvider(tt.primary)
			}
			provider.withConfigVoters(tt.configVoters).withUnreadableConfig(tt.configUnreadable...)
			r.mongoClientProvider = provider

			got, done, err := r.shutdownTarget(ctx, cr, rs, set)
			require.NoError(t, err)

			assert.Equal(t, tt.want, got, "target member counts")
			assert.Equal(t, tt.wantDone, done, "done")

			switch {
			case tt.wantStepDown != "":
				assert.Contains(t, provider.stepDowns, tt.wantStepDown,
					"the primary must be handed over before its group shrinks")
			case tt.wantNoStepDown:
				assert.Empty(t, provider.stepDowns,
					"no member may be stepped down while pods are still terminating")
			}
		})
	}
}

func TestApplyShutdownTarget(t *testing.T) {
	for _, tt := range []struct {
		name   string
		rs     *api.ReplsetSpec
		target map[string]int32
		assert func(t *testing.T, rs *api.ReplsetSpec)
	}{
		{
			name: "legacy, every role zeroed",
			rs: &api.ReplsetSpec{Name: "rs0", Size: new(int32(3)),
				Arbiter:   api.Arbiter{Enabled: true, Size: 1},
				NonVoting: api.NonVotingSpec{Enabled: true, Size: 2},
				Hidden:    api.HiddenSpec{Enabled: true, Size: 1}},
			target: map[string]int32{"mongod": 0, "arbiter": 0, "nonVoting": 0, "hidden": 0},
			assert: func(t *testing.T, rs *api.ReplsetSpec) {
				assert.Equal(t, int32(0), rs.GetMongodSize())
				assert.Equal(t, int32(0), rs.Arbiter.Size)
				assert.Equal(t, int32(0), rs.NonVoting.Size)
				assert.Equal(t, int32(0), rs.Hidden.Size)
			},
		},
		{
			name: "legacy, a partial target leaves the rest alone",
			rs: &api.ReplsetSpec{Name: "rs0", Size: new(int32(3)),
				Arbiter:   api.Arbiter{Enabled: true, Size: 1},
				NonVoting: api.NonVotingSpec{Enabled: true, Size: 2},
				Hidden:    api.HiddenSpec{Enabled: true, Size: 1}},
			target: map[string]int32{"mongod": 2},
			assert: func(t *testing.T, rs *api.ReplsetSpec) {
				assert.Equal(t, int32(2), rs.GetMongodSize())
				assert.Equal(t, int32(1), rs.Arbiter.Size)
				assert.Equal(t, int32(2), rs.NonVoting.Size)
				assert.Equal(t, int32(1), rs.Hidden.Size)
			},
		},
		{
			name: "instances",
			rs: &api.ReplsetSpec{Name: "rs0", Instances: []api.InstanceSpec{
				{Name: "hot", Replicas: 3}, {Name: "cold", Replicas: 1}}},
			target: map[string]int32{"hot": 1},
			assert: func(t *testing.T, rs *api.ReplsetSpec) {
				assert.Equal(t, int32(1), rs.Instance("hot").Replicas)
				assert.Equal(t, int32(1), rs.Instance("cold").Replicas)
			},
		},
		{
			name: "instances, an unknown group name is ignored",
			rs: &api.ReplsetSpec{Name: "rs0", Instances: []api.InstanceSpec{
				{Name: "hot", Replicas: 3}}},
			target: map[string]int32{"gone": 0},
			assert: func(t *testing.T, rs *api.ReplsetSpec) {
				assert.Equal(t, int32(3), rs.Instance("hot").Replicas)
			},
		},
		{
			name:   "legacy, an unknown group name is ignored",
			rs:     &api.ReplsetSpec{Name: "rs0", Size: new(int32(3))},
			target: map[string]int32{"hot": 0},
			assert: func(t *testing.T, rs *api.ReplsetSpec) {
				assert.Equal(t, int32(3), rs.GetMongodSize())
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := &ReconcilePerconaServerMongoDB{}
			r.applyShutdownTarget(tt.rs, tt.target)
			tt.assert(t, tt.rs)
		})
	}
}

func TestDeleteReplset(t *testing.T) {
	instances := []api.InstanceSpec{
		{Name: "mongod", Replicas: 3, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Votes: new(int32(1)), Priority: new(int32(2))}},
		{Name: "ro", Replicas: 2, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Votes: new(int32(0)), Priority: new(int32(0))}},
	}

	t.Run("pods remain", func(t *testing.T) {
		ctx := t.Context()

		cr := instanceCR(t, "sd-cr", "sd", instances, unsafeSize)
		rs := cr.Spec.Replsets[0]
		set, err := membergroup.Resolve(cr, rs)
		require.NoError(t, err)

		objs := []client.Object{cr}
		for _, g := range set.GetAll() {
			objs = append(objs, groupSTS(cr, rs, g, g.Replicas, g.Replicas))
			for i := range int(g.Replicas) {
				objs = append(objs, groupPod(cr, rs, g, i))
			}
		}

		r := buildFakeClient(objs...)
		r.mongoClientProvider = newPrimaryProvider("sd-cr-rs0-0")

		err = r.deleteReplset(ctx, cr, rs)
		require.ErrorIs(t, err, errWaitingTermination)

		// The target has to reach the spec, or the StatefulSet never shrinks
		// and the delete makes no progress.
		assert.Equal(t, int32(0), rs.Instance("ro").Replicas, "non-voting drains at once")
		assert.Equal(t, int32(2), rs.Instance("mongod").Replicas, "the primary's group sheds one member")
	})

	t.Run("no pods left", func(t *testing.T) {
		ctx := t.Context()

		cr := instanceCR(t, "sd-cr", "sd", instances, unsafeSize)
		rs := cr.Spec.Replsets[0]

		r := buildFakeClient(cr)
		r.mongoClientProvider = newPrimaryProvider()

		require.NoError(t, r.deleteReplset(ctx, cr, rs))

		assert.Equal(t, int32(0), rs.Instance("mongod").Replicas)
		assert.Equal(t, int32(0), rs.Instance("ro").Replicas)
	})
}
