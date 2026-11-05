package perconaservermongodb

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/vectorsearch"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func searchTestCR() *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		Name:      "psmdb",
		Namespace: "default",
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
		Name:      naming.SearchStatefulSetName(cr, rs),
		Namespace: cr.Namespace,
		Labels:    naming.SearchLabels(cr, rs),
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
		Name:      name,
		Namespace: cr.Namespace,
		Labels:    naming.SearchLabels(cr, rs),
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
	t.Parallel()

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
	t.Parallel()

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

func TestSearchPVCRevertPreservesExpandedShard(t *testing.T) {
	tests := []struct {
		name            string
		storageOverride bool
		nodeSelector    map[string]string
	}{
		{name: "inherited storage"},
		{name: "storage override", storageOverride: true},
		{name: "node selector override", nodeSelector: map[string]string{"zone": "failed-zone"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := searchTestCR()
			cr.Spec.CRVersion = version.Version()
			cr.Spec.Unsafe.TLS = true
			cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeDisabled}
			cr.Spec.Secrets = &api.SecretsSpec{}
			cr.Spec.Search = &api.SearchSpec{
				Enabled: true,
				Size:    1,
				Storage: &api.VolumeSpec{
					PersistentVolumeClaim: api.PVCSpec{
						PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
							Resources: corev1.VolumeResourceRequirements{
								Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
							},
						},
					},
				},
			}
			cr.Spec.StorageScaling = &api.StorageScalingSpec{
				EnableVolumeScaling: true,
			}
			rs := cr.Spec.Replsets[0]
			cr.Spec.Replsets = append(cr.Spec.Replsets, &api.ReplsetSpec{Name: "rs1"})
			expandedSTS := vectorsearch.StatefulSet(cr, rs, "init", "", nil).DeepCopy()
			expandedPod := searchPod(expandedSTS.Name+"-0", cr, rs)
			expandedPVC := &corev1.PersistentVolumeClaim{
				Name:      vectorsearch.DataVolumeName + "-" + expandedPod.Name,
				Namespace: cr.Namespace,
				Labels:    naming.SearchLabels(cr, rs),
				Spec:      *cr.Spec.Search.Storage.PersistentVolumeClaim.PersistentVolumeClaimSpec.DeepCopy(),
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
				},
			}
			cr.Spec.Search.Storage.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("12Gi")
			expandedSTS.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("12Gi")
			expandedPVC.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("12Gi")
			expandedPVC.Status.Capacity[corev1.ResourceStorage] = resource.MustParse("12Gi")

			failedRS := cr.Spec.Replsets[1]
			if tt.storageOverride || tt.nodeSelector != nil {
				failedRS.Search = &api.SearchReplsetOverride{NodeSelector: tt.nodeSelector}
				if tt.storageOverride {
					failedRS.Search.Storage = cr.Spec.Search.Storage.DeepCopy()
				}
			}
			failedSTS := vectorsearch.StatefulSet(cr, failedRS, "init", "", nil).DeepCopy()
			failedSTS.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("10Gi")
			failedPod := searchPod(failedSTS.Name+"-0", cr, failedRS)
			failedPVC := expandedPVC.DeepCopy()
			failedPVC.Name = vectorsearch.DataVolumeName + "-" + failedPod.Name
			failedPVC.Labels = naming.SearchLabels(cr, failedRS)
			failedPVC.Spec.Resources.Requests[corev1.ResourceStorage] = resource.MustParse("10Gi")
			failedPVC.Status.Capacity[corev1.ResourceStorage] = resource.MustParse("10Gi")

			r := buildFakeClient(cr, expandedSTS, expandedPVC, expandedPod, failedSTS, failedPVC, failedPod)
			attempts := 0
			r.client = interceptor.NewClient(r.client.(client.WithWatch), interceptor.Funcs{Update: func(
				ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption,
			) error {
				if pvc, ok := obj.(*corev1.PersistentVolumeClaim); ok && pvc.Name == failedPVC.Name {
					attempts++
					return errors.New("exceeded quota")
				}
				return c.Update(ctx, obj, opts...)
			}})

			t.Run("reconcile failed resize and reload cluster", func(t *testing.T) {
				require.NoError(t, r.reconcileSearch(t.Context(), cr, failedRS))
				require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), cr))
			})

			t.Run("verify revert preserves shared storage and shard overrides", func(t *testing.T) {
				sharedSize := cr.Spec.Search.Storage.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
				assert.Equal(t, "12Gi", sharedSize.String())
				require.NotNil(t, cr.Spec.Replsets[1].Search)
				failedStorage := cr.Spec.Replsets[1].Search.Storage
				require.NotNil(t, failedStorage)
				failedSize := failedStorage.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
				assert.Equal(t, "10Gi", failedSize.String())
				assert.Equal(t, tt.nodeSelector, cr.Spec.Replsets[1].Search.NodeSelector)
				require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(failedSTS), failedSTS))
				assert.Equal(t, resource.MustParse("10Gi"), failedSTS.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage])
				assert.NotContains(t, failedSTS.Annotations, api.AnnotationPVCResizeInProgress)
			})

			t.Run("reconcile both shards and verify storage remains stable", func(t *testing.T) {
				require.NoError(t, r.reconcileSearch(t.Context(), cr, cr.Spec.Replsets[0]))
				require.NoError(t, r.reconcileSearch(t.Context(), cr, cr.Spec.Replsets[1]))
				assert.Equal(t, 1, attempts, "revert must not cause another shared resize attempt")
				require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(failedSTS), failedSTS))
				assert.NotContains(t, failedSTS.Annotations, api.AnnotationPVCResizeInProgress)
				require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(expandedSTS), expandedSTS))
				expandedSize := expandedSTS.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
				assert.Equal(t, "12Gi", expandedSize.String())
			})
		})
	}
}
