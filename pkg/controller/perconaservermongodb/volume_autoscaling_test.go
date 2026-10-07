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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	psmdbconfig "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
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
				Name: "my-cluster-rs0-0",
			},
			{
				Name: "my-cluster-rs0-1",
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
				Name:      "test-cluster",
				Namespace: "default",
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
				Name:      "mongod-data-test-cluster-rs0-0",
				Namespace: "default",
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
				Name:      "test-cluster",
				Namespace: "default",
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
				Name:      "mongod-data-test-cluster-cfg-0",
				Namespace: "default",
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
				Name:      "test-cluster",
				Namespace: "default",
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
				Name:      "mongod-data-test-cluster-rs0-0",
				Namespace: "default",
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
				Name:      "test-cluster",
				Namespace: "default",
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
				Name:      "mongod-data-test-cluster-rs0-0",
				Namespace: "default",
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

// mountingTemplate returns a pod template whose `container` mounts `claimName`
// at `mountPath`, which is where the autoscaler reads the probe path from.
func mountingTemplate(container, claimName, mountPath string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:         container,
					VolumeMounts: []corev1.VolumeMount{{Name: claimName, MountPath: mountPath}},
				},
			},
		},
	}
}

// TestReconcileStorageAutoscalingComponents checks that autoscaling is applied
// to every component that owns a PVC. None of them agree on the claim name, the
// mount path or the container: replsets keep mongod-data under /data/db, hidden
// and non-voting pods name their mongod container after their component, and
// mongos keeps mongos-logs under /data/db/logs.
func TestReconcileStorageAutoscalingComponents(t *testing.T) {
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
			Name: crName, Namespace: namespace,
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

	mongodPVC := func(get func(rs *api.ReplsetSpec) *api.VolumeSpec) func(cr *api.PerconaServerMongoDB) *api.PVCSpec {
		return func(cr *api.PerconaServerMongoDB) *api.PVCSpec {
			return &get(cr.Spec.Replsets[0]).PersistentVolumeClaim
		}
	}

	tests := map[string]struct {
		labels        func(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) map[string]string
		stsName       string
		containerName string
		claimName     string
		mountPath     string
		pvcSpec       func(cr *api.PerconaServerMongoDB) *api.PVCSpec
	}{
		"mongod": {
			labels:        naming.MongodLabels,
			stsName:       crName + "-" + rsName,
			containerName: naming.ContainerMongod,
			claimName:     psmdbconfig.MongodDataVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataDir,
			pvcSpec:       mongodPVC(func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.VolumeSpec }),
		},
		"hidden": {
			labels:        naming.HiddenLabels,
			stsName:       crName + "-" + rsName + "-hidden",
			containerName: naming.ContainerHidden,
			claimName:     psmdbconfig.MongodDataVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataDir,
			pvcSpec:       mongodPVC(func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.Hidden.VolumeSpec }),
		},
		"non-voting": {
			labels:        naming.NonVotingLabels,
			stsName:       crName + "-" + rsName + "-nv",
			containerName: naming.ContainerNonVoting,
			claimName:     psmdbconfig.MongodDataVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataDir,
			pvcSpec:       mongodPVC(func(rs *api.ReplsetSpec) *api.VolumeSpec { return rs.NonVoting.VolumeSpec }),
		},
		"mongos": {
			labels: func(cr *api.PerconaServerMongoDB, _ *api.ReplsetSpec) map[string]string {
				return naming.MongosLabels(cr)
			},
			stsName:       crName + "-" + naming.ComponentMongos,
			containerName: naming.ContainerMongos,
			claimName:     psmdbconfig.MongosLogVolClaimName,
			mountPath:     psmdbconfig.MongodContainerDataLogsDir,
			pvcSpec: func(cr *api.PerconaServerMongoDB) *api.PVCSpec {
				return cr.Spec.Sharding.Mongos.LogStorage()
			},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()

			cr := newCR()
			rs := cr.Spec.Replsets[0]
			ls := tt.labels(cr, rs)

			podName := tt.stsName + "-0"
			pvcName := tt.claimName + "-" + podName

			sts := &appsv1.StatefulSet{
				Name: tt.stsName, Namespace: namespace, Labels: ls,
				Spec: appsv1.StatefulSetSpec{
					Template: mountingTemplate(tt.containerName, tt.claimName, tt.mountPath),
				},
			}

			pvc := &corev1.PersistentVolumeClaim{
				Name: pvcName, Namespace: namespace, Labels: ls,
				Status: corev1.PersistentVolumeClaimStatus{
					Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
				},
			}

			pod := &corev1.Pod{
				Name: podName, Namespace: namespace, Labels: ls,
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

			pvcSpec := tt.pvcSpec(cr)
			err := r.reconcileStorageAutoscaling(ctx, cr, sts, tt.claimName, pvcSpec, ls)
			assert.NoError(t, err)

			assert.Equal(t, tt.containerName, execContainer, "df must run in the container mounting the volume")
			assert.Equal(t, []string{"df", "-B1", tt.mountPath}, execCommand, "df must probe the volume's mount path")

			status, ok := cr.Status.StorageAutoscaling[pvcName]
			require.True(t, ok, "PVC usage must be reported in status.storageAutoscaling")
			assert.Empty(t, status.LastError)
			assert.Equal(t, "10Gi", status.CurrentSize)

			newSize := pvcSpec.Resources.Requests[corev1.ResourceStorage]
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
	const (
		crName    = "test-cluster"
		namespace = "default"
	)

	newCR := func(mongos *api.MongosSpec, sharded bool) *api.PerconaServerMongoDB {
		return &api.PerconaServerMongoDB{
			Name: crName, Namespace: namespace,
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
			ctx := t.Context()

			r := buildFakeClient(tt.cr)

			execCalls := 0
			r.clientcmd = &mockClientCmd{
				execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
					execCalls++
					return nil
				},
			}

			assert.NoError(t, r.resizeMongosPVCs(ctx, tt.cr))

			assert.Zero(t, execCalls, "no df should run when mongos has no PVC")
			assert.Empty(t, tt.cr.Status.StorageAutoscaling, "no PVC means nothing to report")
		})
	}
}

// TestReconcileStorageAutoscalingNoPVCs makes sure the autoscaler is a no-op
// when the component's volume is not a PVC, or when no PVC exists for it yet.
func TestReconcileStorageAutoscalingNoPVCs(t *testing.T) {
	const (
		crName    = "test-cluster"
		namespace = "default"
	)

	cr := &api.PerconaServerMongoDB{
		Name: crName, Namespace: namespace,
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
		Name: stsName, Namespace: namespace, Labels: ls,
		Spec: appsv1.StatefulSetSpec{
			Template: mountingTemplate(
				naming.ContainerMongos,
				psmdbconfig.MongosLogVolClaimName,
				psmdbconfig.MongodContainerDataLogsDir,
			),
		},
	}

	pvcSpec := &api.PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
	}

	tests := map[string]struct {
		pvcSpec *api.PVCSpec
	}{
		"volume is not a PVC": {pvcSpec: nil},
		"PVC not created yet": {pvcSpec: pvcSpec},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()

			r := buildFakeClient(cr.DeepCopy(), sts)

			execCalls := 0
			r.clientcmd = &mockClientCmd{
				execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
					execCalls++
					return nil
				},
			}

			crCopy := cr.DeepCopy()
			assert.NoError(t, r.reconcileStorageAutoscaling(ctx, crCopy, sts, psmdbconfig.MongosLogVolClaimName, tt.pvcSpec, ls))

			assert.Zero(t, execCalls)
			assert.Empty(t, crCopy.Status.StorageAutoscaling)
		})
	}
}

// autoscalingCR builds a single-replset cluster with autoscaling enabled whose
// data volume requests and holds `size`.
func autoscalingCR(name, namespace, size string) *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		Name: name, Namespace: namespace,
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
					Name: "rs0",
					VolumeSpec: &api.VolumeSpec{
						PersistentVolumeClaim: api.PVCSpec{
							PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
								Resources: corev1.VolumeResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceStorage: resource.MustParse(size),
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// fullPVCAndPod returns a PVC at `capacity` and its running pod, both labelled
// so the autoscaler picks them up.
func fullPVCAndPod(
	stsName, namespace, capacity string, ls map[string]string,
) (*corev1.PersistentVolumeClaim, *corev1.Pod) {
	podName := stsName + "-0"

	pvc := &corev1.PersistentVolumeClaim{
		Name:      psmdbconfig.MongodDataVolClaimName + "-" + podName,
		Namespace: namespace,
		Labels:    ls,
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(capacity)},
		},
	}

	pod := &corev1.Pod{
		Name: podName, Namespace: namespace, Labels: ls,
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: naming.ContainerMongod}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  naming.ContainerMongod,
					State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
				},
			},
		},
	}

	return pvc, pod
}

func fullDiskExec() *mockClientCmd {
	return &mockClientCmd{
		execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
			_, _ = stdout.Write([]byte(`Filesystem       1B-blocks       Used   Available Use% Mounted on
/dev/sdb       10737418240 9663676416  1073741824  90% /data/db`))
			return nil
		},
	}
}

// TestAutoscalingKeepsLargerPendingRequest covers a user expanding a volume by
// hand while the disk is above the threshold. The autoscaled size is one growth
// step over the capacity the PVC has, which is smaller than what the user asked
// for, and must not replace it.
func TestAutoscalingKeepsLargerPendingRequest(t *testing.T) {
	ctx := t.Context()

	const (
		crName    = "test-cluster"
		namespace = "default"
		stsName   = crName + "-rs0"
	)

	// capacity is 10Gi and the growth step is 2Gi, so autoscaling would ask for 12Gi
	cr := autoscalingCR(crName, namespace, "100Gi")
	rs := cr.Spec.Replsets[0]
	ls := naming.MongodLabels(cr, rs)
	sts := &appsv1.StatefulSet{
		Name: stsName, Namespace: namespace, Labels: ls,
		Spec: appsv1.StatefulSetSpec{
			Template: mountingTemplate(
				naming.ContainerMongod,
				psmdbconfig.MongodDataVolClaimName,
				psmdbconfig.MongodContainerDataDir,
			),
		},
	}
	pvc, pod := fullPVCAndPod(stsName, namespace, "10Gi", ls)

	r := buildFakeClient(cr, sts, pvc, pod)
	r.clientcmd = fullDiskExec()

	assert.NoError(t, r.reconcileStorageAutoscaling(ctx, cr, sts,
		psmdbconfig.MongodDataVolClaimName, &rs.VolumeSpec.PersistentVolumeClaim, ls))

	requested := rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, "100Gi", requested.String(), "a pending larger request must not be downgraded")

	stored := new(api.PerconaServerMongoDB)
	require.NoError(t, r.client.Get(ctx, types.NamespacedName{Name: crName, Namespace: namespace}, stored))
	storedSize := stored.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, "100Gi", storedSize.String())

	// the usage is still worth reporting, only the resize is skipped
	status, ok := cr.Status.StorageAutoscaling[pvc.Name]
	require.True(t, ok)
	assert.Empty(t, status.LastError)
}

var errPatchRejected = errors.New("patch rejected")

// TestTriggerResizeRestoresSizeOnPatchFailure covers a failed patch. Callers log
// the error and carry on reconciling with the same CR, so a size the API server
// never accepted may not be left behind in it: the PVC would be grown past what
// the CR requests, and the next reconcile would read that as a shrink.
func TestTriggerResizeRestoresSizeOnPatchFailure(t *testing.T) {
	ctx := t.Context()

	const (
		crName    = "test-cluster"
		namespace = "default"
	)

	cr := autoscalingCR(crName, namespace, "10Gi")
	pvcSpec := &cr.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim

	s := scheme.Scheme
	require.NoError(t, apis.AddToScheme(s))

	fakeClient := fake.NewClientBuilder().
		WithScheme(s).
		WithObjects(cr).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(
				ctx context.Context,
				cl client.WithWatch,
				obj client.Object,
				patch client.Patch,
				opts ...client.PatchOption,
			) error {
				return errPatchRejected
			},
		}).
		Build()

	r := &ReconcilePerconaServerMongoDB{client: fakeClient}

	pvc := &corev1.PersistentVolumeClaim{
		Name: "mongod-data-test-cluster-rs0-0", Namespace: namespace,
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("10Gi")},
		},
	}

	err := r.triggerResize(ctx, cr, pvc, resource.MustParse("12Gi"), pvcSpec)
	require.ErrorIs(t, err, errPatchRejected, "the API server error has to reach the caller")
	assert.EqualError(t, err, "patch CR with new storage size: patch rejected")

	requested := pvcSpec.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, "10Gi", requested.String(), "a rejected size must not stay in the spec")
}

// TestResizeMongosPVCsAutoscalesLogVolume is the happy path of resizeMongosPVCs:
// a sharded cluster whose mongos log volume is a PVC, filled past the threshold.
// It covers the wiring this function owns, that autoscaling runs against the
// mongos log volume and that the size it asks for is what the resize applies.
func TestResizeMongosPVCsAutoscalesLogVolume(t *testing.T) {
	ctx := t.Context()

	const (
		crName    = "some-name"
		namespace = "default"
		stsName   = crName + "-" + naming.ComponentMongos
		podName   = stsName + "-0"
	)
	pvcName := psmdbconfig.MongosLogVolClaimName + "-" + podName
	const customLogDir = "/mnt/mongos-logs"

	cr := &api.PerconaServerMongoDB{
		Name: crName, Namespace: namespace,
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: version.Version(),
			StorageScaling: &api.StorageScalingSpec{
				EnableVolumeScaling: true,
				Autoscaling: &api.AutoscalingSpec{
					Enabled:                 true,
					TriggerThresholdPercent: 80,
					GrowthStep:              resource.MustParse("2Gi"),
				},
			},
			Sharding: api.Sharding{
				Enabled: true,
				Mongos: &api.MongosSpec{
					Logs: &api.MongosLogsSpec{
						PersistentVolumeClaim: &api.PVCSpec{
							PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
								Resources: corev1.VolumeResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceStorage: resource.MustParse("1Gi"),
									},
								},
							},
						},
					},
				},
			},
		},
	}

	ls := naming.MongosLabels(cr)

	// a settled statefulset: resizeMongosPVCs waits for the rollout to finish
	sts := &appsv1.StatefulSet{
		Name: stsName, Namespace: namespace, Labels: ls,
		Spec: appsv1.StatefulSetSpec{
			// a non-default mount path: the probe path has to come from here
			Template: mountingTemplate(naming.ContainerMongos, psmdbconfig.MongosLogVolClaimName, customLogDir),
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					Name: psmdbconfig.MongosLogVolClaimName,
					Spec: corev1.PersistentVolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: resource.MustParse("1Gi"),
							},
						},
					},
				},
			},
		},
		Status: appsv1.StatefulSetStatus{Replicas: 1, UpdatedReplicas: 1},
	}

	pvc := &corev1.PersistentVolumeClaim{
		Name: pvcName, Namespace: namespace, Labels: ls,
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("1Gi")},
		},
	}

	pod := &corev1.Pod{
		Name: podName, Namespace: namespace, Labels: ls,
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: naming.ContainerMongos}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  naming.ContainerMongos,
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
/dev/sdb        1073741824  966367641   107374183  90% /data/db/logs`))
			return nil
		},
	}

	assert.NoError(t, r.resizeMongosPVCs(ctx, cr))

	// the log volume is probed where mongos actually mounts it
	assert.Equal(t, naming.ContainerMongos, execContainer)
	assert.Equal(t, []string{"df", "-B1", customLogDir}, execCommand,
		"the probe path has to be read from the pod template, not assumed")

	// usage above the threshold grows the request in the spec, 1Gi + 2Gi step
	requested := cr.Spec.Sharding.Mongos.LogStorage().Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, "3Gi", requested.String())

	status, ok := cr.Status.StorageAutoscaling[pvcName]
	require.True(t, ok, "PVC usage must be reported in status.storageAutoscaling")
	assert.Empty(t, status.LastError)
	assert.Equal(t, "1Gi", status.CurrentSize)

	// and the resize that follows in the same call picks that size up
	resized := new(corev1.PersistentVolumeClaim)
	require.NoError(t, r.client.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: namespace}, resized))
	grown := resized.Spec.Resources.Requests[corev1.ResourceStorage]
	assert.Equal(t, "3Gi", grown.String(), "autoscaling has to feed the resize in the same reconcile")
}

// TestReconcileStorageAutoscalingVolumeNotMounted covers a volume no container
// mounts: there is no path to probe, so autoscaling stays out rather than guess.
func TestReconcileStorageAutoscalingVolumeNotMounted(t *testing.T) {
	const (
		crName    = "test-cluster"
		namespace = "default"
		stsName   = crName + "-rs0"
	)

	cr := autoscalingCR(crName, namespace, "10Gi")
	rs := cr.Spec.Replsets[0]
	ls := naming.MongodLabels(cr, rs)
	pvc, pod := fullPVCAndPod(stsName, namespace, "10Gi", ls)

	tests := map[string]struct {
		template corev1.PodTemplateSpec
	}{
		"containers mount other volumes": {
			template: mountingTemplate(naming.ContainerMongod, "some-other-claim", "/elsewhere"),
		},
		"empty pod template": {},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()

			sts := &appsv1.StatefulSet{
				Name: stsName, Namespace: namespace, Labels: ls,
				Spec: appsv1.StatefulSetSpec{Template: tt.template},
			}

			r := buildFakeClient(cr.DeepCopy(), sts, pvc.DeepCopy(), pod.DeepCopy())

			execCalls := 0
			r.clientcmd = &mockClientCmd{
				execFunc: func(ctx context.Context, pod *corev1.Pod, containerName string, command []string, stdin io.Reader, stdout, stderr io.Writer, tty bool) error {
					execCalls++
					return nil
				},
			}

			crCopy := cr.DeepCopy()
			pvcSpec := &crCopy.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim

			assert.NoError(t, r.reconcileStorageAutoscaling(ctx, crCopy, sts,
				psmdbconfig.MongodDataVolClaimName, pvcSpec, ls))

			assert.Zero(t, execCalls, "no df without a known mount path")
			assert.Empty(t, crCopy.Status.StorageAutoscaling)

			requested := crCopy.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, "10Gi", requested.String())
		})
	}
}

// TestReconcileStorageAutoscalingPicksOwningContainer pins the invariant the
// mount lookup relies on. Sidecars mount these volumes too — fluentbit when log
// collection is on — and the lookup takes the first match, which is the right
// one only because mongod and mongos are built before any sidecar. If that build
// order ever changes, df would run in a sidecar and this test is what says so.
func TestReconcileStorageAutoscalingPicksOwningContainer(t *testing.T) {
	ctx := t.Context()

	const (
		crName    = "test-cluster"
		namespace = "default"
		stsName   = crName + "-rs0"
	)

	cr := autoscalingCR(crName, namespace, "10Gi")
	rs := cr.Spec.Replsets[0]
	ls := naming.MongodLabels(cr, rs)
	pvc, pod := fullPVCAndPod(stsName, namespace, "10Gi", ls)

	sts := &appsv1.StatefulSet{
		Name: stsName, Namespace: namespace, Labels: ls,
		Spec: appsv1.StatefulSetSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name: naming.ContainerMongod,
							VolumeMounts: []corev1.VolumeMount{
								{Name: psmdbconfig.MongodDataVolClaimName, MountPath: psmdbconfig.MongodContainerDataDir},
							},
						},
						{
							// the log collector mounts the same volume
							Name: "logs",
							VolumeMounts: []corev1.VolumeMount{
								{Name: psmdbconfig.MongodDataVolClaimName, MountPath: "/sidecar/view"},
							},
						},
					},
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

	assert.NoError(t, r.reconcileStorageAutoscaling(ctx, cr, sts,
		psmdbconfig.MongodDataVolClaimName, &rs.VolumeSpec.PersistentVolumeClaim, ls))

	assert.Equal(t, naming.ContainerMongod, execContainer, "df must run in the container that owns the volume")
	assert.Equal(t, []string{"df", "-B1", psmdbconfig.MongodContainerDataDir}, execCommand)
}
