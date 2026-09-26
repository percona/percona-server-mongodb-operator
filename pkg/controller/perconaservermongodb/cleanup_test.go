package perconaservermongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

// stsState reports a StatefulSet's spec replicas, or -1 if it is gone.
func stsState(t *testing.T, r *ReconcilePerconaServerMongoDB, ns, name string) int32 {
	t.Helper()

	sts := new(appsv1.StatefulSet)
	err := r.client.Get(t.Context(), client.ObjectKey{Name: name, Namespace: ns}, sts)
	if k8serrors.IsNotFound(err) {
		return -1
	}
	require.NoError(t, err)
	if sts.Spec.Replicas == nil {
		return 0
	}
	return *sts.Spec.Replicas
}

func TestCleanupRemovedInstances(t *testing.T) {
	const gone = "sd-cr-rs0-gone"

	for _, tt := range []struct {
		name string
		// specReplicas and statusReplicas describe the retiring workload
		specReplicas   int32
		statusReplicas int32
		want           int32 // -1 means deleted
	}{
		{
			// Still running: shed one member, do not delete.
			name:         "a retired group is scaled down, not deleted",
			specReplicas: 3, statusReplicas: 3, want: 2,
		},
		{
			// Deleting now would orphan the pods that are still terminating.
			name:         "a retired group mid-termination is left alone",
			specReplicas: 0, statusReplicas: 2, want: 0,
		},
		{
			name:         "a drained retired group is deleted",
			specReplicas: 0, statusReplicas: 0, want: -1,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()

			declared := []api.InstanceSpec{voting("data", 3), voting("gone", 3)}
			cr := instanceCR(t, "sd-cr", "sd", declared, unsafeSize)
			rs := cr.Spec.Replsets[0]

			full, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			objs := []client.Object{cr}
			for _, g := range full.GetAll() {
				sts := groupSTS(cr, rs, g, g.Replicas, g.Replicas)
				if g.Name == "gone" {
					sts = groupSTS(cr, rs, g, tt.specReplicas, tt.specReplicas)
					// groupSTS mirrors spec into status; a workload that is
					// mid-termination has pods left over from a larger spec.
					sts.Status.Replicas = tt.statusReplicas
				}
				objs = append(objs, sts)
			}
			r := buildFakeClient(objs...)

			// the user deletes the second group
			rs.Instances = rs.Instances[:1]
			set, err := membergroup.Resolve(cr, rs)
			require.NoError(t, err)

			require.NoError(t, r.cleanupRemovedInstances(ctx, cr, rs, set))

			assert.Equal(t, tt.want, stsState(t, r, cr.Namespace, gone))
			assert.Equal(t, int32(3), stsState(t, r, cr.Namespace, "sd-cr-rs0-data"),
				"a declared group is never touched")
		})
	}
}

func TestCleanupRemovedInstancesIgnoresForeignWorkloads(t *testing.T) {
	ctx := t.Context()

	cr := instanceCR(t, "sd-cr", "sd", []api.InstanceSpec{voting("data", 3)}, unsafeSize)
	rs := cr.Spec.Replsets[0]

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	mongosLs := naming.MongosLabels(cr)
	mongosLs[naming.LabelKubernetesReplset] = rs.Name

	objs := []client.Object{cr}
	for _, g := range set.GetAll() {
		objs = append(objs, groupSTS(cr, rs, g, g.Replicas, g.Replicas))
	}
	objs = append(objs,
		withReplicas(labelledSTS(cr, "sd-cr-mongos", mongosLs, true), 2),
		withReplicas(labelledSTS(cr, "sd-cr-rs0-search", naming.SearchLabels(cr, rs), true), 2),
		// member-shaped labels, but nobody's child
		withReplicas(labelledSTS(cr, "sd-cr-rs0-impostor", naming.RSLabels(cr, rs), false), 2),
	)
	r := buildFakeClient(objs...)

	require.NoError(t, r.cleanupRemovedInstances(ctx, cr, rs, set))

	for _, name := range []string{"sd-cr-mongos", "sd-cr-rs0-search", "sd-cr-rs0-impostor"} {
		assert.Equal(t, int32(2), stsState(t, r, cr.Namespace, name),
			"%s is not a member workload of this replica set", name)
	}
}

func withReplicas(sts *appsv1.StatefulSet, n int32) *appsv1.StatefulSet {
	sts.Spec.Replicas = &n
	return sts
}

func TestCleanupStaleGroupConfigs(t *testing.T) {
	ctx := t.Context()

	cr := instanceCR(t, "sd-cr", "sd", []api.InstanceSpec{voting("data", 3)}, unsafeSize)
	rs := cr.Spec.Replsets[0]

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	want := make(map[string]struct{})
	for _, g := range set.GetAll() {
		want[g.ConfigName] = struct{}{}
		want[naming.GroupHookScriptConfigMapName(cr, rs, g.Component)] = struct{}{}
	}

	cm := func(name string, ls map[string]string, owned bool) *corev1.ConfigMap {
		c := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: cr.Namespace, Labels: ls}}
		if owned {
			c.OwnerReferences = []metav1.OwnerReference{ownedBy(cr)}
		}
		return c
	}
	ls := naming.RSLabels(cr, rs)

	objs := []client.Object{cr}
	for name := range want {
		objs = append(objs, cm(name, ls, true))
	}
	objs = append(objs,
		cm("sd-cr-rs0-gone", ls, true),                   // a retired group's config
		cm("sd-cr-rs0-gone-hookscript", ls, true),        // and its hookscript
		cm(naming.SearchConfigMapName(cr, rs), ls, true), // separate subsystem
		cm("sd-cr-unrelated", ls, true),                  // outside the replset prefix
		cm("sd-cr-rs0-impostor", ls, false),              // matching labels, not ours
	)
	r := buildFakeClient(objs...)

	require.NoError(t, r.cleanupStaleGroupConfigs(ctx, cr, rs, want))

	remaining := new(corev1.ConfigMapList)
	require.NoError(t, r.client.List(ctx, remaining, client.InNamespace(cr.Namespace)))

	got := make(map[string]struct{}, len(remaining.Items))
	for i := range remaining.Items {
		got[remaining.Items[i].Name] = struct{}{}
	}

	for name := range want {
		assert.Contains(t, got, name, "a declared group's configuration is kept")
	}
	assert.Contains(t, got, naming.SearchConfigMapName(cr, rs), "search owns its own config")
	assert.Contains(t, got, "sd-cr-unrelated", "outside the replset prefix")
	assert.Contains(t, got, "sd-cr-rs0-impostor", "not controlled by this cluster")

	assert.NotContains(t, got, "sd-cr-rs0-gone")
	assert.NotContains(t, got, "sd-cr-rs0-gone-hookscript")
}
