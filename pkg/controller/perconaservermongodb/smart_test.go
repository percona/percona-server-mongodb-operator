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
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestSortPodsByOrdinal(t *testing.T) {
	ordinalPod := func(i int) corev1.Pod {
		return corev1.Pod{
			Name:   "pod-" + strconv.Itoa(i),
			Labels: map[string]string{appsv1.PodIndexLabel: strconv.Itoa(i)},
		}
	}

	testCases := []struct {
		desc        string
		pods        []corev1.Pod
		expectedOrd []string
		less        func(i, j int) bool
	}{
		{
			desc: "ascending",
			pods: []corev1.Pod{
				ordinalPod(0),
				ordinalPod(1),
				ordinalPod(2),
				ordinalPod(3),
				ordinalPod(4),
				ordinalPod(5),
				ordinalPod(6),
				ordinalPod(7),
				ordinalPod(8),
				ordinalPod(9),
				ordinalPod(10),
			},
			expectedOrd: []string{
				"pod-0",
				"pod-1",
				"pod-2",
				"pod-3",
				"pod-4",
				"pod-5",
				"pod-6",
				"pod-7",
				"pod-8",
				"pod-9",
				"pod-10",
			},
			less: func(i, j int) bool {
				return i < j
			},
		},
		{
			desc: "descending",
			pods: []corev1.Pod{
				ordinalPod(0),
				ordinalPod(1),
				ordinalPod(2),
				ordinalPod(3),
				ordinalPod(4),
				ordinalPod(5),
				ordinalPod(6),
				ordinalPod(7),
				ordinalPod(8),
				ordinalPod(9),
				ordinalPod(10),
			},
			expectedOrd: []string{
				"pod-10",
				"pod-9",
				"pod-8",
				"pod-7",
				"pod-6",
				"pod-5",
				"pod-4",
				"pod-3",
				"pod-2",
				"pod-1",
				"pod-0",
			},
			less: func(i, j int) bool {
				return i > j
			},
		},
	}

	for _, tc := range testCases {
		in := tc.pods
		sortPodsByOrdinal(in, tc.less)
		for i := range in {
			if in[i].Name != tc.expectedOrd[i] {
				t.Errorf("%s: expected pod at index %d to be %s, got %s", tc.desc, i, tc.expectedOrd[i], in[i].Name)
			}
		}
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

	rs := &api.ReplsetSpec{Name: "rs0", Size: 3, LivenessProbe: new(api.LivenessProbeExtended)}
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
	if err := r.smartUpdate(ctx, cr, sts, rs); err != nil {
		t.Fatalf("smart update should wait for replset members, got error: %v", err)
	}
}
