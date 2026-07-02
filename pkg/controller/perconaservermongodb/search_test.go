package perconaservermongodb

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func searchTestCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "psmdb",
			Namespace: "default",
		},
		Spec: api.PerconaServerMongoDBSpec{
			Replsets: []*api.ReplsetSpec{
				{Name: "rs0", Size: 3},
			},
		},
	}
}

// searchSts builds the mongot StatefulSet the way searchStatus expects to
// find it: the search-specific name/labels and a non-nil Replicas.
func searchSts(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, replicas int32) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.SearchStatefulSetName(cr, rs),
			Namespace: cr.Namespace,
			Labels:    naming.SearchLabels(cr, rs),
		},
		Spec: appsv1.StatefulSetSpec{
			Replicas: &replicas,
			Selector: &metav1.LabelSelector{MatchLabels: naming.SearchLabels(cr, rs)},
		},
	}
}

// searchPod builds a pod carrying the SearchLabels (so the status query's
// label selector matches it), then applies the given mutators.
func searchPod(name string, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, mutators ...func(*corev1.Pod)) *corev1.Pod {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
			Labels:    naming.SearchLabels(cr, rs),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	for _, m := range mutators {
		m(pod)
	}
	return pod
}

func containersReady(pod *corev1.Pod) {
	pod.Status.Conditions = append(pod.Status.Conditions, corev1.PodCondition{
		Type:   corev1.ContainersReady,
		Status: corev1.ConditionTrue,
	})
}

func TestSearchStatus(t *testing.T) {
	ctx := context.Background()
	cr := searchTestCR()
	rs := cr.Spec.Replsets[0]

	tests := map[string]struct {
		pause       bool
		objs        func() []client.Object
		wantStatus  api.AppState
		wantSize    int32
		wantReady   int32
		wantMessage string // substring match when non-empty
	}{
		"statefulset not found returns init": {
			objs:       func() []client.Object { return nil },
			wantStatus: api.AppStateInit,
			wantSize:   0,
			wantReady:  0,
		},
		"statefulset present but no pods is init": {
			objs: func() []client.Object {
				return []client.Object{searchSts(cr, rs, 3)}
			},
			wantStatus: api.AppStateInit,
			wantSize:   3,
			wantReady:  0,
		},
		"all replicas ready is ready": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 3),
					searchPod("psmdb-rs0-search-0", cr, rs, containersReady),
					searchPod("psmdb-rs0-search-1", cr, rs, containersReady),
					searchPod("psmdb-rs0-search-2", cr, rs, containersReady),
				}
			},
			wantStatus: api.AppStateReady,
			wantSize:   3,
			wantReady:  3,
		},
		"partially ready is init": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 3),
					searchPod("psmdb-rs0-search-0", cr, rs, containersReady),
					searchPod("psmdb-rs0-search-1", cr, rs),
					searchPod("psmdb-rs0-search-2", cr, rs),
				}
			},
			wantStatus: api.AppStateInit,
			wantSize:   3,
			wantReady:  1,
		},
		"ready count is capped at size": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 1),
					searchPod("psmdb-rs0-search-0", cr, rs, containersReady),
					searchPod("psmdb-rs0-search-1", cr, rs, containersReady),
				}
			},
			wantStatus: api.AppStateReady,
			wantSize:   1,
			wantReady:  1,
		},
		"failed and succeeded pods are skipped": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 3),
					searchPod("psmdb-rs0-search-0", cr, rs, containersReady),
					searchPod("psmdb-rs0-search-1", cr, rs, func(p *corev1.Pod) {
						p.Status.Phase = corev1.PodFailed
						containersReady(p)
					}),
					searchPod("psmdb-rs0-search-2", cr, rs, func(p *corev1.Pod) {
						p.Status.Phase = corev1.PodSucceeded
						containersReady(p)
					}),
				}
			},
			wantStatus: api.AppStateInit,
			wantSize:   3,
			wantReady:  1,
		},
		"terminating pods are skipped": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 3),
					searchPod("psmdb-rs0-search-0", cr, rs, containersReady),
					searchPod("psmdb-rs0-search-1", cr, rs, func(p *corev1.Pod) {
						now := metav1.Now()
						p.DeletionTimestamp = &now
						p.Finalizers = []string{"kubernetes"}
						containersReady(p)
					}),
				}
			},
			wantStatus: api.AppStateInit,
			wantSize:   3,
			wantReady:  1,
		},
		"long-unschedulable pod is error": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 3),
					searchPod("psmdb-rs0-search-0", cr, rs, func(p *corev1.Pod) {
						p.Status.Phase = corev1.PodPending
						p.Status.Conditions = []corev1.PodCondition{
							{
								Type:               corev1.PodScheduled,
								Status:             corev1.ConditionFalse,
								Reason:             corev1.PodReasonUnschedulable,
								Message:            "0/3 nodes are available",
								LastTransitionTime: metav1.NewTime(time.Now().Add(-2 * time.Minute)),
							},
						}
					}),
				}
			},
			wantStatus:  api.AppStateError,
			wantSize:    3,
			wantReady:   0,
			wantMessage: "0/3 nodes are available",
		},
		"recently-unschedulable pod is not error yet": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 3),
					searchPod("psmdb-rs0-search-0", cr, rs, func(p *corev1.Pod) {
						p.Status.Phase = corev1.PodPending
						p.Status.Conditions = []corev1.PodCondition{
							{
								Type:               corev1.PodScheduled,
								Status:             corev1.ConditionFalse,
								Reason:             corev1.PodReasonUnschedulable,
								Message:            "0/3 nodes are available",
								LastTransitionTime: metav1.Now(),
							},
						}
					}),
				}
			},
			wantStatus: api.AppStateInit,
			wantSize:   3,
			wantReady:  0,
		},
		"waiting container message is surfaced": {
			objs: func() []client.Object {
				return []client.Object{
					searchSts(cr, rs, 3),
					searchPod("psmdb-rs0-search-0", cr, rs, func(p *corev1.Pod) {
						p.Status.ContainerStatuses = []corev1.ContainerStatus{
							{
								Name: "mongot",
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{
										Reason:  "ImagePullBackOff",
										Message: "back-off pulling image",
									},
								},
							},
						}
					}),
				}
			},
			wantStatus:  api.AppStateInit,
			wantSize:    3,
			wantReady:   0,
			wantMessage: "mongot: back-off pulling image",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := buildFakeClient(tt.objs()...)

			status, err := r.searchStatus(ctx, cr, rs)
			require.NoError(t, err)

			assert.Equal(t, tt.wantStatus, status.Status)
			assert.Equal(t, tt.wantSize, status.Size)
			assert.Equal(t, tt.wantReady, status.Ready)
			if tt.wantMessage != "" {
				assert.Contains(t, status.Message, tt.wantMessage)
			}
		})
	}
}

func TestSearchStatus_Paused(t *testing.T) {
	ctx := context.Background()
	cr := searchTestCR()
	cr.Spec.Pause = true
	rs := cr.Spec.Replsets[0]

	t.Run("paused with ready pods is stopping", func(t *testing.T) {
		r := buildFakeClient(
			searchSts(cr, rs, 3),
			searchPod("psmdb-rs0-search-0", cr, rs, containersReady),
		)

		status, err := r.searchStatus(ctx, cr, rs)
		require.NoError(t, err)
		assert.Equal(t, api.AppStateStopping, status.Status,
			"a paused cluster with pods still running must report stopping")
		assert.Equal(t, int32(1), status.Ready)
	})

	t.Run("paused with no ready pods is paused", func(t *testing.T) {
		r := buildFakeClient(searchSts(cr, rs, 3))

		status, err := r.searchStatus(ctx, cr, rs)
		require.NoError(t, err)
		assert.Equal(t, api.AppStatePaused, status.Status)
		assert.Equal(t, int32(0), status.Ready)
	})
}
