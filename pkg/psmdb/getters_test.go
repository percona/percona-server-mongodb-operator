package psmdb

import (
	"maps"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

func gettersCR(replsets ...*api.ReplsetSpec) *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.24.0",
			Replsets:  replsets,
		},
	}
}

func gettersVol() *api.VolumeSpec {
	return &api.VolumeSpec{PersistentVolumeClaim: api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{},
	}}
}

// workload is one observed StatefulSet plus the pods that exist for it. The
// pod count is deliberately independent of the group's declared replicas: the
// gap between them is what truncation exists to handle.
type workload struct {
	component string
	pods      int
}

// buildObjects renders the workloads as the objects a live cluster would hold.
// Pods carry the pod-index label because util.SortPodsByOrdinalAsc reads it,
// and the StatefulSet carries the replset and component labels getRSPods
// selects on.
func buildObjects(cr *api.PerconaServerMongoDB, rsName string, workloads []workload) []client.Object {
	rsLabels := map[string]string{
		naming.LabelKubernetesName:      "percona-server-mongodb",
		naming.LabelKubernetesInstance:  cr.Name,
		naming.LabelKubernetesReplset:   rsName,
		naming.LabelKubernetesManagedBy: "percona-server-mongodb-operator",
		naming.LabelKubernetesPartOf:    "percona-server-mongodb",
	}

	objs := make([]client.Object, 0, len(workloads))
	for _, w := range workloads {
		ls := maps.Clone(rsLabels)
		ls[naming.LabelKubernetesComponent] = w.component

		stsName := cr.Name + "-" + rsName
		if w.component != naming.ComponentMongod {
			stsName += "-" + w.component
		}

		objs = append(objs, &appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: cr.Namespace, Labels: ls},
		})

		for i := range w.pods {
			podLabels := maps.Clone(ls)
			podLabels[appsv1.PodIndexLabel] = strconv.Itoa(i)

			objs = append(objs, &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      stsName + "-" + strconv.Itoa(i),
					Namespace: cr.Namespace,
					Labels:    podLabels,
				},
			})
		}
	}
	return objs
}

func podNames(list corev1.PodList) []string {
	out := make([]string, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].Name)
	}
	return out
}

func TestGetRSPods(t *testing.T) {
	tests := map[string]struct {
		rs           *api.ReplsetSpec
		workloads    []workload
		rsName       string
		wantDesired  []string
		wantOutdated []string
	}{
		"legacy in a steady state": {
			rs:           &api.ReplsetSpec{Name: "rs0", Size: new(int32(3)), VolumeSpec: gettersVol()},
			workloads:    []workload{{naming.ComponentMongod, 3}},
			wantDesired:  []string{"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2"},
			wantOutdated: []string{"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2"},
		},
		"legacy mid-downscale drops the surplus": {
			// Five pods still exist but three are wanted. The two on their way
			// out must not be reconfigured back into the replica set.
			rs:          &api.ReplsetSpec{Name: "rs0", Size: new(int32(3)), VolumeSpec: gettersVol()},
			workloads:   []workload{{naming.ComponentMongod, 5}},
			wantDesired: []string{"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2"},
			wantOutdated: []string{
				"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2",
				"cluster1-rs0-3", "cluster1-rs0-4",
			},
		},
		"ordinals are compared numerically, not lexically": {
			// The rewrite replaced "sort by name, slice to size" with an
			// ordinal parse. Lexically, "-10" sorts between "-1" and "-2", so
			// the old code would have kept pod 10 and dropped pod 2.
			rs:        &api.ReplsetSpec{Name: "rs0", Size: new(int32(10)), VolumeSpec: gettersVol()},
			workloads: []workload{{naming.ComponentMongod, 11}},
			wantDesired: []string{
				"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2", "cluster1-rs0-3",
				"cluster1-rs0-4", "cluster1-rs0-5", "cluster1-rs0-6", "cluster1-rs0-7",
				"cluster1-rs0-8", "cluster1-rs0-9",
			},
			wantOutdated: []string{
				"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2", "cluster1-rs0-3",
				"cluster1-rs0-4", "cluster1-rs0-5", "cluster1-rs0-6", "cluster1-rs0-7",
				"cluster1-rs0-8", "cluster1-rs0-9", "cluster1-rs0-10",
			},
		},
		"legacy roles are truncated against their own size": {
			rs: &api.ReplsetSpec{
				Name: "rs0", Size: new(int32(2)), VolumeSpec: gettersVol(),
				NonVoting: api.NonVotingSpec{Enabled: true, Size: 1, VolumeSpec: gettersVol()},
				Arbiter:   api.Arbiter{Enabled: true, Size: 1},
			},
			workloads: []workload{
				{naming.ComponentMongod, 3},
				{naming.ComponentNonVoting, 2},
				{naming.ComponentArbiter, 1},
			},
			// StatefulSets are iterated in name order, and the base group's
			// name is a prefix of every other, so it always comes first:
			// cluster1-rs0, then -arbiter, then -nonVoting.
			wantDesired: []string{
				"cluster1-rs0-0", "cluster1-rs0-1",
				"cluster1-rs0-arbiter-0",
				"cluster1-rs0-nonVoting-0",
			},
			wantOutdated: []string{
				"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2",
				"cluster1-rs0-arbiter-0",
				"cluster1-rs0-nonVoting-0", "cluster1-rs0-nonVoting-1",
			},
		},
		"each instance group is truncated against its own replicas": {
			rs: &api.ReplsetSpec{Name: "rs0", Instances: []api.InstanceSpec{
				{Name: "hot", Replicas: 2, VolumeSpec: gettersVol()},
				{Name: "cold", Replicas: 1, VolumeSpec: gettersVol()},
			}},
			workloads: []workload{{"hot", 3}, {"cold", 2}},
			wantDesired: []string{
				"cluster1-rs0-cold-0",
				"cluster1-rs0-hot-0", "cluster1-rs0-hot-1",
			},
			wantOutdated: []string{
				"cluster1-rs0-cold-0", "cluster1-rs0-cold-1",
				"cluster1-rs0-hot-0", "cluster1-rs0-hot-1", "cluster1-rs0-hot-2",
			},
		},
		"a retired group contributes no desired members": {
			// "cold" was removed from instances[] but its workload is still
			// draining. Before the group lookup this fell through to the base
			// group's replica count, which would have fed a retiring pod into
			// replSetReconfig as if it were a base member.
			rs: &api.ReplsetSpec{Name: "rs0", Instances: []api.InstanceSpec{
				{Name: "hot", Replicas: 2, VolumeSpec: gettersVol()},
			}},
			workloads:   []workload{{"hot", 2}, {"cold", 2}},
			wantDesired: []string{"cluster1-rs0-hot-0", "cluster1-rs0-hot-1"},
			wantOutdated: []string{
				"cluster1-rs0-cold-0", "cluster1-rs0-cold-1",
				"cluster1-rs0-hot-0", "cluster1-rs0-hot-1",
			},
		},
		"a group scaled to zero keeps its pods out of the desired set": {
			rs: &api.ReplsetSpec{Name: "rs0", Instances: []api.InstanceSpec{
				{Name: "hot", Replicas: 2, VolumeSpec: gettersVol()},
				{Name: "cold", Replicas: 0, VolumeSpec: gettersVol()},
			}},
			workloads:   []workload{{"hot", 2}, {"cold", 1}},
			wantDesired: []string{"cluster1-rs0-hot-0", "cluster1-rs0-hot-1"},
			wantOutdated: []string{
				"cluster1-rs0-cold-0",
				"cluster1-rs0-hot-0", "cluster1-rs0-hot-1",
			},
		},
		"search workloads are excluded from both views": {
			// Search is not a replica set member. Including it would put a pod
			// that runs no mongod into the member list.
			rs:        &api.ReplsetSpec{Name: "rs0", Size: new(int32(2)), VolumeSpec: gettersVol()},
			workloads: []workload{{naming.ComponentMongod, 2}, {naming.ComponentSearch, 2}},
			wantDesired: []string{
				"cluster1-rs0-0", "cluster1-rs0-1",
			},
			wantOutdated: []string{
				"cluster1-rs0-0", "cluster1-rs0-1",
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := gettersCR(tt.rs)
			cl := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
				WithObjects(buildObjects(cr, tt.rs.Name, tt.workloads)...).
				Build()

			desired, err := GetRSPods(t.Context(), cl, cr, tt.rs.Name)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDesired, podNames(desired), "GetRSPods")

			outdated, err := GetOutdatedRSPods(t.Context(), cl, cr, tt.rs.Name)
			require.NoError(t, err)
			assert.Equal(t, tt.wantOutdated, podNames(outdated), "GetOutdatedRSPods")
		})
	}
}

func TestGetRSPodsForUndeclaredReplset(t *testing.T) {
	rs0 := &api.ReplsetSpec{Name: "rs0", Size: new(int32(2)), VolumeSpec: gettersVol()}
	cr := gettersCR(rs0)

	objs := buildObjects(cr, "rs0", []workload{{naming.ComponentMongod, 2}})
	objs = append(objs, buildObjects(cr, "rs1", []workload{{naming.ComponentMongod, 2}})...)

	cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objs...).Build()

	desired, err := GetRSPods(t.Context(), cl, cr, "rs1")
	require.NoError(t, err)
	assert.Empty(t, podNames(desired),
		"an undeclared replica set desires no members")

	outdated, err := GetOutdatedRSPods(t.Context(), cl, cr, "rs1")
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"cluster1-rs0-0", "cluster1-rs1-0", "cluster1-rs0-1", "cluster1-rs1-1"},
		podNames(outdated),
		"rs0's pods leak into rs1's view and interleave by ordinal, because the "+
			"selector loses the replset label")
}

func TestSelectDesiredOrdinals(t *testing.T) {
	pod := func(name string) corev1.Pod {
		return corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name}}
	}

	all := []corev1.Pod{
		pod("cluster1-rs0-0"),
		pod("cluster1-rs0-1"),
		pod("cluster1-rs0-2"),
		pod("cluster1-rs0-10"),
	}

	tests := map[string]struct {
		pods     []corev1.Pod
		replicas int32
		want     []string
	}{
		"zero keeps nothing": {
			pods: all, replicas: 0, want: []string{},
		},
		"one keeps the lowest ordinal": {
			pods: all, replicas: 1, want: []string{"cluster1-rs0-0"},
		},
		"three keeps ordinals 0 to 2": {
			pods: all, replicas: 3,
			want: []string{"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2"},
		},
		"eleven keeps the double-digit ordinal too": {
			pods: all, replicas: 11,
			want: []string{"cluster1-rs0-0", "cluster1-rs0-1", "cluster1-rs0-2", "cluster1-rs0-10"},
		},
		"a name with no ordinal is dropped": {
			pods: []corev1.Pod{pod("cluster1-rs0"), pod("cluster1-rs0-0")}, replicas: 3,
			want: []string{"cluster1-rs0-0"},
		},
		"a non-numeric suffix is dropped": {
			pods: []corev1.Pod{pod("cluster1-rs0-abc"), pod("cluster1-rs0-0")}, replicas: 3,
			want: []string{"cluster1-rs0-0"},
		},
		"input order is preserved, not sorted": {
			// The caller sorts before filtering; this must not reorder.
			pods: []corev1.Pod{pod("cluster1-rs0-2"), pod("cluster1-rs0-0")}, replicas: 3,
			want: []string{"cluster1-rs0-2", "cluster1-rs0-0"},
		},
		"no pods": {
			pods: nil, replicas: 3, want: []string{},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got := selectDesiredOrdinals(tt.pods, tt.replicas)

			names := make([]string, 0, len(got))
			for i := range got {
				names = append(names, got[i].Name)
			}
			assert.Equal(t, tt.want, names)
		})
	}
}

func TestGetGroupPods(t *testing.T) {
	rs := &api.ReplsetSpec{Name: "rs0", Instances: []api.InstanceSpec{
		{Name: "hot", Replicas: 2, VolumeSpec: gettersVol()},
		{Name: "cold", Replicas: 1, VolumeSpec: gettersVol()},
	}}
	cr := gettersCR(rs)

	objs := buildObjects(cr, "rs0", []workload{{"hot", 3}, {"cold", 1}})
	objs = append(objs, buildObjects(cr, "rs1", []workload{{"hot", 2}})...)

	cl := fake.NewClientBuilder().WithScheme(scheme.Scheme).WithObjects(objs...).Build()

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	hot, ok := set.GetByName("hot")
	require.True(t, ok)

	pods, err := GetGroupPods(t.Context(), cl, cr, rs, hot)
	require.NoError(t, err)
	assert.Equal(t,
		[]string{"cluster1-rs0-hot-0", "cluster1-rs0-hot-1", "cluster1-rs0-hot-2"},
		podNames(pods),
		"every observed pod is returned, including the one above the declared replicas")

	cold, ok := set.GetByName("cold")
	require.True(t, ok)

	pods, err = GetGroupPods(t.Context(), cl, cr, rs, cold)
	require.NoError(t, err)
	assert.Equal(t, []string{"cluster1-rs0-cold-0"}, podNames(pods),
		"a sibling group's pods are not returned")
}
