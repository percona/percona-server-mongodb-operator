package perconaservermongodb

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestShouldTriggerResize(t *testing.T) {
	r := &ReconcilePerconaServerMongoDB{}
	ctx := context.Background()

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
					StorageAutoscaling: &api.StorageAutoscalingSpec{
						Enabled:                 true,
						TriggerThresholdPercent: 80,
						GrowthStepGi:            2,
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
					StorageAutoscaling: &api.StorageAutoscalingSpec{
						Enabled:                 true,
						TriggerThresholdPercent: 80,
						GrowthStepGi:            2,
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
					StorageAutoscaling: &api.StorageAutoscalingSpec{
						Enabled:                 true,
						TriggerThresholdPercent: 80,
						GrowthStepGi:            2,
						MaxSize:                 "10Gi",
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
					StorageAutoscaling: &api.StorageAutoscalingSpec{
						Enabled:                 true,
						TriggerThresholdPercent: 80,
						GrowthStepGi:            2,
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
			result := r.shouldTriggerResize(ctx, tt.cr, tt.pvc, tt.usage)
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
					StorageAutoscaling: &api.StorageAutoscalingSpec{
						GrowthStepGi: 2,
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
					StorageAutoscaling: &api.StorageAutoscalingSpec{
						GrowthStepGi: 5,
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
					StorageAutoscaling: &api.StorageAutoscalingSpec{
						GrowthStepGi: 10,
						MaxSize:      "15Gi",
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
