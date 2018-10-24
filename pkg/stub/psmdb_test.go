package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestGetWiredTigerCacheSizeGB(t *testing.T) {
	memoryQuantity := resource.NewQuantity(gigaByte/2, resource.DecimalSI)
	assert.Equal(t, 0.25, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5))

	memoryQuantity = resource.NewQuantity(1*gigaByte, resource.DecimalSI)
	assert.Equal(t, 0.25, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5))

	memoryQuantity = resource.NewQuantity(4*gigaByte, resource.DecimalSI)
	assert.Equal(t, 1.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5))

	memoryQuantity = resource.NewQuantity(64*gigaByte, resource.DecimalSI)
	assert.Equal(t, 31.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5))

	memoryQuantity = resource.NewQuantity(128*gigaByte, resource.DecimalSI)
	assert.Equal(t, 63.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5))

	memoryQuantity = resource.NewQuantity(256*gigaByte, resource.DecimalSI)
	assert.Equal(t, 127.5, getWiredTigerCacheSizeGB(corev1.ResourceList{corev1.ResourceMemory: *memoryQuantity}, 0.5))
}

func TestAddPSMDBSpecDefaults(t *testing.T) {
	spec := v1alpha1.PerconaServerMongoDBSpec{}

	addPSMDBSpecDefaults(&spec)

	assert.Equal(t, defaultVersion, spec.Version)
	assert.NotNil(t, spec.Mongod)
	assert.Equal(t, defaultMongodSize, spec.Mongod.Size)
	assert.Equal(t, defaultStorageEngine, spec.Mongod.StorageEngine)
	assert.NotNil(t, spec.Mongod.WiredTiger)
	assert.Equal(t, defaultWiredTigerCacheSizeRatio, spec.Mongod.WiredTiger.CacheSizeRatio)
}
