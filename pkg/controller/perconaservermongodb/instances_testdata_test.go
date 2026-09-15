package perconaservermongodb

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

// instanceCR returns a defaulted single-replset CR whose rs0 declares its
// topology through instances[]. The legacy fields are cleared because the CRD
// makes them mutually exclusive with instances[], and CheckNSetDefaults would
// otherwise validate a shape the API server never accepts.
//
// mutate runs after the topology is set and before defaulting, which is the
// only window where a test can relax checkSafeInstanceDefaults. Topologies that
// mirror a PSA — an even voter count, or fewer than three data-bearing voters —
// are rejected outright without spec.unsafeFlags.replsetSize, so a test that
// needs one must ask for it here.
func instanceCR(t *testing.T, name, ns string, instances []api.InstanceSpec, mutate ...func(*api.PerconaServerMongoDB)) *api.PerconaServerMongoDB {
	t.Helper()

	cr, err := readDefaultCR(name, ns)
	require.NoError(t, err)

	rs := cr.Spec.Replsets[0]
	rs.Size = 0
	rs.VolumeSpec = nil
	rs.Arbiter = api.Arbiter{}
	rs.NonVoting = api.NonVotingSpec{}
	rs.Hidden = api.HiddenSpec{}
	rs.Instances = instances
	cr.Spec.Sharding.Enabled = false

	for _, m := range mutate {
		m(cr)
	}

	require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

	return cr
}

// unsafeSize relaxes the replica-set size checks. Pass it to instanceCR for a
// topology that is deliberately not production-safe.
func unsafeSize(cr *api.PerconaServerMongoDB) { cr.Spec.Unsafe.ReplsetSize = true }

// resolveGroup resolves rs and returns the named group, failing the test if the
// topology does not declare it.
func resolveGroup(t *testing.T, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, name string) membergroup.Group {
	t.Helper()

	set, err := membergroup.Resolve(cr, rs)
	require.NoError(t, err)

	group, ok := set.GetByName(name)
	require.Truef(t, ok, "no member group %q in replset %s (have %v)", name, rs.Name, set.GetNames())

	return group
}

// groupPod builds a Running+Ready pod of the group at the given ordinal.
//
// The component label and the container name both come from the group: code
// under test looks pods up by component and execs into group.ContainerName,
// which is not "mongod" for every group. PodIndexLabel is set because
// util.SortPodsByOrdinal* reads it, and a pod without it sorts to -1.
func groupPod(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, g membergroup.Group, idx int) *corev1.Pod {
	ls := naming.RSLabels(cr, rs)
	ls[naming.LabelKubernetesComponent] = g.Component
	ls[appsv1.PodIndexLabel] = strconv.Itoa(idx)

	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.GroupPodName(cr, rs, g.Name, idx),
			Namespace: cr.Namespace,
			Labels:    ls,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{
				Name:  g.ContainerName,
				Ports: []corev1.ContainerPort{{ContainerPort: rs.GetPort()}},
			}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{{
				Name:  g.ContainerName,
				Ready: true,
				State: corev1.ContainerState{
					Running: &corev1.ContainerStateRunning{StartedAt: metav1.Now()},
				},
			}},
			Conditions: []corev1.PodCondition{
				{Type: corev1.ContainersReady, Status: corev1.ConditionTrue},
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
		},
	}
}

// notReady marks a pod built by groupPod as not ready, leaving everything else
// in place. Several call sites distinguish "pod exists" from "pod is usable".
func notReady(pod *corev1.Pod) *corev1.Pod {
	for i := range pod.Status.ContainerStatuses {
		pod.Status.ContainerStatuses[i].Ready = false
	}
	for i := range pod.Status.Conditions {
		pod.Status.Conditions[i].Status = corev1.ConditionFalse
	}
	return pod
}

// terminating stamps a deletion timestamp on a pod built by groupPod.
// shutdownTarget counts live and total pods separately, and a terminating pod
// must not be counted as live or the StatefulSet is scaled back up.
func terminating(pod *corev1.Pod) *corev1.Pod {
	now := metav1.Now()
	pod.DeletionTimestamp = &now
	pod.Finalizers = []string{"test/hold"} // the fake client rejects a deletionTimestamp without one
	return pod
}

// groupSTS builds the group's StatefulSet, owned by cr.
//
// The controller owner reference is not optional: getMemberStatefulsets,
// getShardsWithWorkloads and cleanupRemovedInstances all filter on
// metav1.IsControlledBy, so a fixture without it produces an empty list and a
// vacuously passing test.
func groupSTS(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, g membergroup.Group, specReplicas, readyReplicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      g.STSName,
			Namespace: cr.Namespace,
			Labels:    g.Labels,
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: api.SchemeGroupVersion.String(),
				Kind:       "PerconaServerMongoDB",
				Name:       cr.Name,
				UID:        cr.UID,
				Controller: new(true),
			}},
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: new(specReplicas),
			Selector: &metav1.LabelSelector{MatchLabels: g.Labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: g.Labels},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{
						Name:  g.ContainerName,
						Ports: []corev1.ContainerPort{{ContainerPort: rs.GetPort()}},
					}},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{
			Replicas:      specReplicas,
			ReadyReplicas: readyReplicas,
		},
	}
}
