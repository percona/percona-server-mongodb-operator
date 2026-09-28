package perconaservermongodb

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	psmdbconfig "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func TestShouldTriggerResize(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{}

	tests := []struct {
		name     string
		cr       *api.PerconaServerMongoDB
		pvc      *corev1.PersistentVolumeClaim
		usage    *PVCUsage
		expected bool
	}{
		{
			name: "usage above threshold",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					StorageScaling: &api.StorageScalingSpec{
						Autoscaling: &api.AutoscalingSpec{
							Enabled:                 true,
							TriggerThresholdPercent: 80,
							GrowthStep:              resource.MustParse("2Gi"),
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			usage: &PVCUsage{
				UsagePercent: 85,
			},
			expected: true,
		},
		{
			name: "usage below threshold",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					StorageScaling: &api.StorageScalingSpec{
						Autoscaling: &api.AutoscalingSpec{
							Enabled:                 true,
							TriggerThresholdPercent: 80,
							GrowthStep:              resource.MustParse("2Gi"),
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			usage: &PVCUsage{
				UsagePercent: 75,
			},
			expected: false,
		},
		{
			name: "at maxSize",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					StorageScaling: &api.StorageScalingSpec{
						Autoscaling: &api.AutoscalingSpec{
							Enabled:                 true,
							TriggerThresholdPercent: 80,
							GrowthStep:              resource.MustParse("2Gi"),
							MaxSize:                 resource.MustParse("10Gi"),
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			usage: &PVCUsage{
				UsagePercent: 85,
			},
			expected: false,
		},
		{
			name: "resize in progress",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					StorageScaling: &api.StorageScalingSpec{
						Autoscaling: &api.AutoscalingSpec{
							Enabled:                 true,
							TriggerThresholdPercent: 80,
							GrowthStep:              resource.MustParse("2Gi"),
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
					Conditions: []corev1.PersistentVolumeClaimCondition{
						{
							Type:   corev1.PersistentVolumeClaimResizing,
							Status: corev1.ConditionTrue,
						},
					},
				},
			},
			usage: &PVCUsage{
				UsagePercent: 85,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.shouldTriggerResize(t.Context(), tt.cr, tt.pvc, tt.usage)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestCalculateNewSize(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{}

	tests := []struct {
		name       string
		cr         *api.PerconaServerMongoDB
		pvc        *corev1.PersistentVolumeClaim
		expectedGi string
	}{
		{
			name: "add 2Gi",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					StorageScaling: &api.StorageScalingSpec{
						Autoscaling: &api.AutoscalingSpec{
							GrowthStep: resource.MustParse("2Gi"),
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			expectedGi: "12Gi",
		},
		{
			name: "add 5Gi",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					StorageScaling: &api.StorageScalingSpec{
						Autoscaling: &api.AutoscalingSpec{
							GrowthStep: resource.MustParse("5Gi"),
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			expectedGi: "15Gi",
		},
		{
			name: "enforce maxSize",
			cr: &api.PerconaServerMongoDB{
				Spec: api.PerconaServerMongoDBSpec{
					StorageScaling: &api.StorageScalingSpec{
						Autoscaling: &api.AutoscalingSpec{
							GrowthStep: resource.MustParse("10Gi"),
							MaxSize:    resource.MustParse("15Gi"),
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			expectedGi: "15Gi", // capped at maxSize, not 20
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := r.calculateNewSize(tt.cr, tt.pvc)
			expected := resource.MustParse(tt.expectedGi)

			assert.Equal(t, expected.Value(), result.Value())
		})
	}
}

func TestExtractPodNameFromPVC(t *testing.T) {
	tests := []struct {
		name     string
		pvcName  string
		stsName  string
		expected string
	}{
		{
			name:     "standard PVC name",
			pvcName:  "mongod-data-my-cluster-rs0-0",
			stsName:  "my-cluster-rs0",
			expected: "my-cluster-rs0-0",
		},
		{
			name:     "config server PVC",
			pvcName:  "mongod-data-my-cluster-cfg-0",
			stsName:  "my-cluster-cfg",
			expected: "my-cluster-cfg-0",
		},
		{
			name:     "invalid PVC name",
			pvcName:  "other-volume-claim",
			stsName:  "my-cluster-rs0",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPodNameFromPVC(tt.pvcName, tt.stsName)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFindPodByName(t *testing.T) {
	podList := &corev1.PodList{
		Items: []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cluster-rs0-0",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "my-cluster-rs0-1",
				},
			},
		},
	}

	tests := []struct {
		name     string
		podName  string
		expected bool
	}{
		{
			name:     "pod exists",
			podName:  "my-cluster-rs0-0",
			expected: true,
		},
		{
			name:     "pod not found",
			podName:  "my-cluster-rs0-2",
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := findPodByName(podList, tt.podName)
			if tt.expected {
				assert.NotNil(t, pod)
				assert.Equal(t, tt.podName, pod.Name)
			} else {
				assert.Nil(t, pod)
			}
		})
	}
}

func TestUpdateAutoscalingStatus(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{}

	tests := map[string]struct {
		cr             *api.PerconaServerMongoDB
		pvcName        string
		usage          *PVCUsage
		err            error
		expectedStatus api.StorageAutoscalingStatus
		checkResizeInc bool
	}{
		"initialize nil map and set usage": {
			cr: &api.PerconaServerMongoDB{
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: nil,
				},
			},
			pvcName: "mongod-data-test-rs0-0",
			usage: &PVCUsage{
				TotalBytes:   10 * 1024 * 1024 * 1024, // 10Gi
				UsagePercent: 50,
			},
			expectedStatus: api.StorageAutoscalingStatus{
				CurrentSize: "10Gi",
				LastError:   "",
				ResizeCount: 0,
			},
		},
		"set error only": {
			cr: &api.PerconaServerMongoDB{
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: nil,
				},
			},
			pvcName: "mongod-data-test-rs0-0",
			err:     errors.New("failed to get metrics"),
			expectedStatus: api.StorageAutoscalingStatus{
				LastError: "failed to get metrics",
			},
		},
		"size increased - should increment resize count": {
			cr: &api.PerconaServerMongoDB{
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: map[string]api.StorageAutoscalingStatus{
						"mongod-data-test-rs0-0": {
							CurrentSize: "10Gi",
							ResizeCount: 1,
						},
					},
				},
			},
			pvcName: "mongod-data-test-rs0-0",
			usage: &PVCUsage{
				TotalBytes:   15 * 1024 * 1024 * 1024, // 15Gi
				UsagePercent: 60,
			},
			expectedStatus: api.StorageAutoscalingStatus{
				CurrentSize: "15Gi",
				LastError:   "",
				ResizeCount: 2,
			},
			checkResizeInc: true,
		},
		"size unchanged - should not increment resize count": {
			cr: &api.PerconaServerMongoDB{
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: map[string]api.StorageAutoscalingStatus{
						"mongod-data-test-rs0-0": {
							CurrentSize: "10Gi",
							ResizeCount: 1,
						},
					},
				},
			},
			pvcName: "mongod-data-test-rs0-0",
			usage: &PVCUsage{
				TotalBytes:   10 * 1024 * 1024 * 1024, // 10Gi (same)
				UsagePercent: 75,
			},
			expectedStatus: api.StorageAutoscalingStatus{
				CurrentSize: "10Gi",
				LastError:   "",
				ResizeCount: 1,
			},
		},
		"usage clears previous error": {
			cr: &api.PerconaServerMongoDB{
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: map[string]api.StorageAutoscalingStatus{
						"mongod-data-test-rs0-0": {
							CurrentSize: "10Gi",
							LastError:   "previous error",
							ResizeCount: 1,
						},
					},
				},
			},
			pvcName: "mongod-data-test-rs0-0",
			usage: &PVCUsage{
				TotalBytes:   10 * 1024 * 1024 * 1024,
				UsagePercent: 50,
			},
			expectedStatus: api.StorageAutoscalingStatus{
				CurrentSize: "10Gi",
				LastError:   "",
				ResizeCount: 1,
			},
		},
		"error preserves existing usage info": {
			cr: &api.PerconaServerMongoDB{
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: map[string]api.StorageAutoscalingStatus{
						"mongod-data-test-rs0-0": {
							CurrentSize: "10Gi",
							ResizeCount: 2,
						},
					},
				},
			},
			pvcName: "mongod-data-test-rs0-0",
			err:     errors.New("connection refused"),
			expectedStatus: api.StorageAutoscalingStatus{
				CurrentSize: "10Gi",
				LastError:   "connection refused",
				ResizeCount: 2,
			},
		},
		"new PVC status added to existing map": {
			cr: &api.PerconaServerMongoDB{
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: map[string]api.StorageAutoscalingStatus{
						"mongod-data-test-rs0-0": {
							CurrentSize: "10Gi",
							ResizeCount: 1,
						},
					},
				},
			},
			pvcName: "mongod-data-test-rs0-1",
			usage: &PVCUsage{
				TotalBytes:   20 * 1024 * 1024 * 1024,
				UsagePercent: 40,
			},
			expectedStatus: api.StorageAutoscalingStatus{
				CurrentSize: "20Gi",
				LastError:   "",
				ResizeCount: 0,
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r.updateAutoscalingStatus(t.Context(), tt.cr, tt.pvcName, tt.usage, tt.err)

			require.NotNil(t, tt.cr.Status.StorageAutoscaling)
			status, ok := tt.cr.Status.StorageAutoscaling[tt.pvcName]
			require.True(t, ok)

			assert.Equal(t, tt.expectedStatus.CurrentSize, status.CurrentSize)
			assert.Equal(t, tt.expectedStatus.LastError, status.LastError)
			assert.Equal(t, tt.expectedStatus.ResizeCount, status.ResizeCount)

			if tt.checkResizeInc {
				assert.False(t, status.LastResizeTime.IsZero())
			}
		})
	}
}

func TestTriggerResize(t *testing.T) {
	tests := map[string]struct {
		cr             *api.PerconaServerMongoDB
		pvc            *corev1.PersistentVolumeClaim
		newSize        resource.Quantity
		expectedResize int32
	}{
		"successful resize for replset": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							VolumeSpec: &api.VolumeSpec{
								PersistentVolumeClaim: api.PVCSpec{
									PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceStorage: resource.MustParse("10Gi"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mongod-data-test-cluster-rs0-0",
					Namespace: "default",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			newSize:        resource.MustParse("15Gi"),
			expectedResize: 1,
		},
		"successful resize for sharding config": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Sharding: api.Sharding{
						Enabled: true,
						ConfigsvrReplSet: &api.ReplsetSpec{
							Name: "cfg",
							VolumeSpec: &api.VolumeSpec{
								PersistentVolumeClaim: api.PVCSpec{
									PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceStorage: resource.MustParse("5Gi"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mongod-data-test-cluster-cfg-0",
					Namespace: "default",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("5Gi"),
					},
				},
			},
			newSize:        resource.MustParse("8Gi"),
			expectedResize: 1,
		},
		"multiple resizes increment counter": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							VolumeSpec: &api.VolumeSpec{
								PersistentVolumeClaim: api.PVCSpec{
									PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceStorage: resource.MustParse("10Gi"),
											},
										},
									},
								},
							},
						},
					},
				},
				Status: api.PerconaServerMongoDBStatus{
					StorageAutoscaling: map[string]api.StorageAutoscalingStatus{
						"mongod-data-test-cluster-rs0-0": {
							ResizeCount: 2,
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mongod-data-test-cluster-rs0-0",
					Namespace: "default",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			newSize:        resource.MustParse("15Gi"),
			expectedResize: 3,
		},
		"resize with multiple replsets": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					Replsets: []*api.ReplsetSpec{
						{
							Name: "rs0",
							VolumeSpec: &api.VolumeSpec{
								PersistentVolumeClaim: api.PVCSpec{
									PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceStorage: resource.MustParse("10Gi"),
											},
										},
									},
								},
							},
						},
						{
							Name: "rs1",
							VolumeSpec: &api.VolumeSpec{
								PersistentVolumeClaim: api.PVCSpec{
									PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
										Resources: corev1.VolumeResourceRequirements{
											Requests: corev1.ResourceList{
												corev1.ResourceStorage: resource.MustParse("10Gi"),
											},
										},
									},
								},
							},
						},
					},
				},
			},
			pvc: &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mongod-data-test-cluster-rs0-0",
					Namespace: "default",
				},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("10Gi"),
					},
				},
			},
			newSize:        resource.MustParse("15Gi"),
			expectedResize: 1,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := scheme.Scheme
			err := apis.AddToScheme(s)
			require.NoError(t, err)

			fakeClient := fake.NewClientBuilder().
				WithScheme(s).
				WithObjects(tt.cr).
				Build()

			r := &ReconcilePerconaServerMongoDB{
				client: fakeClient,
			}

			var volumeSpec *api.VolumeSpec
			if len(tt.cr.Spec.Replsets) > 0 {
				volumeSpec = tt.cr.Spec.Replsets[0].VolumeSpec
			} else if tt.cr.Spec.Sharding.Enabled {
				volumeSpec = tt.cr.Spec.Sharding.ConfigsvrReplSet.VolumeSpec
			}
			require.NotNil(t, volumeSpec)

			originalSize := volumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]

			err = r.triggerResize(t.Context(), tt.cr, tt.pvc, tt.newSize, volumeSpec)
			require.NoError(t, err)

			updatedSize := volumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.newSize.Value(), updatedSize.Value())
			assert.NotEqual(t, originalSize.Value(), updatedSize.Value())
		})
	}
}

// TestReconcileStorageAutoscalingComponents checks that autoscaling is applied
// to every replset component that owns a mongod-data PVC. Hidden, non-voting
// and arbiter pods run mongod in a container named after their component, and
// probing a hardcoded "mongod" container silently skipped their PVCs.
func TestReconcileStorageAutoscalingComponents(t *testing.T) {
	ctx := context.Background()

	const (
		crName    = "test-cluster"
		namespace = "default"
		rsName    = "rs0"
	)

	newCR := func() *api.PerconaServerMongoDB {
		volumeSpec := func() *api.VolumeSpec {
			return &api.VolumeSpec{
				PersistentVolumeClaim: api.PVCSpec{
					PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("10Gi"),
							},
						},
					},
				},
			}
		}

		return &api.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: namespace},
			Spec: api.PerconaServerMongoDBSpec{
				StorageScaling: &api.StorageScalingSpec{
					EnableVolumeScaling: true,
					Autoscaling: &api.AutoscalingSpec{
						Enabled:                 true,
						TriggerThresholdPercent: 80,
						GrowthStep:              resource.MustParse("2Gi"),
					},
				},
				Replsets: []*api.ReplsetSpec{
					{
						Name:       rsName,
						VolumeSpec: volumeSpec(),
						NonVoting: api.NonVotingSpec{
							Enabled:    true,
							VolumeSpec: volumeSpec(),
						},
						Hidden: api.HiddenSpec{
							Enabled:    true,
							VolumeSpec: volumeSpec(),
						},
					},
				},
			},
		}
	}

	tests := map[string]struct {
		labels        func(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) map[string]string
		stsName       string
		containerName string
		volumeSpec    func(rs *api.ReplsetSpec) *api.VolumeSpec
	}{
		"mongod": {
			labels:        naming.MongodLabels,
			stsName:       crName + "-" + rsName,
			containerName: naming.ContainerMongod,
			volumeSpec:    func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.VolumeSpec },
		},
		"hidden": {
			labels:        naming.HiddenLabels,
			stsName:       crName + "-" + rsName + "-hidden",
			containerName: naming.ContainerHidden,
			volumeSpec:    func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.Hidden.VolumeSpec },
		},
		"non-voting": {
			labels:        naming.NonVotingLabels,
			stsName:       crName + "-" + rsName + "-nv",
			containerName: naming.ContainerNonVoting,
			volumeSpec:    func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.NonVoting.VolumeSpec },
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newCR()
			rs := cr.Spec.Replsets[0]
			ls := tt.labels(cr, rs)

			podName := tt.stsName + "-0"
			pvcName := psmdbconfig.MongodDataVolClaimName + "-" + podName

			sts := &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Name: tt.stsName, Namespace: namespace, Labels: ls},
			}

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{Name: pvcName, Namespace: namespace, Labels: ls},
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
				},
			}

			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: podName, Namespace: namespace, Labels: ls},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: tt.containerName}},
				},
				Status: corev1.PodStatus{
					Phase: corev1.PodRunning,
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:  tt.containerName,
							State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
						},
					},
				},
			}

			r := buildFakeClient(cr, sts, pvc, pod)

			var execContainer string
			r.clientcmd = &mockClientCmd{
				execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
					execContainer = containerName
					_, _ = stdout.Write([]byte(`Filesystem       1B-blocks       Used   Available Use% Mounted on
/dev/sdb       10737418240 9663676416  1073741824  90% /data/db`))
					return nil
				},
			}

			err := r.reconcileStorageAutoscaling(ctx, cr, sts, tt.volumeSpec(rs), ls)
			require.NoError(t, err)

			assert.Equal(t, tt.containerName, execContainer, "df must run in the pod's mongod container")

			status, ok := cr.Status.StorageAutoscaling[pvcName]
			require.True(t, ok, "PVC usage must be reported in status.storageAutoscaling")
			assert.Empty(t, status.LastError)
			assert.Equal(t, "10Gi", status.CurrentSize)

			newSize := tt.volumeSpec(rs).PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, "12Gi", newSize.String(), "usage above threshold must grow the volume")
		})
	}
}
