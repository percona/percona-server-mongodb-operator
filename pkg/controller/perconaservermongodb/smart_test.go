package perconaservermongodb

import (
	"context"
	"maps"
	"slices"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestSortPodsByOrdinal(t *testing.T) {
	t.Parallel()

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

func claimRolloutPod(ordinal int, volumes ...string) corev1.Pod {
	pod := mockReadyReplsetPod("some-name-rs0-"+strconv.Itoa(ordinal), "some-namespace", "some-name", "rs0", "mongod")
	pod.Labels[appsv1.PodIndexLabel] = strconv.Itoa(ordinal)
	pod.Labels["controller-revision-hash"] = "old-revision"
	for _, v := range volumes {
		pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: v})
	}
	return *pod
}

func claimRolloutSts(replicas int32) *appsv1.StatefulSet {
	sts := mockReadyReplsetSts("some-name-rs0", "some-namespace", "some-name", "rs0", "mongod", replicas)
	sts.Spec.Replicas = new(int32(3))
	sts.Spec.Selector = &metav1.LabelSelector{MatchLabels: maps.Clone(sts.Labels)}
	sts.Spec.VolumeClaimTemplates = []corev1.PersistentVolumeClaim{
		{Name: config.MongodDataVolClaimName},
		{Name: config.MongodJournalVolClaimName},
	}
	sts.Status.Replicas = replicas
	sts.Status.UpdateRevision = "new-revision"
	return sts
}

func TestPodBlockingClaimRollout(t *testing.T) {
	data, journal := config.MongodDataVolClaimName, config.MongodJournalVolClaimName

	tests := map[string]struct {
		sts     *appsv1.StatefulSet
		pods    []corev1.Pod
		wantPod string
	}{
		"all pods present": {
			sts:  claimRolloutSts(3),
			pods: []corev1.Pod{claimRolloutPod(0, data), claimRolloutPod(1, data), claimRolloutPod(2, data)},
		},
		"pod missing and every pod lacks the claim": {
			sts:     claimRolloutSts(2),
			pods:    []corev1.Pod{claimRolloutPod(1, data), claimRolloutPod(0, data)},
			wantPod: "some-name-rs0-0",
		},
		"pod missing and the lowest pod is already updated": {
			sts:     claimRolloutSts(2),
			pods:    []corev1.Pod{claimRolloutPod(0, data, journal), claimRolloutPod(1, data)},
			wantPod: "some-name-rs0-1",
		},
		"pod missing but every pod has its claims": {
			sts:  claimRolloutSts(2),
			pods: []corev1.Pod{claimRolloutPod(0, data, journal), claimRolloutPod(1, data, journal)},
		},
		"replicas unset": {
			sts: func() *appsv1.StatefulSet {
				sts := claimRolloutSts(2)
				sts.Spec.Replicas = nil
				return sts
			}(),
			pods: []corev1.Pod{claimRolloutPod(0, data), claimRolloutPod(1, data)},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := podBlockingClaimRollout(tc.sts, tc.pods)
			assert.Equal(t, tc.wantPod != "", ok)
			assert.Equal(t, tc.wantPod, got.Name)
		})
	}
}

func TestSmartUpdateUnblocksClaimRollout(t *testing.T) {
	data := config.MongodDataVolClaimName

	tests := map[string]struct {
		pods        []corev1.Pod
		objs        []client.Object
		pvcShrink   bool
		wantErrMsg  string
		wantDeleted []string
	}{
		"running backup keeps the pod": {
			pods: []corev1.Pod{claimRolloutPod(0, data), claimRolloutPod(1, data)},
			objs: []client.Object{&api.PerconaServerMongoDBBackup{
				Name:      "running",
				Namespace: "some-namespace",
				Spec:      api.PerconaServerMongoDBBackupSpec{ClusterName: "some-name"},
				Status:    api.PerconaServerMongoDBBackupStatus{State: api.BackupStateRunning},
			}},
		},
		"pvc shrink keeps the pod": {
			pods:      []corev1.Pod{claimRolloutPod(0, data), claimRolloutPod(1, data)},
			pvcShrink: true,
		},
		"statefulset short of a pod recreates the lowest pod missing the claim": {
			pods:        []corev1.Pod{claimRolloutPod(0, data), claimRolloutPod(1, data)},
			wantErrMsg:  "wait pod restart: reach pod wait limit",
			wantDeleted: []string{"some-name-rs0-0"},
		},
		"all pods present waits for replset members": {
			pods: []corev1.Pod{claimRolloutPod(0, data), claimRolloutPod(1, data), claimRolloutPod(2, data)},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			rs := &api.ReplsetSpec{Name: "rs0", Size: 3, LivenessProbe: &api.LivenessProbeExtended{}}
			rs.LivenessProbe.InitialDelaySeconds = 1
			cr := &api.PerconaServerMongoDB{
				Name:      "some-name",
				Namespace: "some-namespace",
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion:      version.Version(),
					UpdateStrategy: api.SmartUpdateStatefulSetStrategyType,
					Replsets:       []*api.ReplsetSpec{rs},
				},
				Status: api.PerconaServerMongoDBStatus{
					// the replset reconcile reports no members while a pod is missing
					Replsets: map[string]api.ReplsetStatus{"rs0": {Initialized: true}},
				},
			}

			if tc.pvcShrink {
				cr.Status.PVCShrink = map[string]api.PVCShrinkState{"some-name-rs0": {}}
			}

			sts := claimRolloutSts(int32(len(tc.pods)))
			objs := append([]client.Object{cr, sts}, tc.objs...)
			for i := range tc.pods {
				objs = append(objs, &tc.pods[i])
			}
			r := buildFakeClient(objs...)

			err := r.smartUpdate(t.Context(), cr, sts, rs)
			if tc.wantErrMsg != "" {
				require.EqualError(t, err, tc.wantErrMsg)
			} else {
				require.NoError(t, err)
			}

			for _, pod := range tc.pods {
				getErr := r.client.Get(t.Context(), client.ObjectKeyFromObject(&pod), new(corev1.Pod))
				if slices.Contains(tc.wantDeleted, pod.Name) {
					assert.True(t, k8serrors.IsNotFound(getErr), "pod %s should be deleted", pod.Name)
				} else {
					assert.NoError(t, getErr, "pod %s should be kept", pod.Name)
				}
			}
		})
	}
}
