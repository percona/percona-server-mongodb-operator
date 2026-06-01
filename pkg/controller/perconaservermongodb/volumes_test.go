package perconaservermongodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestResizeVolumesIfNeeded_NoSpuriousResizeOnDecimalUnits(t *testing.T) {
	err := apis.AddToScheme(scheme.Scheme)
	require.NoError(t, err)

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
				ObjectMeta: metav1.ObjectMeta{
					Name:      stsName,
					Namespace: ns,
				},
				Spec: appsv1.StatefulSetSpec{
					VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
						{
							ObjectMeta: metav1.ObjectMeta{
								Name: config.MongodDataVolClaimName,
							},
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
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: ns,
					Labels: map[string]string{
						"app": "test",
					},
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
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: ns,
					Labels: map[string]string{
						"app": "test",
					},
				},
			}

			cr := &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cluster",
					Namespace: ns,
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					StorageScaling: &api.StorageScalingSpec{
						EnableVolumeScaling: true,
					},
				},
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme.Scheme).
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
				scheme: scheme.Scheme,
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

			resizeErr := r.resizeVolumesIfNeeded(t.Context(), cr, sts, ls, volumeSpec)

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

func TestRoundUpGiB(t *testing.T) {
	tests := []struct {
		name     string
		input    int64
		expected int64
	}{
		{
			name:     "exactly 1 GiB",
			input:    GiB,
			expected: 1,
		},
		{
			name:     "1 byte over 1 GiB",
			input:    GiB + 1,
			expected: 2,
		},
		{
			name:     "1 GB (decimal) rounds up to 1 GiB",
			input:    1000 * 1000 * 1000,
			expected: 1,
		},
		{
			name:     "zero",
			input:    0,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := RoundUpGiB(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}
