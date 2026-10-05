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
		name      string
		pvcName   string
		claimName string
		expected  string
	}{
		{
			name:      "standard PVC name",
			pvcName:   "mongod-data-my-cluster-rs0-0",
			claimName: psmdbconfig.MongodDataVolClaimName,
			expected:  "my-cluster-rs0-0",
		},
		{
			name:      "config server PVC",
			pvcName:   "mongod-data-my-cluster-cfg-0",
			claimName: psmdbconfig.MongodDataVolClaimName,
			expected:  "my-cluster-cfg-0",
		},
		{
			name:      "mongos log PVC",
			pvcName:   "mongos-logs-my-cluster-mongos-0",
			claimName: psmdbconfig.MongosLogVolClaimName,
			expected:  "my-cluster-mongos-0",
		},
		{
			name:      "invalid PVC name",
			pvcName:   "other-volume-claim",
			claimName: psmdbconfig.MongodDataVolClaimName,
			expected:  "",
		},
		{
			name:      "claim name of another component",
			pvcName:   "mongos-logs-my-cluster-mongos-0",
			claimName: psmdbconfig.MongodDataVolClaimName,
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPodNameFromPVC(tt.pvcName, tt.claimName)
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

			err = r.triggerResize(t.Context(), tt.cr, tt.pvc, tt.newSize, &volumeSpec.PersistentVolumeClaim)
			require.NoError(t, err)

			updatedSize := volumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.newSize.Value(), updatedSize.Value())
			assert.NotEqual(t, originalSize.Value(), updatedSize.Value())
		})
	}
}

// TestReconcileStorageAutoscalingComponents checks that autoscaling is applied
// to every component that owns a PVC. None of them agree on the claim name, the
// mount path or the container: replsets keep mongod-data under /data/db, hidden
// and non-voting pods name their mongod container after their component, and
// mongos keeps mongos-logs under /data/db/logs.
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
				Sharding: api.Sharding{
					Enabled: true,
					Mongos: &api.MongosSpec{
						Logs: &api.MongosLogsSpec{
							PersistentVolumeClaim: &volumeSpec().PersistentVolumeClaim,
						},
					},
				},
			},
		}
	}

	mongodVol := func(get func(rs *api.ReplsetSpec) *api.VolumeSpec, component string) func(cr *api.PerconaServerMongoDB) autoscaledVolume {
		return func(cr *api.PerconaServerMongoDB) autoscaledVolume {
			return mongodVolume(get(cr.Spec.Replsets[0]), component)
		}
	}

	tests := map[string]struct {
		labels        func(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) map[string]string
		stsName       string
		containerName string
		claimName     string
		mountPath     string
		vol           func(cr *api.PerconaServerMongoDB) autoscaledVolume
	}{
		"mongod": {
			labels:        naming.MongodLabels,
			stsName:       crName + "-" + rsName,
			containerName: naming.ContainerMongod,
			claimName:     psmdbconfig.MongodDataVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataDir,
			vol:           mongodVol(func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.VolumeSpec }, naming.ComponentMongod),
		},
		"hidden": {
			labels:        naming.HiddenLabels,
			stsName:       crName + "-" + rsName + "-hidden",
			containerName: naming.ContainerHidden,
			claimName:     psmdbconfig.MongodDataVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataDir,
			vol:           mongodVol(func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.Hidden.VolumeSpec }, naming.ComponentHidden),
		},
		"non-voting": {
			labels:        naming.NonVotingLabels,
			stsName:       crName + "-" + rsName + "-nv",
			containerName: naming.ContainerNonVoting,
			claimName:     psmdbconfig.MongodDataVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataDir,
			vol:           mongodVol(func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.NonVoting.VolumeSpec }, naming.ComponentNonVoting),
		},
		"mongos": {
			labels: func(cr *api.PerconaServerMongoDB, _ *api.ReplsetSpec) map[string]string {
				return naming.MongosLabels(cr)
			},
			stsName:       crName + "-" + naming.ComponentMongos,
			containerName: naming.ContainerMongos,
			claimName:     psmdbconfig.MongosLogVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataLogsDir,
			vol: func(cr *api.PerconaServerMongoDB) autoscaledVolume {
				return mongosVolume(cr.Spec.Sharding.Mongos.LogStorage())
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := newCR()
			rs := cr.Spec.Replsets[0]
			ls := tt.labels(cr, rs)

			podName := tt.stsName + "-0"
			pvcName := tt.claimName + "-" + podName

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
			var execCommand []string
			r.clientcmd = &mockClientCmd{
				execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
					execContainer = containerName
					execCommand = command
					_, _ = stdout.Write([]byte(`Filesystem       1B-blocks       Used   Available Use% Mounted on
/dev/sdb       10737418240 9663676416  1073741824  90% /data/db`))
					return nil
				},
			}

			vol := tt.vol(cr)
			err := r.reconcileStorageAutoscaling(ctx, cr, sts, vol, ls)
			require.NoError(t, err)

			assert.Equal(t, tt.containerName, execContainer, "df must run in the container mounting the volume")
			assert.Equal(t, []string{"df", "-B1", tt.mountPath}, execCommand, "df must probe the volume's mount path")

			status, ok := cr.Status.StorageAutoscaling[pvcName]
			require.True(t, ok, "PVC usage must be reported in status.storageAutoscaling")
			assert.Empty(t, status.LastError)
			assert.Equal(t, "10Gi", status.CurrentSize)

			newSize := vol.pvcSpec.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, "12Gi", newSize.String(), "usage above threshold must grow the volume")
		})
	}
}

// TestResizeMongosPVCsWithoutLogPVC covers the clusters that have no mongos PVC
// to grow. The mongos log volume only becomes a PVC when
// sharding.mongos.logs.persistentVolumeClaim is set: with the log collector
// alone it is an emptyDir, and with neither it does not exist. None of those
// may reach the autoscaler, let alone error out.
func TestResizeMongosPVCsWithoutLogPVC(t *testing.T) {
	ctx := context.Background()

	const (
		crName    = "test-cluster"
		namespace = "default"
	)

	newCR := func(mongos *api.MongosSpec, sharded bool) *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{Name: crName, Namespace: namespace},
			Spec: api.PerconaServerMongoDBSpec{
				// the log collector gives mongos an emptyDir log volume, never a PVC
				LogCollector: &api.LogCollectorSpec{Enabled: true},
				StorageScaling: &api.StorageScalingSpec{
					EnableVolumeScaling: true,
					Autoscaling: &api.AutoscalingSpec{
						Enabled:                 true,
						TriggerThresholdPercent: 80,
						GrowthStep:              resource.MustParse("2Gi"),
					},
				},
				Sharding: api.Sharding{
					Enabled: sharded,
					Mongos:  mongos,
				},
			},
		}
	}

	tests := map[string]struct {
		cr *api.PerconaServerMongoDB
	}{
		"log collector without log storage": {
			cr: newCR(&api.MongosSpec{}, true),
		},
		"logs section without a PVC": {
			cr: newCR(&api.MongosSpec{Logs: &api.MongosLogsSpec{}}, true),
		},
		"sharding disabled": {
			cr: newCR(&api.MongosSpec{}, false),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := buildFakeClient(tt.cr)

			execCalls := 0
			r.clientcmd = &mockClientCmd{
				execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
					execCalls++
					return nil
				},
			}

			require.NoError(t, r.resizeMongosPVCs(ctx, tt.cr))

			assert.Zero(t, execCalls, "no df should run when mongos has no PVC")
			assert.Empty(t, tt.cr.Status.StorageAutoscaling, "no PVC means nothing to report")
		})
	}
}

// TestReconcileStorageAutoscalingNoPVCs makes sure the autoscaler is a no-op
// when the component's volume is not a PVC, or when no PVC exists for it yet.
func TestReconcileStorageAutoscalingNoPVCs(t *testing.T) {
	ctx := context.Background()

	const (
		crName    = "test-cluster"
		namespace = "default"
	)

	cr := &api.PerconaServerMongoDB{
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
		},
	}

	ls := naming.MongosLabels(cr)
	stsName := crName + "-" + naming.ComponentMongos
	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: stsName, Namespace: namespace, Labels: ls},
	}

	emptyDirVolume := mongosVolume(nil)
	pvcVolume := mongosVolume(&api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	})

	tests := map[string]struct {
		vol autoscaledVolume
	}{
		"volume is not a PVC": {vol: emptyDirVolume},
		"PVC not created yet": {vol: pvcVolume},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			r := buildFakeClient(cr.DeepCopy(), sts)

			execCalls := 0
			r.clientcmd = &mockClientCmd{
				execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
					execCalls++
					return nil
				},
			}

			crCopy := cr.DeepCopy()
			require.NoError(t, r.reconcileStorageAutoscaling(ctx, crCopy, sts, tt.vol, ls))

			assert.Zero(t, execCalls)
			assert.Empty(t, crCopy.Status.StorageAutoscaling)
		})
	}
}
