package perconaservermongodb

import (
	"context"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo"
	mongoFake "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/fake"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/vectorsearch"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestValidatePVCName(t *testing.T) {
	sts := &appsv1.StatefulSet{
		Name: "some-name-rs0",
	}

	tests := []struct {
		name    string
		pvcName string
		want    bool
	}{
		{
			name:    "matching StatefulSet ordinal",
			pvcName: "mongod-data-some-name-rs0-0",
			want:    true,
		},
		{
			name:    "hidden StatefulSet with overlapping prefix",
			pvcName: "mongod-data-some-name-rs0-hidden-0",
			want:    false,
		},
		{
			name:    "non-voting StatefulSet with overlapping prefix",
			pvcName: "mongod-data-some-name-rs0-nv-0",
			want:    false,
		},
		{
			name:    "non-numeric suffix",
			pvcName: "mongod-data-some-name-rs0-other",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pvc := corev1.PersistentVolumeClaim{
				Name: tt.pvcName,
			}

			assert.Equal(t, tt.want, validatePVCName(config.MongodDataVolClaimName, pvc, sts))
		})
	}
}

func TestReconcilePersistentVolumes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		requested         string
		configured        string
		actual            string
		resizeInProgress  bool
		volumeScaling     bool
		volumeShrink      bool
		orphanPVC         bool
		expectSTSDeleted  bool
		expectRestartedAt bool
		expectErrContains string
		expectCRStorage   string
	}{
		{
			name:              "finishes resize when pvc exceeds requested size",
			requested:         "1200Mi",
			configured:        "1200Mi",
			actual:            "6G",
			resizeInProgress:  true,
			expectSTSDeleted:  true,
			expectRestartedAt: true,
		},
		{
			name:             "finishes resize when orphan pvc exceeds requested size",
			requested:        "1200Mi",
			configured:       "1200Mi",
			actual:           "6G",
			resizeInProgress: true,
			orphanPVC:        true,
			expectSTSDeleted: true,
		},
		{
			name:              "deletes statefulset when requested matches actual but template differs",
			requested:         "1200Mi",
			configured:        "6G",
			actual:            "1200Mi",
			expectSTSDeleted:  true,
			expectRestartedAt: true,
		},
		{
			name:              "deletes statefulset when actual exceeds requested and template is lower",
			requested:         "1200Mi",
			configured:        "1Gi",
			actual:            "6G",
			expectSTSDeleted:  true,
			expectRestartedAt: true,
		},
		{
			name:       "does nothing when requested configured and actual sizes are aligned",
			requested:  "1200Mi",
			configured: "1200Mi",
			actual:     "1200Mi",
		},
		{
			name:       "does nothing when requested and configured sizes are aligned",
			requested:  "1200Mi",
			configured: "1200Mi",
			actual:     "6G",
		},
		{
			name:              "rejects shrink when configured is higher than requested and actual is higher than requested",
			requested:         "1200Mi",
			configured:        "2Gi",
			actual:            "6G",
			volumeScaling:     true,
			expectErrContains: "requested storage (1200Mi) is less than actual storage (6G)",
			expectCRStorage:   "2Gi",
		},
		{
			name:              "rejects shrink for single-member replica set",
			requested:         "1200Mi",
			configured:        "6G",
			actual:            "6G",
			volumeShrink:      true,
			expectErrContains: "single-member replica set has no sync source",
			expectCRStorage:   "6G",
		},
		{
			name:             "deletes statefulset when pvc already matches increased request but template is stale",
			requested:        "2Gi",
			configured:       "1200Mi",
			actual:           "2Gi",
			expectSTSDeleted: true,
		},
	}

	components := []string{
		naming.ComponentMongod,
		naming.ComponentNonVoting,
		naming.ComponentHidden,
		naming.ComponentConfigSrv,
		naming.ComponentSearch,
	}
	for _, component := range components {
		for _, tt := range tests {
			if component == naming.ComponentSearch && tt.volumeShrink {
				tt.name = "rejects search shrink for multi-member replica set"
				tt.expectErrContains = "volume shrinking is not supported for search"
			}
			t.Run(component+"/"+tt.name, func(t *testing.T) {
				const restartedAt = "2026-01-01T00:00:00Z"
				var (
					cr             *api.PerconaServerMongoDB
					sts            *appsv1.StatefulSet
					pod            *corev1.Pod
					r              *ReconcilePerconaServerMongoDB
					deletionPolicy *metav1.DeletionPropagation
				)

				t.Run("prepare cluster and storage resources", func(t *testing.T) {
					requested := resource.MustParse(tt.requested)
					configured := resource.MustParse(tt.configured)
					actual := resource.MustParse(tt.actual)

					var err error
					cr, err = readDefaultCR("some-cluster", "ns")
					require.NoError(t, err)
					require.NotEmpty(t, cr.Spec.Replsets)
					cr.Spec.StorageScaling = &api.StorageScalingSpec{
						EnableVolumeScaling: tt.volumeScaling,
						EnableVolumeShrink:  tt.volumeShrink,
					}
					if tt.volumeShrink {
						cr.Status.State = api.AppStateReady
					}

					rs := cr.Spec.Replsets[0]
					rs.Size = 1
					rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests = corev1.ResourceList{
						corev1.ResourceStorage: requested,
					}

					labels := naming.MongodLabels(cr, rs)
					stsName := naming.MongodStatefulSetName(cr, rs)
					claimName := config.MongodDataVolClaimName
					switch component {
					case naming.ComponentNonVoting:
						rs.NonVoting.VolumeSpec = rs.VolumeSpec.DeepCopy()
						labels = naming.NonVotingLabels(cr, rs)
					case naming.ComponentHidden:
						rs.Hidden.VolumeSpec = rs.VolumeSpec.DeepCopy()
						labels = naming.HiddenLabels(cr, rs)
					case naming.ComponentConfigSrv:
						cr.Spec.Sharding.ConfigsvrReplSet.Name = api.ConfigReplSetName
						cr.Spec.Sharding.ConfigsvrReplSet.Size = 1
						cr.Spec.Sharding.ConfigsvrReplSet.VolumeSpec = rs.VolumeSpec.DeepCopy()
						labels = naming.MongodLabels(cr, cr.Spec.Sharding.ConfigsvrReplSet)
						labels[naming.LabelKubernetesComponent] = naming.ComponentConfigSrv
					case naming.ComponentSearch:
						rs.Size = 3
						cr.Spec.Search = &api.SearchSpec{Enabled: true, Storage: rs.VolumeSpec.DeepCopy()}
						labels = naming.SearchLabels(cr, rs)
						stsName = naming.SearchStatefulSetName(cr, rs)
						claimName = vectorsearch.DataVolumeName
					}
					sts = newStatefulSet(cr.Namespace, stsName, labels, configured)
					sts.Spec.VolumeClaimTemplates[0].Name = claimName
					sts.Labels = maps.Clone(labels)
					sts.Labels["custom-label"] = "metadata-only"
					sts.Spec.VolumeClaimTemplates = append([]corev1.PersistentVolumeClaim{{Name: "sidecar-data"}}, sts.Spec.VolumeClaimTemplates...)
					if tt.expectRestartedAt {
						sts.Spec.Template.Annotations = map[string]string{
							naming.AnnotationKubectlRestartedAt: restartedAt,
						}
					}
					if tt.resizeInProgress {
						sts.Annotations = map[string]string{
							api.AnnotationPVCResizeInProgress: time.Now().Add(-time.Minute).Format(time.RFC3339),
						}
					}

					pod = newPod(cr.Namespace, sts.Name+"-0", labels)
					pvc := newPVC(cr.Namespace, claimName+"-"+pod.Name, labels, actual, actual)
					objects := []client.Object{cr, sts, pod, pvc}
					if tt.orphanPVC {
						orphanPVC := newPVC(cr.Namespace, claimName+"-"+sts.Name+"-1", labels, actual, actual)
						objects = append(objects, orphanPVC)
					}

					r = buildFakeClient(objects...)
					r.client = interceptor.NewClient(r.client.(client.WithWatch), interceptor.Funcs{
						Delete: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
							if _, ok := obj.(*appsv1.StatefulSet); ok {
								options := &client.DeleteOptions{}
								options.ApplyOptions(opts)
								deletionPolicy = options.PropagationPolicy
							}
							return c.Delete(ctx, obj, opts...)
						},
					})
				})

				t.Run("reconcile PVCs", func(t *testing.T) {
					err := r.reconcilePVCs(t.Context(), cr, sts)
					if tt.expectErrContains != "" {
						require.Error(t, err)
						assert.Contains(t, err.Error(), tt.expectErrContains)
					} else {
						require.NoError(t, err)
					}
				})

				t.Run("verify StatefulSet and pod preservation", func(t *testing.T) {
					fetchedSTS := &appsv1.StatefulSet{}
					err := r.client.Get(t.Context(), client.ObjectKeyFromObject(sts), fetchedSTS)
					if tt.expectSTSDeleted {
						assert.Error(t, err)
						assert.True(t, client.IgnoreNotFound(err) == nil)
						require.NotNil(t, deletionPolicy)
						assert.Equal(t, metav1.DeletePropagationOrphan, *deletionPolicy)
						require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(pod), new(corev1.Pod)))
					} else {
						assert.NoError(t, err)
					}
				})

				t.Run("verify cluster storage and preserved restart annotation", func(t *testing.T) {
					fetchedCR := &api.PerconaServerMongoDB{}
					err := r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), fetchedCR)
					require.NoError(t, err)
					if tt.expectCRStorage != "" {
						expected := resource.MustParse(tt.expectCRStorage)
						storage := fetchedCR.Spec.Replsets[0].VolumeSpec
						switch component {
						case naming.ComponentNonVoting:
							storage = fetchedCR.Spec.Replsets[0].NonVoting.VolumeSpec
						case naming.ComponentHidden:
							storage = fetchedCR.Spec.Replsets[0].Hidden.VolumeSpec
						case naming.ComponentConfigSrv:
							storage = fetchedCR.Spec.Sharding.ConfigsvrReplSet.VolumeSpec
						case naming.ComponentSearch:
							storage = fetchedCR.Spec.Replsets[0].SearchStorage(fetchedCR)
						}
						assert.Equal(t, expected, storage.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage])
					}
					if tt.expectRestartedAt {
						assert.Equal(t, restartedAt, fetchedCR.Annotations[api.AnnotationPreservedRestartedAt(sts.Name)])
					}
				})
			})
		}
	}
}

func TestReconcilePersistentVolumesExternalAutoscaling(t *testing.T) {
	t.Parallel()

	const (
		namespace      = "test-ns"
		clusterName    = "test-cluster"
		configuredSize = "1Gi"
		requestedSize  = "2Gi"
	)

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, apis.AddToScheme(scheme))

	cr, err := readDefaultCR(clusterName, namespace)
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)

	rs := cr.Spec.Replsets[0]
	rs.Size = 1
	rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: resource.MustParse(requestedSize),
	}

	labels := naming.MongodLabels(cr, rs)
	sts := newStatefulSet(namespace, naming.MongodStatefulSetName(cr, rs), labels, resource.MustParse(configuredSize))
	pod := newPod(namespace, sts.Name+"-0", labels)
	pvc := newPVC(namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, resource.MustParse(configuredSize), resource.MustParse(configuredSize))

	tests := map[string]struct {
		externalAutoscaling bool
		volumeExpansion     bool
		expectRequestedSize string
		expectPVCSize       string
		expectSTSAnnotation bool
	}{
		"external autoscaling enabled, volume expansion enabled": {
			externalAutoscaling: true,
			volumeExpansion:     true,
			expectRequestedSize: requestedSize,
			expectPVCSize:       configuredSize,
		},
		"external autoscaling enabled, volume expansion disabled": {
			externalAutoscaling: true,
			volumeExpansion:     false,
			expectRequestedSize: requestedSize,
			expectPVCSize:       configuredSize,
		},
		"external autoscaling disabled, volume expansion enabled": {
			externalAutoscaling: false,
			volumeExpansion:     true,
			expectRequestedSize: requestedSize,
			expectPVCSize:       requestedSize,
			expectSTSAnnotation: true,
		},
		"external autoscaling disabled, volume expansion disabled": {
			externalAutoscaling: false,
			volumeExpansion:     false,
			expectRequestedSize: configuredSize,
			expectPVCSize:       configuredSize,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()

			cr := cr.DeepCopy()
			sts := sts.DeepCopy()
			pvc := pvc.DeepCopy()
			pod := pod.DeepCopy()

			cr.Spec.StorageScaling = &api.StorageScalingSpec{
				EnableExternalAutoscaling: tt.externalAutoscaling,
				EnableVolumeScaling:       tt.volumeExpansion,
			}

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(cr, sts, pvc, pod).
				Build()

			r := &ReconcilePerconaServerMongoDB{
				client: cl,
				scheme: scheme,
			}

			err := r.reconcilePVCs(ctx, cr, sts)
			require.NoError(t, err)

			gotSTS := new(appsv1.StatefulSet)
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(sts), gotSTS))
			_, hasResizeAnnotation := gotSTS.Annotations[api.AnnotationPVCResizeInProgress]
			assert.Equal(t, tt.expectSTSAnnotation, hasResizeAnnotation)

			requestedSize := cr.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Zero(t, requestedSize.Cmp(resource.MustParse(tt.expectRequestedSize)))

			gotPVC := new(corev1.PersistentVolumeClaim)
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(pvc), gotPVC))
			pvcSize := gotPVC.Spec.Resources.Requests[corev1.ResourceStorage]
			assert.Zero(t, pvcSize.Cmp(resource.MustParse(tt.expectPVCSize)))
		})
	}
}

func newStatefulSet(namespace, name string, labels map[string]string, storage resource.Quantity) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		Name:      name,
		Namespace: namespace,
		Labels:    labels,
		Spec: appsv1.StatefulSetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: labels,
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "mongod",
							Image: "percona/percona-server-mongodb",
						},
					},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					Name: config.MongodDataVolClaimName,
					Spec: corev1.PersistentVolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: storage,
							},
						},
					},
				},
			},
		},
	}
}

func newPod(namespace, name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		Name:      name,
		Namespace: namespace,
		Labels:    labels,
	}
}

func newPVC(namespace, name string, labels map[string]string, requested, capacity resource.Quantity) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		Name:      name,
		Namespace: namespace,
		Labels:    labels,
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: requested,
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: capacity,
			},
		},
	}
}

func TestResizeVolumesIfNeeded_NoSpuriousResizeOnDecimalUnits(t *testing.T) {
	t.Parallel()

	const (
		ns      = "test-ns"
		stsName = "my-cluster-rs0"
	)

	tests := []struct {
		name             string
		requestedStorage string
		actualCapacity   string
		expectResize     bool
	}{
		{
			name:             "1G requested, 1G provisioned (decimal match, no resize needed)",
			requestedStorage: "1G",
			actualCapacity:   "1G",
			expectResize:     false,
		},
		{
			name:             "1Gi requested, 1Gi provisioned (binary match, no resize needed)",
			requestedStorage: "1Gi",
			actualCapacity:   "1Gi",
			expectResize:     false,
		},
		{
			name:             "2Gi requested, 2Gi provisioned (binary match, no resize needed)",
			requestedStorage: "2Gi",
			actualCapacity:   "2Gi",
			expectResize:     false,
		},
		{
			name:             "2G requested, 1G provisioned (under-provisioned, resize needed)",
			requestedStorage: "2G",
			actualCapacity:   "1G",
			expectResize:     true,
		},
		{
			name:             "6Gi requested, 6Gi provisioned (binary match, no resize needed)",
			requestedStorage: "6Gi",
			actualCapacity:   "6Gi",
			expectResize:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requested := resource.MustParse(tt.requestedStorage)
			actual := resource.MustParse(tt.actualCapacity)

			pvcName := config.MongodDataVolClaimName + "-" + stsName + "-0"
			podName := stsName + "-0"

			sts := &appsv1.StatefulSet{
				Name:      stsName,
				Namespace: ns,
				Spec: appsv1.StatefulSetSpec{
					VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
						{
							Name: config.MongodDataVolClaimName,
							Spec: corev1.PersistentVolumeClaimSpec{
								Resources: corev1.VolumeResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceStorage: requested.DeepCopy(),
									},
								},
							},
						},
					},
				},
			}

			pvc := &corev1.PersistentVolumeClaim{
				Name:      pvcName,
				Namespace: ns,
				Labels: map[string]string{
					"app": "test",
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceStorage: requested.DeepCopy(),
						},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: actual.DeepCopy(),
					},
				},
			}

			pod := &corev1.Pod{
				Name:      podName,
				Namespace: ns,
				Labels: map[string]string{
					"app": "test",
				},
			}

			cr := &api.PerconaServerMongoDB{
				Name:      "my-cluster",
				Namespace: ns,
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					StorageScaling: &api.StorageScalingSpec{
						EnableVolumeScaling: true,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(testScheme()).
				WithObjects(cr, sts, pvc, pod).
				WithStatusSubresource(pvc).
				Build()

			pvc.Status = corev1.PersistentVolumeClaimStatus{
				Capacity: corev1.ResourceList{
					corev1.ResourceStorage: actual.DeepCopy(),
				},
			}
			err := fakeClient.Status().Update(context.Background(), pvc)
			require.NoError(t, err)

			r := &ReconcilePerconaServerMongoDB{
				client: fakeClient,
				scheme: testScheme(),
			}

			volumeSpec := &api.VolumeSpec{
				PersistentVolumeClaim: api.PVCSpec{
					PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: requested.DeepCopy(),
							},
						},
					},
				},
			}

			ls := map[string]string{"app": "test"}

			_, resizeErr := r.resizeVolumesIfNeeded(t.Context(), cr, sts, ls, config.MongodDataVolClaimName, volumeSpec.PersistentVolumeClaim)

			stsKey := types.NamespacedName{Name: stsName, Namespace: ns}

			if tt.expectResize {
				updatedSTS := &appsv1.StatefulSet{}
				err := fakeClient.Get(t.Context(), stsKey, updatedSTS)
				if resizeErr != nil {
					assert.Contains(t, resizeErr.Error(), "less than actual")
				} else {
					require.NoError(t, err)
					assert.NotEmpty(t, updatedSTS.Annotations[api.AnnotationPVCResizeInProgress])
				}
			} else {
				assert.NoError(t, resizeErr)

				updatedSTS := &appsv1.StatefulSet{}
				err := fakeClient.Get(t.Context(), stsKey, updatedSTS)
				require.NoError(t, err)
				assert.Empty(t, updatedSTS.Annotations[api.AnnotationPVCResizeInProgress])
			}
		})
	}
}

func TestMaxPVCCapacity(t *testing.T) {
	labels := map[string]string{"app": "test"}
	sts := newStatefulSet("ns", "my-sts", labels, resource.MustParse("10Gi"))

	tests := []struct {
		name     string
		pvcs     []corev1.PersistentVolumeClaim
		expected string
	}{
		{
			name:     "no PVCs returns zero",
			pvcs:     nil,
			expected: "0",
		},
		{
			name: "single matching PVC",
			pvcs: []corev1.PersistentVolumeClaim{
				*newPVC("ns", config.MongodDataVolClaimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi")),
			},
			expected: "10Gi",
		},
		{
			name: "returns largest capacity among matching PVCs",
			pvcs: []corev1.PersistentVolumeClaim{
				*newPVC("ns", config.MongodDataVolClaimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi")),
				*newPVC("ns", config.MongodDataVolClaimName+"-my-sts-1", labels, resource.MustParse("10Gi"), resource.MustParse("15Gi")),
				*newPVC("ns", config.MongodDataVolClaimName+"-my-sts-2", labels, resource.MustParse("10Gi"), resource.MustParse("12Gi")),
			},
			expected: "15Gi",
		},
		{
			name: "skips non-matching PVCs",
			pvcs: []corev1.PersistentVolumeClaim{
				*newPVC("ns", config.MongodDataVolClaimName+"-my-sts-0", labels, resource.MustParse("5Gi"), resource.MustParse("5Gi")),
				*newPVC("ns", config.MongodDataVolClaimName+"-other-sts-0", labels, resource.MustParse("20Gi"), resource.MustParse("20Gi")),
			},
			expected: "5Gi",
		},
		{
			name: "skips PVCs with nil capacity",
			pvcs: []corev1.PersistentVolumeClaim{
				{
					Name: config.MongodDataVolClaimName + "-my-sts-0", Namespace: "ns", Labels: labels,
					Status: corev1.PersistentVolumeClaimStatus{},
				},
				*newPVC("ns", config.MongodDataVolClaimName+"-my-sts-1", labels, resource.MustParse("8Gi"), resource.MustParse("8Gi")),
			},
			expected: "8Gi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := maxPVCCapacity(tt.pvcs, config.MongodDataVolClaimName, sts)
			expected := resource.MustParse(tt.expected)
			assert.True(t, got.Equal(expected), "got %s, expected %s", got.String(), expected.String())
		})
	}
}

func TestIsPVCExpansionInProgress(t *testing.T) {
	labels := map[string]string{"app": "test"}
	sts := newStatefulSet("ns", "my-sts", labels, resource.MustParse("10Gi"))
	claimName := config.MongodDataVolClaimName

	tests := []struct {
		name       string
		pvcs       []corev1.PersistentVolumeClaim
		expanding  bool
		expectName string
	}{
		{
			name:      "no PVCs returns false",
			pvcs:      nil,
			expanding: false,
		},
		{
			name: "fully expanded PVC returns false",
			pvcs: []corev1.PersistentVolumeClaim{
				*newPVC("ns", claimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi")),
			},
			expanding: false,
		},
		{
			name: "spec.requests > status.capacity detected",
			pvcs: []corev1.PersistentVolumeClaim{
				*newPVC("ns", claimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi")),
				*newPVC("ns", claimName+"-my-sts-1", labels, resource.MustParse("15Gi"), resource.MustParse("10Gi")),
			},
			expanding:  true,
			expectName: claimName + "-my-sts-1",
		},
		{
			name: "FileSystemResizePending condition detected",
			pvcs: func() []corev1.PersistentVolumeClaim {
				pvc := newPVC("ns", claimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi"))
				pvc.Status.Conditions = []corev1.PersistentVolumeClaimCondition{
					{
						Type:   corev1.PersistentVolumeClaimFileSystemResizePending,
						Status: corev1.ConditionTrue,
					},
				}
				return []corev1.PersistentVolumeClaim{*pvc}
			}(),
			expanding:  true,
			expectName: claimName + "-my-sts-0",
		},
		{
			name: "Resizing condition detected",
			pvcs: func() []corev1.PersistentVolumeClaim {
				pvc := newPVC("ns", claimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi"))
				pvc.Status.Conditions = []corev1.PersistentVolumeClaimCondition{
					{
						Type:   corev1.PersistentVolumeClaimResizing,
						Status: corev1.ConditionTrue,
					},
				}
				return []corev1.PersistentVolumeClaim{*pvc}
			}(),
			expanding:  true,
			expectName: claimName + "-my-sts-0",
		},
		{
			name: "condition with status false is ignored",
			pvcs: func() []corev1.PersistentVolumeClaim {
				pvc := newPVC("ns", claimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi"))
				pvc.Status.Conditions = []corev1.PersistentVolumeClaimCondition{
					{
						Type:   corev1.PersistentVolumeClaimFileSystemResizePending,
						Status: corev1.ConditionFalse,
					},
				}
				return []corev1.PersistentVolumeClaim{*pvc}
			}(),
			expanding: false,
		},
		{
			name: "skips non-matching PVCs",
			pvcs: []corev1.PersistentVolumeClaim{
				*newPVC("ns", claimName+"-other-sts-0", labels, resource.MustParse("20Gi"), resource.MustParse("10Gi")),
				*newPVC("ns", claimName+"-my-sts-0", labels, resource.MustParse("10Gi"), resource.MustParse("10Gi")),
			},
			expanding: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, expanding := isPVCExpansionInProgress(tt.pvcs, claimName, sts)
			assert.Equal(t, tt.expanding, expanding)
			if tt.expanding {
				assert.Equal(t, tt.expectName, name)
			}
		})
	}
}

func TestShrinkStatusBlocksReconciliation(t *testing.T) {
	cr, err := readDefaultCR("some-cluster", "ns")
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)

	rs := cr.Spec.Replsets[0]
	rs.Size = 3
	requested := resource.MustParse("5Gi")
	rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: requested,
	}
	cr.Spec.StorageScaling = &api.StorageScalingSpec{
		EnableVolumeShrink: true,
	}

	labels := naming.MongodLabels(cr, rs)
	stsName := naming.MongodStatefulSetName(cr, rs)
	sts := newStatefulSet(cr.Namespace, stsName, labels, resource.MustParse("10Gi"))

	cr.Status.PVCShrink = map[string]api.PVCShrinkState{
		stsName: {
			TargetSize:    "5Gi",
			StartedAt:     time.Now().Format(time.RFC3339),
			Phase:         "secondaries",
			ProcessedPods: []string{},
			PrimaryPod:    stsName + "-0",
		},
	}

	pod := newPod(cr.Namespace, stsName+"-0", labels)
	pvc := newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, requested, resource.MustParse("10Gi"))

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, apis.AddToScheme(s))

	r := &ReconcilePerconaServerMongoDB{
		client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr, sts, pod, pvc).Build(),
		scheme: s,
	}

	err = r.reconcilePVCs(t.Context(), cr, sts)
	if err != nil {
		assert.NotContains(t, err.Error(), "requested storage")
	}
}

func TestShrinkRejectsSingleMember(t *testing.T) {
	cr, err := readDefaultCR("some-cluster", "ns")
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)

	rs := cr.Spec.Replsets[0]
	rs.Size = 1
	requested := resource.MustParse("5Gi")
	rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: requested,
	}
	cr.Spec.StorageScaling = &api.StorageScalingSpec{
		EnableVolumeShrink: true,
	}
	cr.Status.State = api.AppStateReady

	labels := naming.MongodLabels(cr, rs)
	stsName := naming.MongodStatefulSetName(cr, rs)
	sts := newStatefulSet(cr.Namespace, stsName, labels, resource.MustParse("10Gi"))

	pod := newPod(cr.Namespace, stsName+"-0", labels)
	pvc := newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, requested, resource.MustParse("10Gi"))

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, apis.AddToScheme(s))

	rec := events.NewFakeRecorder(10)
	r := &ReconcilePerconaServerMongoDB{
		client:   fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr, sts, pod, pvc).Build(),
		scheme:   s,
		recorder: rec,
	}

	err = r.reconcilePVCs(t.Context(), cr, sts)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "single-member replica set has no sync source")
	assert.Equal(t, []string{"Warning PVCShrinkRejected"}, eventReasons(rec))
}

func TestShrinkPreflightRevertsVCTWhenBlocked(t *testing.T) {
	cr, err := readDefaultCR("some-cluster", "ns")
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)

	rs := cr.Spec.Replsets[0]
	rs.Size = 3
	requested := resource.MustParse("5Gi")
	actual := resource.MustParse("10Gi")
	rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: requested,
	}
	cr.Spec.StorageScaling = &api.StorageScalingSpec{
		EnableVolumeShrink: true,
	}
	cr.Status.State = api.AppStateError

	labels := naming.MongodLabels(cr, rs)
	stsName := naming.MongodStatefulSetName(cr, rs)
	sts := newStatefulSet(cr.Namespace, stsName, labels, requested)

	pod := newPod(cr.Namespace, stsName+"-0", labels)
	pvc := newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, requested, actual)

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, apis.AddToScheme(s))

	r := &ReconcilePerconaServerMongoDB{
		client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr, sts, pod, pvc).Build(),
		scheme: s,
	}

	err = r.reconcilePVCs(t.Context(), cr, sts)
	require.NoError(t, err, "blocked shrink should return nil, not error")

	vctSize := sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, actual.String(), vctSize.String(), "VCT should be reverted to actual PVC size when shrink is blocked")
}

func TestShrinkingReplsetStatefulSet(t *testing.T) {
	cr, err := readDefaultCR("some-cluster", "ns")
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)
	rs := cr.Spec.Replsets[0]

	mongod := naming.MongodStatefulSetName(cr, rs)
	hidden := naming.HiddenStatefulSetName(cr, rs)
	nonVoting := naming.NonVotingStatefulSetName(cr, rs)
	state := api.PVCShrinkState{Phase: "secondaries", TargetSize: "3Gi"}

	scFor := func(stsName string) shrinkContext {
		return shrinkContext{
			cr:            cr,
			sts:           newStatefulSet(cr.Namespace, stsName, nil, resource.MustParse("3Gi")),
			replset:       rs,
			isRSComponent: true,
		}
	}

	t.Run("no shrink in progress", func(t *testing.T) {
		cr.Status.PVCShrink = nil
		assert.Empty(t, shrinkingReplsetStatefulSet(scFor(hidden)))
	})

	t.Run("own shrink is ignored", func(t *testing.T) {
		cr.Status.PVCShrink = nil
		cr.SetPVCShrinkState(hidden, state)
		assert.Empty(t, shrinkingReplsetStatefulSet(scFor(hidden)))
	})

	t.Run("mongod shrink blocks hidden and non-voting", func(t *testing.T) {
		cr.Status.PVCShrink = nil
		cr.SetPVCShrinkState(mongod, state)
		assert.Equal(t, mongod, shrinkingReplsetStatefulSet(scFor(hidden)))
		assert.Equal(t, mongod, shrinkingReplsetStatefulSet(scFor(nonVoting)))
	})

	t.Run("hidden shrink blocks mongod", func(t *testing.T) {
		cr.Status.PVCShrink = nil
		cr.SetPVCShrinkState(hidden, state)
		assert.Equal(t, hidden, shrinkingReplsetStatefulSet(scFor(mongod)))
	})

	t.Run("other replset does not block", func(t *testing.T) {
		cr.Status.PVCShrink = nil
		cr.SetPVCShrinkState(naming.MongodStatefulSetName(cr, &api.ReplsetSpec{Name: "rs1"}), state)
		assert.Empty(t, shrinkingReplsetStatefulSet(scFor(mongod)))
	})
}

func TestShrinkMongosShrinkEntryPath(t *testing.T) {
	cr, err := readDefaultCR("some-cluster", "ns")
	require.NoError(t, err)

	cr.Spec.Sharding.Enabled = true
	cr.Spec.StorageScaling = &api.StorageScalingSpec{
		EnableVolumeShrink: true,
	}
	cr.Status.State = api.AppStateError

	requested := resource.MustParse("2Gi")
	actual := resource.MustParse("3Gi")

	labels := naming.MongosLabels(cr)
	stsName := naming.MongosStatefulSetName(cr)

	sts := &appsv1.StatefulSet{
		Name:      stsName,
		Namespace: cr.Namespace,
		Labels:    labels,
		Spec: appsv1.StatefulSetSpec{
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					Name: config.MongosLogVolClaimName,
					Spec: corev1.PersistentVolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: actual,
							},
						},
					},
				},
			},
		},
	}

	pod := newPod(cr.Namespace, stsName+"-0", labels)
	pvc := newPVC(cr.Namespace, config.MongosLogVolClaimName+"-"+pod.Name, labels, requested, actual)

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, apis.AddToScheme(s))

	r := &ReconcilePerconaServerMongoDB{
		client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr, sts, pod, pvc).Build(),
		scheme: s,
	}

	pvcSpec := api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: requested,
				},
			},
		},
	}

	templates, err := r.resizeVolumesIfNeeded(t.Context(), cr, sts, labels, config.MongosLogVolClaimName, pvcSpec)
	require.NoError(t, err, "blocked mongos shrink should return nil")
	assert.Equal(t, sts.Spec.VolumeClaimTemplates, templates, "returned templates must preserve the shrink correction")

	vctSize := sts.Spec.VolumeClaimTemplates[0].Spec.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, actual.String(), vctSize.String(),
		"mongos VCT should be reverted to actual PVC size when shrink is blocked")
}

func TestResizeVolumesIfNeeded_VolumeResizeFailedEvent(t *testing.T) {
	t.Parallel()

	const (
		ns      = "test-ns"
		stsName = "my-cluster-rs0"
	)

	tests := map[string]struct {
		note                 string
		wantErr              bool
		wantRevertedCR       bool
		wantResizeInProgress bool
	}{
		"deadline exceeded is retried": {
			note:                 "resize volume \"pvc-1\" by resizer \"pd.csi.storage.gke.io\" failed: rpc error: code = DeadlineExceeded desc = context deadline exceeded",
			wantResizeInProgress: true,
		},
		"unavailable is retried": {
			note:                 "resize volume \"pvc-1\" by resizer \"pd.csi.storage.gke.io\" failed: rpc error: code = Unavailable desc = connection error",
			wantResizeInProgress: true,
		},
		"other errors revert the resize": {
			note:           "resize volume \"pvc-1\" by resizer \"pd.csi.storage.gke.io\" failed: rpc error: code = InvalidArgument desc = invalid size",
			wantErr:        true,
			wantRevertedCR: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			original := resource.MustParse("1Gi")
			requested := resource.MustParse("2Gi")
			ls := map[string]string{"app": "test"}
			pvcName := config.MongodDataVolClaimName + "-" + stsName + "-0"

			cr := &api.PerconaServerMongoDB{
				Name:      "my-cluster",
				Namespace: ns,
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					StorageScaling: &api.StorageScalingSpec{
						EnableVolumeScaling: true,
					},
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							VolumeSpec: &api.VolumeSpec{
								PersistentVolumeClaim: api.PVCSpec{
									PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{corev1.ResourceStorage: requested.DeepCopy()},
										},
									},
								},
							},
						},
					},
				},
			}

			sts := &appsv1.StatefulSet{
				Name:      stsName,
				Namespace: ns,
				Labels: map[string]string{
					naming.LabelKubernetesComponent: naming.ComponentMongod,
					naming.LabelKubernetesReplset:   "rs0",
				},
				Annotations: map[string]string{
					api.AnnotationPVCResizeInProgress: time.Now().Add(-time.Hour).Format(time.RFC3339),
				},
				Spec: appsv1.StatefulSetSpec{
					VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
						{
							Name: config.MongodDataVolClaimName,
							Spec: corev1.PersistentVolumeClaimSpec{
								Resources: corev1.VolumeResourceRequirements{
									Requests: corev1.ResourceList{corev1.ResourceStorage: original.DeepCopy()},
								},
							},
						},
					},
				},
			}

			pvc := &corev1.PersistentVolumeClaim{
				Name:      pvcName,
				Namespace: ns,
				Labels:    ls,
				Spec: corev1.PersistentVolumeClaimSpec{
					Resources: corev1.VolumeResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceStorage: requested.DeepCopy()},
					},
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: original.DeepCopy()},
				},
			}

			pod := &corev1.Pod{
				Name:      stsName + "-0",
				Namespace: ns,
				Labels:    ls,
			}

			event := &eventsv1.Event{
				Name:      pvcName + ".resize-failed",
				Namespace: ns,
				EventTime: metav1.NewMicroTime(time.Now()),
				Regarding: corev1.ObjectReference{Kind: "PersistentVolumeClaim", Name: pvcName, Namespace: ns},
				Reason:    "VolumeResizeFailed",
				Note:      tt.note,
			}

			cl := fake.NewClientBuilder().
				WithScheme(testScheme()).
				WithObjects(cr, sts, pvc, pod, event).
				WithStatusSubresource(pvc).
				WithIndex(&eventsv1.Event{}, "regarding.name", func(o client.Object) []string {
					return []string{o.(*eventsv1.Event).Regarding.Name}
				}).
				Build()

			r := &ReconcilePerconaServerMongoDB{
				client: cl,
				scheme: testScheme(),
			}

			_, err := r.resizeVolumesIfNeeded(t.Context(), cr, sts, ls, config.MongodDataVolClaimName, cr.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim)
			if tt.wantErr {
				assert.ErrorContains(t, err, "volume resize failed")
			} else {
				assert.NoError(t, err)
			}

			gotSts := &appsv1.StatefulSet{}
			require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(sts), gotSts))
			_, inProgress := gotSts.Annotations[api.AnnotationPVCResizeInProgress]
			assert.Equal(t, tt.wantResizeInProgress, inProgress)

			gotCR := &api.PerconaServerMongoDB{}
			require.NoError(t, cl.Get(t.Context(), client.ObjectKeyFromObject(cr), gotCR))
			wantSize := requested
			if tt.wantRevertedCR {
				wantSize = original
			}
			gotSize := gotCR.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Zero(t, wantSize.Cmp(gotSize), "want %s, got %s", wantSize.String(), gotSize.String())
		})
	}
}

func TestShrinkStepdownRetry(t *testing.T) {
	cr, err := readDefaultCR("some-cluster", "ns")
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)

	rs := cr.Spec.Replsets[0]
	rs.Size = 3

	labels := naming.MongodLabels(cr, rs)
	labels[naming.LabelKubernetesReplset] = rs.Name
	stsName := naming.MongodStatefulSetName(cr, rs)
	sts := newStatefulSet(cr.Namespace, stsName, labels, resource.MustParse("5Gi"))

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, apis.AddToScheme(s))

	pvcSpec := api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("5Gi"),
				},
			},
		},
	}

	sc := shrinkContext{
		cr:            cr,
		sts:           sts,
		ls:            labels,
		claimName:     config.MongodDataVolClaimName,
		pvcSpec:       pvcSpec,
		component:     naming.ComponentMongod,
		replset:       rs,
		isRSComponent: true,
		needsStepdown: true,
	}

	t.Run("isPodPrimary failure causes soft retry", func(t *testing.T) {
		primaryPod := newPod(cr.Namespace, stsName+"-0", labels)

		r := &ReconcilePerconaServerMongoDB{
			client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr, primaryPod).Build(),
			scheme: s,
		}

		state := api.PVCShrinkState{
			Phase:      "stepdown",
			PrimaryPod: primaryPod.Name,
		}
		cr.SetPVCShrinkState(stsName, state)

		err := r.shrinkStepdown(t.Context(), sc, state)
		require.NoError(t, err, "isPodPrimary error should return nil for soft retry")

		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, "stepdown", got.Phase, "phase should remain stepdown so next reconcile retries")
	})

	t.Run("pod not found advances to primary phase", func(t *testing.T) {
		r := &ReconcilePerconaServerMongoDB{
			client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr).Build(),
			scheme: s,
		}

		state := api.PVCShrinkState{
			Phase:      "stepdown",
			PrimaryPod: stsName + "-0",
		}
		cr.SetPVCShrinkState(stsName, state)

		err := r.shrinkStepdown(t.Context(), sc, state)
		require.NoError(t, err)

		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, "primary", got.Phase, "missing pod should advance to primary phase")
	})

	t.Run("needsStepdown false skips to primary phase", func(t *testing.T) {
		noStepdownSC := sc
		noStepdownSC.needsStepdown = false

		r := &ReconcilePerconaServerMongoDB{
			client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr).Build(),
			scheme: s,
		}

		state := api.PVCShrinkState{
			Phase:      "stepdown",
			PrimaryPod: stsName + "-0",
		}
		cr.SetPVCShrinkState(stsName, state)

		err := r.shrinkStepdown(t.Context(), noStepdownSC, state)
		require.NoError(t, err)

		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, "primary", got.Phase, "non-stepdown component should skip to primary phase")
	})
}

// TestResizeVolumesIfNeeded_IgnoresPVCsWithoutPods covers a replset that was
// scaled down with autoscaling on. The PVC of the member that went away is kept
// unless the delete-psmdb-pvc finalizer is set, and nothing ever resizes it
// because it has no pod. Counting it when working out the size the volumes
// actually have would leave the operator believing a resize is still owed: it
// re-annotates the statefulset, finds every PVC it can resize already at the
// requested size, deletes the statefulset to pick up the template, and starts
// over, which shows up as the cluster flapping between ready and error.
func TestResizeVolumesIfNeeded_IgnoresPVCsWithoutPods(t *testing.T) {
	t.Parallel()

	const (
		ns      = "test-ns"
		stsName = "my-cluster-rs0"
	)

	// rs0 went from 3 members to 2 and the surviving volumes have grown to 3Gi
	requested := resource.MustParse("3Gi")
	grown := resource.MustParse("3Gi")
	leftBehind := resource.MustParse("1Gi")

	ls := map[string]string{"app": "test"}

	sts := &appsv1.StatefulSet{
		Name:      stsName,
		Namespace: ns,
		Spec: appsv1.StatefulSetSpec{
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					Name: config.MongodDataVolClaimName,
					Spec: corev1.PersistentVolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{corev1.ResourceStorage: requested.DeepCopy()},
						},
					},
				},
			},
		},
	}

	newPVC := func(index int, capacity resource.Quantity) *corev1.PersistentVolumeClaim {
		return &corev1.PersistentVolumeClaim{
			Name:      fmt.Sprintf("%s-%s-%d", config.MongodDataVolClaimName, stsName, index),
			Namespace: ns,
			Labels:    ls,
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{corev1.ResourceStorage: capacity.DeepCopy()},
				},
			},
			Status: corev1.PersistentVolumeClaimStatus{
				Capacity: corev1.ResourceList{corev1.ResourceStorage: capacity.DeepCopy()},
			},
		}
	}

	newPod := func(index int) *corev1.Pod {
		return &corev1.Pod{
			Name:      fmt.Sprintf("%s-%d", stsName, index),
			Namespace: ns,
			Labels:    ls,
		}
	}

	pvcs := []*corev1.PersistentVolumeClaim{
		newPVC(0, grown),
		newPVC(1, grown),
		newPVC(2, leftBehind), // the member that was scaled away
	}

	cr := &api.PerconaServerMongoDB{
		Name:      "my-cluster",
		Namespace: ns,
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			StorageScaling: &api.StorageScalingSpec{
				EnableVolumeScaling: true,
			},
		},
	}

	objs := []client.Object{cr, sts, newPod(0), newPod(1)}
	for _, pvc := range pvcs {
		objs = append(objs, pvc)
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(objs...).
		WithStatusSubresource(pvcs[0], pvcs[1], pvcs[2]).
		Build()

	for _, pvc := range pvcs {
		require.NoError(t, fakeClient.Status().Update(t.Context(), pvc))
	}

	r := &ReconcilePerconaServerMongoDB{client: fakeClient, scheme: testScheme()}

	pvcSpec := api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: requested.DeepCopy()},
			},
		},
	}

	_, err := r.resizeVolumesIfNeeded(t.Context(), cr, sts, ls, config.MongodDataVolClaimName, pvcSpec)
	require.NoError(t, err)

	updatedSTS := &appsv1.StatefulSet{}
	require.NoError(t, fakeClient.Get(t.Context(), types.NamespacedName{Name: stsName, Namespace: ns}, updatedSTS))
	assert.Empty(t, updatedSTS.Annotations[api.AnnotationPVCResizeInProgress],
		"the volumes in use are already at the requested size, so no resize should start")

	kept := &corev1.PersistentVolumeClaim{}
	require.NoError(t, fakeClient.Get(t.Context(),
		types.NamespacedName{Name: pvcs[2].Name, Namespace: ns}, kept))
	size := kept.Spec.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, "1Gi", size.String(), "a PVC with no pod must be left alone")
}

func TestUnhealthyRSMember(t *testing.T) {
	member := func(name string, state mongo.MemberState) *mongo.Member {
		return &mongo.Member{Name: name, State: state}
	}

	tests := []struct {
		name      string
		members   []*mongo.Member
		replacing string
		want      string
	}{
		{
			name: "all healthy",
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary),
				member("rs0-1", mongo.MemberStateSecondary),
				member("rs0-arbiter-0", mongo.MemberStateArbiter),
			},
		},
		{
			name: "hidden member recovering",
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary),
				member("rs0-1", mongo.MemberStateSecondary),
				member("rs0-hidden-0", mongo.MemberStateRecovering),
			},
			want: "rs0-hidden-0",
		},
		{
			name: "member in initial sync",
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary),
				member("rs0-2", mongo.MemberStateStartup2),
			},
			want: "rs0-2",
		},
		{
			name: "member down",
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary),
				member("rs0-1", mongo.MemberStateDown),
			},
			want: "rs0-1",
		},
		{
			name: "other unhealthy member is reported before the replacing one",
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary),
				member("rs0-1", mongo.MemberStateStartup2),
				member("rs0-hidden-0", mongo.MemberStateRecovering),
			},
			replacing: "rs0-1",
			want:      "rs0-hidden-0",
		},
		{
			name: "replacing member is reported when it's the only unhealthy one",
			members: []*mongo.Member{
				member("rs0-0", mongo.MemberStatePrimary),
				member("rs0-1", mongo.MemberStateStartup2),
				member("rs0-hidden-0", mongo.MemberStateSecondary),
			},
			replacing: "rs0-1",
			want:      "rs0-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := unhealthyRSMember(mongo.Status{Members: tt.members}, tt.replacing)
			if tt.want == "" {
				assert.Nil(t, got)
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got.Name)
		})
	}
}

type shrinkMongoProvider struct {
	status       mongo.Status
	isPrimary    bool
	stepDowns    int
	rsDials      int
	rsRetryDials int
	dialErr      error
	statusErr    error
}

func (p *shrinkMongoProvider) Mongo(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error) {
	p.rsDials++
	conf := mongo.DialConfig{Backoff: mongo.DefaultBackoff()}
	for _, opt := range opts {
		opt(&conf)
	}
	if conf.Backoff != nil {
		p.rsRetryDials++
	}
	if p.dialErr != nil {
		return nil, p.dialErr
	}
	return &shrinkMongoClient{Client: mongoFake.NewClient(), p: p}, nil
}

func (p *shrinkMongoProvider) Mongos(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, opts ...mongo.ConfigOption) (mongo.Client, error) {
	return &shrinkMongoClient{Client: mongoFake.NewClient(), p: p}, nil
}

func (p *shrinkMongoProvider) Standalone(ctx context.Context, cr *api.PerconaServerMongoDB, role api.SystemUserRole, host string, tlsEnabled bool, opts ...mongo.ConfigOption) (mongo.Client, error) {
	return &shrinkMongoClient{Client: mongoFake.NewClient(), p: p}, nil
}

type shrinkMongoClient struct {
	mongo.Client
	p *shrinkMongoProvider
}

func (c *shrinkMongoClient) RSStatus(ctx context.Context) (mongo.Status, error) {
	if c.p.statusErr != nil {
		return mongo.Status{}, c.p.statusErr
	}
	return c.p.status, nil
}

func (c *shrinkMongoClient) IsMaster(ctx context.Context) (*mongo.IsMasterResp, error) {
	return &mongo.IsMasterResp{IsMaster: c.p.isPrimary}, nil
}

func (c *shrinkMongoClient) StepDown(ctx context.Context, seconds int, force bool) error {
	c.p.stepDowns++
	return nil
}

func TestShrinkPausesOnUnhealthyMember(t *testing.T) {
	cr, err := readDefaultCR("some-cluster", "ns")
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)

	rs := cr.Spec.Replsets[0]
	rs.Size = 3

	labels := naming.MongodLabels(cr, rs)
	labels[naming.LabelKubernetesReplset] = rs.Name
	stsName := naming.MongodStatefulSetName(cr, rs)
	sts := newStatefulSet(cr.Namespace, stsName, labels, resource.MustParse("3Gi"))

	s := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(s))
	require.NoError(t, apis.AddToScheme(s))

	sc := shrinkContext{
		cr:            cr,
		sts:           sts,
		ls:            labels,
		claimName:     config.MongodDataVolClaimName,
		component:     naming.ComponentMongod,
		replset:       rs,
		isRSComponent: true,
		needsStepdown: true,
	}

	healthy := mongo.Status{Members: []*mongo.Member{
		{Name: "rs0-0", State: mongo.MemberStatePrimary},
		{Name: "rs0-1", State: mongo.MemberStateSecondary},
		{Name: "rs0-2", State: mongo.MemberStateSecondary},
		{Name: "rs0-hidden-0", State: mongo.MemberStateSecondary},
	}}
	hiddenRecovering := mongo.Status{Members: []*mongo.Member{
		{Name: "rs0-0", State: mongo.MemberStatePrimary},
		{Name: "rs0-1", State: mongo.MemberStateSecondary},
		{Name: "rs0-2", State: mongo.MemberStateSecondary},
		{Name: "rs0-hidden-0", State: mongo.MemberStateRecovering},
	}}

	stsPod := func(i int) *corev1.Pod {
		podLabels := maps.Clone(labels)
		podLabels[appsv1.PodIndexLabel] = strconv.Itoa(i)
		return newPod(cr.Namespace, fmt.Sprintf("%s-%d", stsName, i), podLabels)
	}

	objects := func() []runtime.Object {
		objs := []runtime.Object{cr, sts}
		for i := range 3 {
			podName := fmt.Sprintf("%s-%d", stsName, i)
			objs = append(objs,
				stsPod(i),
				newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+podName, labels, resource.MustParse("5Gi"), resource.MustParse("5Gi")),
			)
		}
		return objs
	}

	pvcCount := func(t *testing.T, r *ReconcilePerconaServerMongoDB) int {
		pvcs := corev1.PersistentVolumeClaimList{}
		require.NoError(t, r.client.List(t.Context(), &pvcs, client.InNamespace(cr.Namespace)))
		return len(pvcs.Items)
	}

	secondariesState := api.PVCShrinkState{
		Phase:         "secondaries",
		TargetSize:    "3Gi",
		PrimaryPod:    stsName + "-0",
		ProcessedPods: []string{},
	}

	newReconciler := func(p *shrinkMongoProvider) (*ReconcilePerconaServerMongoDB, *events.FakeRecorder) {
		rec := events.NewFakeRecorder(10)
		return &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objects()...).Build(),
			scheme:              s,
			mongoClientProvider: p,
			recorder:            rec,
		}, rec
	}

	t.Run("secondaries phase keeps PVC while a member is unhealthy", func(t *testing.T) {
		r, rec := newReconciler(&shrinkMongoProvider{status: hiddenRecovering})
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		assert.Equal(t, 3, pvcCount(t, r))

		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Contains(t, got.WaitingFor, "rs0-hidden-0")

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, got, false))
		assert.Equal(t, []string{"Warning PVCShrinkWaiting"}, eventReasons(rec), "waiting event should be emitted once")
	})

	t.Run("secondaries phase deletes PVC when all members are healthy", func(t *testing.T) {
		p := &shrinkMongoProvider{status: healthy}
		r, rec := newReconciler(p)
		state := secondariesState
		state.WaitingFor = "member rs0-hidden-0 is RECOVERING"
		cr.SetPVCShrinkState(stsName, state)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, state, false))
		assert.Equal(t, 2, pvcCount(t, r))
		assert.Equal(t, []string{"Normal PVCShrinkReplacingMember"}, eventReasons(rec))
		assert.Positive(t, p.rsDials)
		assert.Zero(t, p.rsRetryDials, "health check must not block the reconcile on retries")

		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Empty(t, got.WaitingFor)
	})

	stepdownState := api.PVCShrinkState{
		Phase:      "stepdown",
		TargetSize: "3Gi",
		PrimaryPod: stsName + "-0",
	}

	t.Run("stepdown waits while a member is unhealthy", func(t *testing.T) {
		p := &shrinkMongoProvider{status: hiddenRecovering, isPrimary: true}
		r, rec := newReconciler(p)
		cr.SetPVCShrinkState(stsName, stepdownState)

		require.NoError(t, r.shrinkStepdown(t.Context(), sc, stepdownState))
		assert.Zero(t, p.stepDowns)
		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, "stepdown", got.Phase)
		assert.Equal(t, []string{"Warning PVCShrinkWaiting"}, eventReasons(rec))
	})

	t.Run("stepdown proceeds when all members are healthy", func(t *testing.T) {
		p := &shrinkMongoProvider{status: healthy, isPrimary: true}
		r, rec := newReconciler(p)
		cr.SetPVCShrinkState(stsName, stepdownState)

		require.NoError(t, r.shrinkStepdown(t.Context(), sc, stepdownState))
		assert.Equal(t, 1, p.stepDowns)
		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, "primary", got.Phase)
		assert.Equal(t, []string{"Normal PVCShrinkStepDown"}, eventReasons(rec))
	})

	t.Run("primary phase completes shrink", func(t *testing.T) {
		r, rec := newReconciler(&shrinkMongoProvider{status: healthy})
		state := api.PVCShrinkState{
			Phase:         "primary",
			TargetSize:    "3Gi",
			PrimaryPod:    stsName + "-0",
			ProcessedPods: []string{stsName + "-0", stsName + "-1", stsName + "-2"},
		}
		cr.SetPVCShrinkState(stsName, state)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, state, true))
		_, ok := cr.GetPVCShrinkState(stsName)
		assert.False(t, ok)
		assert.Equal(t, []string{"Normal PVCShrinkCompleted"}, eventReasons(rec))
	})

	// Pods already recreated with the new size and Ready: the readiness probe reports a freshly
	// wiped member Ready until it receives the replset config, so shrink must check the member state.
	host := func(podName string) string {
		h, err := psmdb.MongoHost(t.Context(), nil, cr, cr.Spec.ClusterServiceDNSMode, rs, rs.Expose.Enabled, *newPod(cr.Namespace, podName, labels))
		require.NoError(t, err)
		return h
	}
	replaced := func(t *testing.T, p *shrinkMongoProvider) (*ReconcilePerconaServerMongoDB, *events.FakeRecorder) {
		objs := []runtime.Object{cr, sts}
		for i := range 3 {
			pod := stsPod(i)
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
			objs = append(objs, pod, newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, resource.MustParse("3Gi"), resource.MustParse("3Gi")))
		}
		rec := events.NewFakeRecorder(10)
		return &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build(),
			scheme:              s,
			mongoClientProvider: p,
			recorder:            rec,
		}, rec
	}
	statusWith := func(syncing string, state mongo.MemberState) mongo.Status {
		status := mongo.Status{}
		for i := range 3 {
			name := host(fmt.Sprintf("%s-%d", stsName, i))
			st := mongo.MemberStateSecondary
			if i == 0 {
				st = mongo.MemberStatePrimary
			}
			if name == syncing {
				st = state
			}
			status.Members = append(status.Members, &mongo.Member{Name: name, State: st})
		}
		return status
	}

	t.Run("replaced member is not done while in initial sync", func(t *testing.T) {
		syncing := host(stsName + "-2")
		r, rec := replaced(t, &shrinkMongoProvider{status: statusWith(syncing, mongo.MemberStateStartup2)})
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Empty(t, got.ProcessedPods)
		assert.Contains(t, got.WaitingFor, "STARTUP2")
		assert.Empty(t, eventReasons(rec), "syncing member being replaced is expected, no warning")
	})

	t.Run("replaced member is done once it is secondary", func(t *testing.T) {
		r, _ := replaced(t, &shrinkMongoProvider{status: statusWith("", 0)})
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, "stepdown", got.Phase)
		assert.ElementsMatch(t, []string{stsName + "-1", stsName + "-2"}, got.ProcessedPods)
	})

	t.Run("primary phase does not complete while last member is in initial sync", func(t *testing.T) {
		syncing := host(stsName + "-0")
		status := statusWith(syncing, mongo.MemberStateStartup2)
		status.Members[1].State = mongo.MemberStatePrimary
		r, rec := replaced(t, &shrinkMongoProvider{status: status})
		state := api.PVCShrinkState{
			Phase:         "primary",
			TargetSize:    "3Gi",
			PrimaryPod:    stsName + "-0",
			ProcessedPods: []string{stsName + "-1", stsName + "-2"},
		}
		cr.SetPVCShrinkState(stsName, state)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, state, true))
		_, ok := cr.GetPVCShrinkState(stsName)
		assert.True(t, ok, "shrink must not complete")
		assert.Empty(t, eventReasons(rec))
	})

	t.Run("other unhealthy member while waiting for replaced member emits warning", func(t *testing.T) {
		r, rec := replaced(t, &shrinkMongoProvider{status: hiddenRecovering})
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Empty(t, got.ProcessedPods)
		assert.Equal(t, []string{"Warning PVCShrinkWaiting"}, eventReasons(rec))
	})

	t.Run("other unhealthy member is reported while replaced member syncs", func(t *testing.T) {
		status := statusWith(host(stsName+"-2"), mongo.MemberStateStartup2)
		status.Members = append(status.Members, &mongo.Member{Name: "rs0-hidden-0", State: mongo.MemberStateRecovering})
		r, rec := replaced(t, &shrinkMongoProvider{status: status})
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Contains(t, got.WaitingFor, "rs0-hidden-0")
		assert.Equal(t, []string{"Warning PVCShrinkWaiting"}, eventReasons(rec))
	})

	t.Run("member health is checked while replaced pod is not ready", func(t *testing.T) {
		objs := []runtime.Object{cr, sts}
		for i := range 3 {
			pod := stsPod(i)
			if i != 2 {
				pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
			}
			objs = append(objs, pod, newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, resource.MustParse("3Gi"), resource.MustParse("3Gi")))
		}
		status := statusWith(host(stsName+"-2"), mongo.MemberStateStartup2)
		status.Members = append(status.Members, &mongo.Member{Name: "rs0-hidden-0", State: mongo.MemberStateRecovering})
		rec := events.NewFakeRecorder(10)
		r := &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build(),
			scheme:              s,
			mongoClientProvider: &shrinkMongoProvider{status: status},
			recorder:            rec,
		}
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Empty(t, got.ProcessedPods)
		assert.Contains(t, got.WaitingFor, "rs0-hidden-0")
		assert.Equal(t, []string{"Warning PVCShrinkWaiting"}, eventReasons(rec))
	})

	t.Run("replaced member stays processed when next member is replaced in the same reconcile", func(t *testing.T) {
		objs := []runtime.Object{cr, sts}
		for i := range 3 {
			pod := stsPod(i)
			pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
			size := resource.MustParse("5Gi")
			if i == 2 {
				size = resource.MustParse("3Gi")
			}
			objs = append(objs, pod, newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, size, size))
		}
		rec := events.NewFakeRecorder(10)
		r := &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build(),
			scheme:              s,
			mongoClientProvider: &shrinkMongoProvider{status: statusWith("", 0)},
			recorder:            rec,
		}
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		assert.Equal(t, 2, pvcCount(t, r), "rs0-1 PVC should be deleted")

		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, []string{stsName + "-2"}, got.ProcessedPods)
	})

	t.Run("waits without connecting while statefulset is being recreated", func(t *testing.T) {
		var objs []runtime.Object
		for _, o := range objects() {
			if _, ok := o.(*appsv1.StatefulSet); !ok {
				objs = append(objs, o)
			}
		}
		p := &shrinkMongoProvider{status: healthy}
		rec := events.NewFakeRecorder(10)
		r := &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build(),
			scheme:              s,
			mongoClientProvider: p,
			recorder:            rec,
		}
		state := secondariesState
		state.WaitingFor = "member rs0-hidden-0 is RECOVERING"
		cr.SetPVCShrinkState(stsName, state)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, state, false))
		assert.Equal(t, 3, pvcCount(t, r))
		assert.Zero(t, p.rsDials, "replset client has no hosts without the statefulset")

		got, ok := cr.GetPVCShrinkState(stsName)
		require.True(t, ok)
		assert.Equal(t, "statefulset is being recreated", got.WaitingFor)
		assert.Empty(t, eventReasons(rec), "every shrink waits for the statefulset, no warning")
	})

	for name, p := range map[string]*shrinkMongoProvider{
		"connection failure": {status: healthy, dialErr: errors.New("auth failed")},
		"RS status failure":  {status: healthy, statusErr: errors.New("no primary")},
	} {
		t.Run(name+" is recorded as waiting reason", func(t *testing.T) {
			r, rec := newReconciler(p)
			state := secondariesState
			state.WaitingFor = "member rs0-hidden-0 is RECOVERING"
			cr.SetPVCShrinkState(stsName, state)

			require.NoError(t, r.shrinkProcessPods(t.Context(), sc, state, false))
			assert.Equal(t, 3, pvcCount(t, r))

			got, ok := cr.GetPVCShrinkState(stsName)
			require.True(t, ok)
			assert.NotEqual(t, state.WaitingFor, got.WaitingFor, "stale member reason must be replaced")
			assert.NotEmpty(t, got.WaitingFor)
			assert.NotContains(t, got.WaitingFor, "auth failed", "raw errors would change the reason on every reconcile")
			assert.NotContains(t, got.WaitingFor, "no primary", "raw errors would change the reason on every reconcile")

			require.NoError(t, r.shrinkProcessPods(t.Context(), sc, got, false))
			assert.Equal(t, []string{"Warning PVCShrinkWaiting"}, eventReasons(rec), "waiting event should be emitted once")
		})
	}

	t.Run("PVC already being deleted is not deleted again", func(t *testing.T) {
		objs := objects()
		for _, o := range objs {
			if pvc, ok := o.(*corev1.PersistentVolumeClaim); ok && pvc.Name == config.MongodDataVolClaimName+"-"+stsName+"-2" {
				now := metav1.Now()
				pvc.DeletionTimestamp = &now
				pvc.Finalizers = []string{"kubernetes.io/pvc-protection"}
			}
		}
		rec := events.NewFakeRecorder(10)
		r := &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(objs...).Build(),
			scheme:              s,
			mongoClientProvider: &shrinkMongoProvider{status: healthy},
			recorder:            rec,
		}
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		assert.Equal(t, 3, pvcCount(t, r))
		assert.Empty(t, eventReasons(rec))
	})

	terminatingPVC := func(podReady bool) []runtime.Object {
		objs := objects()
		for _, o := range objs {
			switch obj := o.(type) {
			case *corev1.PersistentVolumeClaim:
				if obj.Name == config.MongodDataVolClaimName+"-"+stsName+"-2" {
					now := metav1.Now()
					obj.DeletionTimestamp = &now
					obj.Finalizers = []string{"kubernetes.io/pvc-protection"}
				}
			case *corev1.Pod:
				if podReady && obj.Name == stsName+"-2" {
					obj.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
				}
			}
		}
		return objs
	}
	podExists := func(t *testing.T, r *ReconcilePerconaServerMongoDB, name string) bool {
		err := r.client.Get(t.Context(), types.NamespacedName{Namespace: cr.Namespace, Name: name}, &corev1.Pod{})
		if k8serrors.IsNotFound(err) {
			return false
		}
		require.NoError(t, err)
		return true
	}

	t.Run("running pod of a terminating PVC is kept while a member is unhealthy", func(t *testing.T) {
		rec := events.NewFakeRecorder(10)
		r := &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(terminatingPVC(true)...).Build(),
			scheme:              s,
			mongoClientProvider: &shrinkMongoProvider{status: hiddenRecovering},
			recorder:            rec,
		}
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		assert.True(t, podExists(t, r, stsName+"-2"))
		assert.Equal(t, []string{"Warning PVCShrinkWaiting"}, eventReasons(rec))
	})

	t.Run("running pod of a terminating PVC is deleted when all members are healthy", func(t *testing.T) {
		r := &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(terminatingPVC(true)...).Build(),
			scheme:              s,
			mongoClientProvider: &shrinkMongoProvider{status: healthy},
			recorder:            &events.FakeRecorder{},
		}
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		assert.False(t, podExists(t, r, stsName+"-2"))
	})

	t.Run("not ready pod of a terminating PVC is deleted while a member is unhealthy", func(t *testing.T) {
		r := &ReconcilePerconaServerMongoDB{
			client:              fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(terminatingPVC(false)...).Build(),
			scheme:              s,
			mongoClientProvider: &shrinkMongoProvider{status: hiddenRecovering},
			recorder:            &events.FakeRecorder{},
		}
		cr.SetPVCShrinkState(stsName, secondariesState)

		require.NoError(t, r.shrinkProcessPods(t.Context(), sc, secondariesState, false))
		assert.False(t, podExists(t, r, stsName+"-2"))
	})
}

// eventReasons returns "<type> <reason>" of every event recorded so far.
func eventReasons(rec *events.FakeRecorder) []string {
	var reasons []string
	for {
		select {
		case e := <-rec.Events:
			fields := strings.Fields(e)
			reasons = append(reasons, strings.Join(fields[:2], " "))
		default:
			return reasons
		}
	}
}
