package mongod

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestGetMongodPort(t *testing.T) {
	assert.Equal(t, "27017", GetMongodPort(&corev1.Container{
		Ports: []corev1.ContainerPort{
			{
				Name:          MongodPortName,
				ContainerPort: int32(27017),
			},
		},
	}))
	assert.Equal(t, "", GetMongodPort(&corev1.Container{
		Ports: []corev1.ContainerPort{},
	}))
}

func TestGetWiredTigerCacheSizeGB(t *testing.T) {
	memoryQuantity := resource.NewQuantity(gigaByte/2, resource.DecimalSI)
	assert.Equal(t, 0.25, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5, true))

	memoryQuantity = resource.NewQuantity(1*gigaByte, resource.DecimalSI)
	assert.Equal(t, 0.25, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5, true))

	memoryQuantity = resource.NewQuantity(4*gigaByte, resource.DecimalSI)
	assert.Equal(t, 1.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5, true))

	memoryQuantity = resource.NewQuantity(64*gigaByte, resource.DecimalSI)
	assert.Equal(t, 31.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5, true))

	memoryQuantity = resource.NewQuantity(128*gigaByte, resource.DecimalSI)
	assert.Equal(t, 63.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5, true))

	memoryQuantity = resource.NewQuantity(256*gigaByte, resource.DecimalSI)
	assert.Equal(t, 127.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5, true))

	memoryQuantity = resource.NewQuantity(gigaByte, resource.DecimalSI)
	assert.Equal(t, 1.0, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 1.0, false))
}
