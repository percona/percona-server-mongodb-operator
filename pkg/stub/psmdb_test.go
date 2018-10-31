package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func TestNewPSMDBStatefulSet(t *testing.T) {
	psmdb := &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.Name(),
			Namespace: "test",
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Mongod: &v1alpha1.MongodSpec{
				ReplsetName: defaultReplsetName,
				Size:        defaultMongodSize,
				Port:        99999,
				ResourcesSpec: &v1alpha1.ResourcesSpec{
					Limits: &v1alpha1.ResourceSpecRequirements{
						Cpu:     "1",
						Memory:  "1G",
						Storage: "1G",
					},
					Requests: &v1alpha1.ResourceSpecRequirements{
						Cpu:    "1",
						Memory: "1G",
					},
				},
			},
		},
	}
	set, err := newPSMDBStatefulSet(psmdb)
	assert.NoError(t, err)
	assert.NotNil(t, set)
	assert.Equal(t, t.Name()+"-"+defaultReplsetName, set.Name)
	assert.Len(t, set.Spec.Template.Spec.Containers, 1)
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--storageEngine=wiredTiger")
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--wiredTigerCacheSizeGB=0.25")
	assert.Len(t, set.Spec.Template.Spec.Containers[0].Ports, 1)
	assert.Equal(t, int32(99999), set.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
}
