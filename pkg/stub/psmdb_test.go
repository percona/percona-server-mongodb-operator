package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAddPSMDBSpecDefaults(t *testing.T) {
	h := &Handler{
		serverVersion: &v1alpha1.ServerVersion{
			Platform: v1alpha1.PlatformKubernetes,
		},
	}
	m := &v1alpha1.PerconaServerMongoDB{
		Spec: v1alpha1.PerconaServerMongoDBSpec{},
	}

	h.addPSMDBSpecDefaults(m)

	assert.Equal(t, defaultVersion, m.Spec.Version)
	assert.Equal(t, int64(defaultRunUID), m.Spec.RunUID)

	assert.Len(t, m.Spec.Replsets, 1)
	assert.Equal(t, defaultReplsetName, m.Spec.Replsets[0].Name)
	assert.Equal(t, defaultMongodSize, m.Spec.Replsets[0].Size)

	assert.NotNil(t, m.Spec.Mongod)
	assert.Equal(t, defaultStorageEngine, m.Spec.Mongod.Storage.Engine)
	assert.NotNil(t, m.Spec.Mongod.Storage.WiredTiger)
	assert.NotNil(t, m.Spec.Mongod.Storage.WiredTiger.EngineConfig)
	assert.Equal(t, defaultWiredTigerCacheSizeRatio, m.Spec.Mongod.Storage.WiredTiger.EngineConfig.CacheSizeRatio)

	m.Spec = v1alpha1.PerconaServerMongoDBSpec{
		Mongod: &v1alpha1.MongodSpec{
			Storage: &v1alpha1.MongodSpecStorage{
				Engine: v1alpha1.StorageEngineInMemory,
			},
		},
	}
	h.addPSMDBSpecDefaults(m)
	assert.NotNil(t, m.Spec.Mongod.Storage.InMemory)
	assert.NotNil(t, m.Spec.Mongod.Storage.InMemory.EngineConfig)
	assert.Equal(t, m.Spec.Mongod.Storage.InMemory.EngineConfig.InMemorySizeRatio, defaultInMemorySizeRatio)

	// test runUID default is skipped on Openshift
	m.Spec = v1alpha1.PerconaServerMongoDBSpec{}
	h.serverVersion.Platform = v1alpha1.PlatformOpenshift
	h.addPSMDBSpecDefaults(m)
	assert.Equal(t, int64(0), m.Spec.RunUID)
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
			Mongod: &v1alpha1.MongodSpec{
				Net: &v1alpha1.MongodSpecNet{
					Port: 99999,
				},
			},
		},
	}

	h := &Handler{
		serverVersion: &v1alpha1.ServerVersion{
			Platform: v1alpha1.PlatformKubernetes,
		},
	}

	// default/wiredTiger set
	set, err := h.newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
	assert.NoError(t, err)
	assert.NotNil(t, set)
	assert.Equal(t, t.Name()+"-"+defaultReplsetName, set.Name)
	assert.Len(t, set.Spec.Template.Spec.Containers, 1)
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--storageEngine=wiredTiger")
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--wiredTigerCacheSizeGB=0.25")
	assert.Len(t, set.Spec.Template.Spec.Containers[0].Ports, 1)
	assert.Equal(t, int32(99999), set.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
	assert.Equal(t, int64(1001), *set.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser)

	// mmapv1 set
	psmdb.Spec.Mongod.Storage.Engine = v1alpha1.StorageEngineMMAPv1
	psmdb.Spec.Mongod.Storage.MMAPv1 = &v1alpha1.MongodSpecMMAPv1{
		Smallfiles: true,
	}
	mmapSet, err := h.newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
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
	imSet, err := h.newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
	assert.NoError(t, err)
	assert.NotNil(t, imSet)
	assert.Contains(t, imSet.Spec.Template.Spec.Containers[0].Args, "--inMemorySizeGB=0.93")

	// test runUID is disabled on Openshift
	h.serverVersion.Platform = v1alpha1.PlatformOpenshift
	osSet, err := h.newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
	assert.NoError(t, err)
	assert.Nil(t, osSet.Spec.Template.Spec.Containers[0].SecurityContext.RunAsUser)
}
