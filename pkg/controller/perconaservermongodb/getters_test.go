package perconaservermongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

func ownedBy(cr *api.PerconaServerMongoDB) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: api.SchemeGroupVersion.String(),
		Kind:       "PerconaServerMongoDB",
		Name:       cr.Name,
		UID:        cr.UID,
		Controller: new(true),
	}
}

func labelledSTS(cr *api.PerconaServerMongoDB, name string, ls map[string]string, owned bool) *appsv1.StatefulSet {
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cr.Namespace, Labels: ls},
	}
	if owned {
		sts.OwnerReferences = []metav1.OwnerReference{ownedBy(cr)}
	}
	return sts
}

func labelledPod(cr *api.PerconaServerMongoDB, name string, ls map[string]string) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cr.Namespace, Labels: ls}}
}

func labelledPVC(cr *api.PerconaServerMongoDB, name string, ls map[string]string) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cr.Namespace, Labels: ls},
	}
}

func stsNames(list appsv1.StatefulSetList) []string {
	out := make([]string, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].Name)
	}
	return out
}

func podNames(list corev1.PodList) []string {
	out := make([]string, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].Name)
	}
	return out
}

func pvcNames(list corev1.PersistentVolumeClaimList) []string {
	out := make([]string, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, list.Items[i].Name)
	}
	return out
}

func TestGetMemberStatefulsets(t *testing.T) {
	ctx := t.Context()

	instances := []api.InstanceSpec{voting("mongod", 3), voting("analytics", 2)}
	cr := instanceCR(t, "gt-cr", "gt", instances, unsafeSize)
	rs := cr.Spec.Replsets[0]

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	objs := []client.Object{cr}
	for _, g := range set.GetAll() {
		sts := groupSTS(cr, rs, g, g.Replicas, g.Replicas)
		objs = append(objs, sts)
	}
	objs = append(objs,
		labelledSTS(cr, "gt-cr-mongos", naming.MongosLabels(cr), true),
		labelledSTS(cr, "gt-cr-rs0-search", naming.SearchLabels(cr, rs), true),
		// same labels as a real member workload, but nobody's child
		labelledSTS(cr, "gt-cr-rs0-impostor", naming.RSLabels(cr, rs), false),
	)

	r := buildFakeClient(objs...)

	got, err := r.getMemberStatefulsets(ctx, cr, rs)
	require.NoError(t, err)

	assert.Equal(t, []string{"gt-cr-rs0", "gt-cr-rs0-analytics"}, stsNames(got),
		"member groups only, sorted by name")
}

func TestGetShardsWithWorkloads(t *testing.T) {
	ctx := t.Context()

	cr := legacyCR(t, "gt-cr", "gt", func(c *api.PerconaServerMongoDB) {
		c.Spec.Sharding.Enabled = true
		// rs1 is declared before rs0 so the returned order cannot come from
		// the declaration order by accident.
		second := c.Spec.Replsets[0].DeepCopy()
		second.Name = "rs1"
		c.Spec.Replsets = []*api.ReplsetSpec{second, c.Spec.Replsets[0]}
		// several member groups per replica set: each is its own StatefulSet
		// carrying the same replset label, so the name has to be deduplicated.
		for _, rs := range c.Spec.Replsets {
			rs.Hidden = api.HiddenSpec{Enabled: true, Size: 1}
			rs.NonVoting = api.NonVotingSpec{Enabled: true, Size: 1}
		}
	})
	cfg := cr.Spec.Sharding.ConfigsvrReplSet

	objs := []client.Object{cr}
	for _, rs := range cr.GetAllReplsets() {
		set, err := membergroup.Resolve(cr, rs)
		require.NoError(t, err)
		if rs.ClusterRole != api.ClusterRoleConfigSvr {
			require.Greater(t, set.Len(), 1, "a shard needs several groups for the dedup to mean anything")
		}

		for _, g := range set.GetAll() {
			objs = append(objs, groupSTS(cr, rs, g, g.Replicas, g.Replicas))
		}
	}
	objs = append(objs,
		labelledSTS(cr, "gt-cr-mongos", naming.MongosLabels(cr), true),
		labelledSTS(cr, "gt-cr-rs0-search", naming.SearchLabels(cr, cr.Spec.Replsets[1]), true),
		labelledSTS(cr, "gt-cr-rs9-impostor", naming.RSLabels(cr, &api.ReplsetSpec{Name: "rs9"}), false),
	)
	require.NotNil(t, cfg)

	r := buildFakeClient(objs...)

	got, err := r.getShardsWithWorkloads(ctx, cr)
	require.NoError(t, err)

	assert.Equal(t, []string{"rs0", "rs1"}, got,
		"deduplicated and sorted; no mongos, no search, no config server, nothing unowned")
}

func TestGetEligibleMemberPod(t *testing.T) {
	ctx := t.Context()

	// analytics sorts before mongod, so iteration order is observable.
	instances := []api.InstanceSpec{voting("mongod", 2), voting("analytics", 2), voting("empty", 0)}
	cr := instanceCR(t, "gt-cr", "gt", instances, unsafeSize)
	rs := cr.Spec.Replsets[0]

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	objs := []client.Object{cr}
	for _, g := range set.GetAll() {
		for i := range 3 { // one pod above each group's declared count
			objs = append(objs, groupPod(cr, rs, g, i))
		}
	}
	r := buildFakeClient(objs...)

	always := func(membergroup.Group, *corev1.Pod) bool { return true }

	t.Run("groups are walked in set order", func(t *testing.T) {
		pod, group, err := r.getEligibleMemberPod(ctx, cr, rs, set, always)
		require.NoError(t, err)
		assert.Equal(t, "analytics", group.Name)
		assert.Equal(t, "gt-cr-rs0-analytics-0", pod.Name)
	})

	t.Run("the predicate decides", func(t *testing.T) {
		pod, group, err := r.getEligibleMemberPod(ctx, cr, rs, set,
			func(g membergroup.Group, _ *corev1.Pod) bool { return g.Name == "mongod" })
		require.NoError(t, err)
		assert.Equal(t, "mongod", group.Name)
		assert.Equal(t, "gt-cr-rs0-0", pod.Name)
	})

	t.Run("a group scaled to zero yields nothing", func(t *testing.T) {
		_, _, err := r.getEligibleMemberPod(ctx, cr, rs, set,
			func(g membergroup.Group, _ *corev1.Pod) bool { return g.Name == "empty" })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no eligible member pod")
	})

	t.Run("pods above the declared count are skipped", func(t *testing.T) {
		_, _, err := r.getEligibleMemberPod(ctx, cr, rs, set,
			func(_ membergroup.Group, p *corev1.Pod) bool {
				return p.Name == "gt-cr-rs0-2" || p.Name == "gt-cr-rs0-analytics-2"
			})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no eligible member pod")
	})

	t.Run("nothing matches", func(t *testing.T) {
		_, _, err := r.getEligibleMemberPod(ctx, cr, rs, set,
			func(membergroup.Group, *corev1.Pod) bool { return false })
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no eligible member pod")
	})
}

func TestIsReadyDataBearingPod(t *testing.T) {
	instances := []api.InstanceSpec{
		voting("mongod", 1),
		{Name: "arb", Replicas: 1, RSConfig: &api.MemberConfigSpec{
			ArbiterOnly: new(true), Votes: new(int32(1)), Priority: new(int32(0))}},
	}
	cr := instanceCR(t, "gt-cr", "gt", instances, unsafeSize)
	rs := cr.Spec.Replsets[0]

	mongod := resolveGroup(t, cr, rs, "mongod")
	arb := resolveGroup(t, cr, rs, "arb")

	t.Run("a ready data-bearing pod", func(t *testing.T) {
		assert.True(t, IsReadyDataBearingPod(mongod, groupPod(cr, rs, mongod, 0)))
	})

	t.Run("an arbiter is never eligible, ready or not", func(t *testing.T) {
		assert.False(t, IsReadyDataBearingPod(arb, groupPod(cr, rs, arb, 0)))
	})

	t.Run("a pod that is not ready", func(t *testing.T) {
		assert.False(t, IsReadyDataBearingPod(mongod, notReady(groupPod(cr, rs, mongod, 0))))
	})

	t.Run("the group's container is not the one running", func(t *testing.T) {
		pod := groupPod(cr, rs, mongod, 0)
		pod.Status.ContainerStatuses[0].Name = "something-else"
		assert.False(t, IsReadyDataBearingPod(mongod, pod))
	})
}

func TestGetMemberPodsAndPVCs(t *testing.T) {
	ctx := t.Context()

	cr := legacyCR(t, "gt-cr", "gt", func(c *api.PerconaServerMongoDB) {
		c.Spec.Sharding.Enabled = true
	})
	rs := cr.Spec.Replsets[0]
	cfg := cr.Spec.Sharding.ConfigsvrReplSet

	objs := []client.Object{
		labelledPod(cr, "gt-cr-rs0-0", naming.RSLabels(cr, rs)),
		labelledPod(cr, "gt-cr-cfg-0", naming.RSLabels(cr, cfg)),
		labelledPod(cr, "gt-cr-mongos-0", naming.MongosLabels(cr)),
		labelledPod(cr, "gt-cr-rs0-search-0", naming.SearchLabels(cr, rs)),
		// cluster label but no replset label: not a member workload
		labelledPod(cr, "gt-cr-stray", naming.ClusterLabels(cr)),

		labelledPVC(cr, "mongod-data-gt-cr-rs0-0", naming.RSLabels(cr, rs)),
		labelledPVC(cr, "mongod-data-gt-cr-cfg-0", naming.RSLabels(cr, cfg)),
		labelledPVC(cr, "data-gt-cr-rs0-search-0", naming.SearchLabels(cr, rs)),
	}

	r := buildFakeClient(append(objs, cr)...)

	pods, err := r.getMemberPods(ctx, cr)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"gt-cr-rs0-0", "gt-cr-cfg-0"}, podNames(pods))

	pvcs, err := r.getMemberPVCs(ctx, cr)
	require.NoError(t, err)
	assert.ElementsMatch(t,
		[]string{"mongod-data-gt-cr-rs0-0", "mongod-data-gt-cr-cfg-0"}, pvcNames(pvcs))
}
