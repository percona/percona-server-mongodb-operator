package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAddPSMDBSpecDefaults(t *testing.T) {
	spec := v1alpha1.PerconaServerMongoDBSpec{}

	addPSMDBSpecDefaults(&spec)

	assert.Equal(t, defaultVersion, spec.Version)

	assert.Len(t, spec.Replsets, 1)
	assert.Equal(t, defaultReplsetName, spec.Replsets[0].Name)
	assert.Equal(t, defaultMongodSize, spec.Replsets[0].Size)

	assert.NotNil(t, spec.Mongod)
	assert.Equal(t, defaultStorageEngine, spec.Mongod.Storage.Engine)
	assert.NotNil(t, spec.Mongod.Storage.WiredTiger)
	assert.NotNil(t, spec.Mongod.Storage.WiredTiger.EngineConfig)
	assert.Equal(t, defaultWiredTigerCacheSizeRatio, spec.Mongod.Storage.WiredTiger.EngineConfig.CacheSizeRatio)

	spec2 := v1alpha1.PerconaServerMongoDBSpec{
		Mongod: &v1alpha1.MongodSpec{
			Storage: &v1alpha1.MongodSpecStorage{
				Engine: v1alpha1.StorageEngineInMemory,
			},
		},
	}
	addPSMDBSpecDefaults(&spec2)
	assert.NotNil(t, spec2.Mongod.Storage.InMemory)
	assert.NotNil(t, spec2.Mongod.Storage.InMemory.EngineConfig)
	assert.Equal(t, spec2.Mongod.Storage.InMemory.EngineConfig.InMemorySizeRatio, defaultInMemorySizeRatio)
}

func TestNewPSMDBStatefulSet(t *testing.T) {
	psmdb := &v1alpha1.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{
			Name:      t.Name(),
			Namespace: "test",
		},
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Replsets: []*v1alpha1.ReplsetSpec{
				{
					Name: defaultReplsetName,
					Size: defaultMongodSize,
				},
			},
			Mongod: &v1alpha1.MongodSpec{
				Net: &v1alpha1.MongodSpecNet{
					Port: 99999,
				},
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
	// default/wiredTiger set
	set, err := newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
	assert.NoError(t, err)
	assert.NotNil(t, set)
	assert.Equal(t, t.Name()+"-"+defaultReplsetName, set.Name)
	assert.Len(t, set.Spec.Template.Spec.Containers, 1)
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--storageEngine=wiredTiger")
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--wiredTigerCacheSizeGB=0.25")
	assert.Len(t, set.Spec.Template.Spec.Containers[0].Ports, 1)
	assert.Equal(t, int32(99999), set.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)

	// mmapv1 set
	psmdb.Spec.Mongod.Storage.Engine = v1alpha1.StorageEngineMMAPv1
	psmdb.Spec.Mongod.Storage.MMAPv1 = &v1alpha1.MongodSpecMMAPv1{
		Smallfiles: true,
	}
	mmapSet, err := newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
	assert.NoError(t, err)
	assert.NotNil(t, mmapSet)
	assert.Contains(t, mmapSet.Spec.Template.Spec.Containers[0].Args, "--storageEngine=mmapv1")
	assert.Contains(t, mmapSet.Spec.Template.Spec.Containers[0].Args, "--smallfiles")

	// inMemory set
	psmdb.Spec.Mongod.Storage.Engine = v1alpha1.StorageEngineInMemory
	psmdb.Spec.Mongod.Storage.InMemory = &v1alpha1.MongodSpecInMemory{
		EngineConfig: &v1alpha1.MongodSpecInMemoryEngineConfig{
			InMemorySizeRatio: 1.0,
		},
	}
	imSet, err := newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
	assert.NoError(t, err)
	assert.NotNil(t, imSet)
	assert.Contains(t, imSet.Spec.Template.Spec.Containers[0].Args, "--inMemorySizeGB=0.93")
}
