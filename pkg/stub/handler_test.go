package stub

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestGetWiredTigerCacheSizeGB(t *testing.T) {
	memoryQuantity := resource.NewQuantity(gigaByte, resource.DecimalSI)
	assert.Equal(t, 0.25, getWiredTigerCacheSizeGB(memoryQuantity, 0.5))

	memoryQuantity = resource.NewQuantity(4*gigaByte, resource.DecimalSI)
	assert.Equal(t, 1.5, getWiredTigerCacheSizeGB(memoryQuantity, 0.5))

	memoryQuantity = resource.NewQuantity(64*gigaByte, resource.DecimalSI)
	assert.Equal(t, 31.5, getWiredTigerCacheSizeGB(memoryQuantity, 0.5))

	memoryQuantity = resource.NewQuantity(128*gigaByte, resource.DecimalSI)
	assert.Equal(t, 63.5, getWiredTigerCacheSizeGB(memoryQuantity, 0.5))
}
