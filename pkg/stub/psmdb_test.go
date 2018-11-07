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
	assert.Equal(t, defaultWiredTigerCacheSizeRatio, spec.Mongod.Storage.WiredTiger.CacheSizeRatio)
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
	set, err := newPSMDBStatefulSet(psmdb, psmdb.Spec.Replsets[0], nil)
	assert.NoError(t, err)
	assert.NotNil(t, set)
	assert.Equal(t, t.Name()+"-"+defaultReplsetName, set.Name)
	assert.Len(t, set.Spec.Template.Spec.Containers, 1)
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--storageEngine=wiredTiger")
	assert.Contains(t, set.Spec.Template.Spec.Containers[0].Args, "--wiredTigerCacheSizeGB=0.25")
	assert.Len(t, set.Spec.Template.Spec.Containers[0].Ports, 1)
	assert.Equal(t, int32(99999), set.Spec.Template.Spec.Containers[0].Ports[0].ContainerPort)
}
