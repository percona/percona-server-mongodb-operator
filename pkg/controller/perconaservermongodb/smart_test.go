package perconaservermongodb

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestReplsetHasUnavailableVoters(t *testing.T) {
	t.Parallel()

	arbiterInstance := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, RSConfig: &api.MemberConfigSpec{
			ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}}
	}
	hiddenInstance := func(name string, replicas int32) api.InstanceSpec {
		return api.InstanceSpec{Name: name, Replicas: replicas, VolumeSpec: memberVol(),
			RSConfig: &api.MemberConfigSpec{Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0))}}
	}

	for _, tt := range []struct {
		name      string
		instances []api.InstanceSpec
		// ready maps a group name to its StatefulSet's ReadyReplicas. A group
		// absent from the map gets no StatefulSet at all.
		ready map[string]int32
		want  bool
	}{
		{
			// Non-voting members cannot cost quorum, so their readiness is not
			// consulted at all.
			name:      "every voter ready, a non-voting group down",
			instances: []api.InstanceSpec{voting("mongod", 3), nonVotingInst("nv", 2)},
			ready:     map[string]int32{"mongod": 3, "nv": 0},
			want:      false,
		},
		{
			name:      "a voter in the group being updated is not ready",
			instances: []api.InstanceSpec{voting("mongod", 3)},
			ready:     map[string]int32{"mongod": 2},
			want:      true,
		},
		{
			name:      "a voter in a sibling group is not ready",
			instances: []api.InstanceSpec{voting("mongod", 3), hiddenInstance("hid", 1)},
			ready:     map[string]int32{"mongod": 3, "hid": 0},
			want:      true,
		},
		{
			name:      "an arbiter is not ready",
			instances: []api.InstanceSpec{voting("mongod", 3), arbiterInstance("arb", 1)},
			ready:     map[string]int32{"mongod": 3, "arb": 0},
			want:      true,
		},
		{
			name:      "a declared voting group has no workload yet",
			instances: []api.InstanceSpec{voting("mongod", 3), voting("hot", 3)},
			ready:     map[string]int32{"mongod": 3},
			want:      true,
		},
		{
			name:      "a voting group scaled to zero is skipped",
			instances: []api.InstanceSpec{voting("mongod", 3), voting("hot", 0)},
			ready:     map[string]int32{"mongod": 3},
			want:      false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cr := instanceCR(t, "sm-cr", "sm", tt.instances, unsafeSize)
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			objs := []client.Object{cr}
			for name, ready := range tt.ready {
				g := resolveGroup(t, cr, rs, name)
				objs = append(objs, groupSTS(cr, rs, g, g.Replicas, ready))
			}
			r := buildFakeClient(objs...)

			got, err := r.replsetHasUnavailableVoters(ctx, cr, set)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsArbiterPod covers the check that keeps arbiters out of freeze and
// step-down. An arbiter cannot even be connected to as clusterAdmin, so getting
// this wrong turns a routine update into an error.
func TestIsArbiterPod(t *testing.T) {
	t.Parallel()

	instances := []api.InstanceSpec{
		voting("mongod", 3),
		{Name: "arb", Replicas: 1, RSConfig: &api.MemberConfigSpec{
			ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}},
	}

	for _, tt := range []struct {
		name string
		// group to build the pod from, or "" to build an orphan
		group string
		// container is used only for the orphan rows
		container string
		want      bool
	}{
		{name: "a pod of an arbiter group", group: "arb", want: true},
		{name: "a pod of a data-bearing group", group: "mongod", want: false},
		{
			// A group deleted from the CR leaves pods behind that resolve to no
			// group. They must still not be frozen or stepped down.
			name: "an orphan pod running the arbiter container", container: naming.ContainerArbiter, want: true,
		},
		{
			name: "an orphan pod running mongod", container: naming.ContainerMongod, want: false,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cr := instanceCR(t, "sm-cr", "sm", instances, unsafeSize)
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			var pod *corev1.Pod
			if tt.group != "" {
				pod = groupPod(cr, rs, resolveGroup(t, cr, rs, tt.group), 0)
			} else {
				pod = groupPod(cr, rs, resolveGroup(t, cr, rs, "mongod"), 0)
				pod.Labels[naming.LabelKubernetesComponent] = "gone"
				pod.Spec.Containers[0].Name = tt.container
			}

			assert.Equal(t, tt.want, isArbiterPod(pod, set))
		})
	}
}
func TestIsMemberContainer(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]bool{
		naming.ContainerMongod:      true,
		naming.ContainerMongos:      true,
		naming.ContainerNonVoting:   true,
		naming.ContainerArbiter:     true,
		naming.ContainerHidden:      true,
		naming.ContainerBackupAgent: false,
		"pmm-client":                false,
		"":                          false,
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, want, isMemberContainer(name))
		})
	}
}

func TestSetPrimary(t *testing.T) {
	t.Parallel()

	instances := []api.InstanceSpec{
		voting("mongod", 3),
		{Name: "hid", Replicas: 1, VolumeSpec: memberVol(), RSConfig: &api.MemberConfigSpec{
			Hidden: new(true), Votes: new(int32(1)), Priority: new(int32(0))}},
		{Name: "arb", Replicas: 1, RSConfig: &api.MemberConfigSpec{
			ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}},
	}

	for _, tt := range []struct {
		name string
		// primaries are the pods reporting isMaster
		primaries []string
		// expected is the pod the primary should end up on
		expected      string
		wantStepDown  []string
		wantFreezes   []string
		wantNoFreezes bool
	}{
		{
			// Nothing to do, and nothing may be disturbed.
			name:          "the expected pod is already primary",
			primaries:     []string{"sm-cr-rs0-0"},
			expected:      "sm-cr-rs0-0",
			wantNoFreezes: true,
		},
		{
			name:         "every other electable member is frozen, then the primary steps down",
			primaries:    []string{"sm-cr-rs0-1"},
			expected:     "sm-cr-rs0-0",
			wantStepDown: []string{"sm-cr-rs0-1"},
			// neither the expected primary nor the current one, and never the arbiter
			wantFreezes: []string{"sm-cr-rs0-2", "sm-cr-rs0-hid-0"},
		},
		{
			name:         "the primary is found in another group",
			primaries:    []string{"sm-cr-rs0-hid-0"},
			expected:     "sm-cr-rs0-0",
			wantStepDown: []string{"sm-cr-rs0-hid-0"},
			wantFreezes:  []string{"sm-cr-rs0-1", "sm-cr-rs0-2"},
		},
		{
			name:          "no primary is observed",
			primaries:     nil,
			expected:      "sm-cr-rs0-0",
			wantNoFreezes: true,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			cr := instanceCR(t, "sm-cr", "sm", instances, unsafeSize)
			rs := cr.Spec.Replsets[0]

			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			objs := []client.Object{cr}
			var expectedPod *corev1.Pod
			for _, g := range set.GetAll() {
				objs = append(objs, groupSTS(cr, rs, g, g.Replicas, g.Replicas))
				for i := range int(g.Replicas) {
					pod := groupPod(cr, rs, g, i)
					if pod.Name == tt.expected {
						expectedPod = pod
					}
					objs = append(objs, pod)
				}
			}
			require.NotNil(t, expectedPod, "fixture must contain the expected primary")

			r := buildFakeClient(objs...)
			provider := newPrimaryProvider(tt.primaries...)
			r.mongoClientProvider = provider

			require.NoError(t, r.setPrimary(ctx, cr, rs, set, *expectedPod))

			if tt.wantNoFreezes {
				assert.Empty(t, provider.freezes, "nothing may be frozen")
				assert.Empty(t, provider.stepDowns, "nothing may be stepped down")
				return
			}

			assert.ElementsMatch(t, tt.wantFreezes, provider.freezes)
			assert.Equal(t, tt.wantStepDown, provider.stepDowns)
			assert.NotContains(t, provider.freezes, "sm-cr-rs0-arb-0",
				"an arbiter can be neither frozen nor stepped down")
		})
	}
}

func TestSmartMongosUpdateUnsetsMongosFirst(t *testing.T) {
	tests := map[string]func(cr *api.PerconaServerMongoDB){
		"mongos is scaled to zero": func(cr *api.PerconaServerMongoDB) {
			cr.Spec.Sharding.Mongos.Size = 0
		},
		"update strategy is not smart": func(cr *api.PerconaServerMongoDB) {
			cr.Spec.UpdateStrategy = appsv1.RollingUpdateStatefulSetStrategyType
		},
		// e.g. mongos was created after the TLS secrets were recreated
		"mongos statefulset has no changes": func(cr *api.PerconaServerMongoDB) {},
	}

	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newTestCR()
			cr.Annotations = map[string]string{api.AnnotationUpdateMongosFirst: "true"}
			cr.Spec.UpdateStrategy = api.SmartUpdateStatefulSetStrategyType
			cr.Spec.Sharding = api.Sharding{
				Enabled: true,
				Mongos:  &api.MongosSpec{Size: 2},
			}
			mutate(cr)

			r := buildFakeClient(cr)

			require.NoError(t, r.smartMongosUpdate(t.Context(), cr, &appsv1.StatefulSet{}))

			stored := new(api.PerconaServerMongoDB)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), stored))
			assert.NotContains(t, stored.Annotations, api.AnnotationUpdateMongosFirst)
			assert.NotContains(t, cr.Annotations, api.AnnotationUpdateMongosFirst)
		})
	}
}

func TestSmartUpdateWaitsForReplsetMembers(t *testing.T) {
	ctx := context.Background()

	rs := &api.ReplsetSpec{Name: "rs0", Size: new(int32(3)), LivenessProbe: new(api.LivenessProbeExtended)}
	cr := &api.PerconaServerMongoDB{
		Name:      "some-name",
		Namespace: "some-namespace",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion:      version.Version(),
			UpdateStrategy: api.SmartUpdateStatefulSetStrategyType,
			Replsets:       []*api.ReplsetSpec{rs},
		},
		Status: api.PerconaServerMongoDBStatus{
			Replsets: map[string]api.ReplsetStatus{
				// Members are dropped from the stored status when the replset
				// reconciliation returns none, e.g. while waiting to be added as a shard.
				"rs0": {Initialized: true},
			},
		},
	}

	sts := mockReadyReplsetSts("some-name-rs0", cr.Namespace, cr.Name, rs.Name, "mongod", 3)
	sts.Status.Replicas = 3
	sts.Status.UpdateRevision = "new-revision"

	objs := []client.Object{cr, sts}
	for i := range 3 {
		pod := mockReadyReplsetPod("some-name-rs0-"+strconv.Itoa(i), cr.Namespace, cr.Name, rs.Name, "mongod")
		pod.Labels["controller-revision-hash"] = "old-revision"
		objs = append(objs, pod)
	}

	r := buildFakeClient(objs...)

	// Pods that haven't joined the replset have no users, so checking them
	// for primary would fail authentication and block the reconcile.
	set, err := membergroup.Resolve(cr, rs)
	if err != nil {
		t.Fatalf("resolve member groups: %v", err)
	}
	group, ok := set.GetByName(naming.GroupMongod)
	if !ok {
		t.Fatal("mongod group not resolved")
	}
	if err := r.smartUpdate(ctx, cr, sts, rs, group); err != nil {
		t.Fatalf("smart update should wait for replset members, got error: %v", err)
	}
}
