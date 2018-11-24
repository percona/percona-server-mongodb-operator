package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestPodList(t *testing.T) {
	podList := podList()
	assert.Equal(t, "Pod", podList.TypeMeta.Kind)
}

func TestGetPodNames(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name() + "-0",
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name: t.Name() + "-1",
			},
		},
	}
	podNames := getPodNames(pods)
	assert.Len(t, podNames, 2)
	assert.Equal(t, []string{t.Name() + "-0", t.Name() + "-1"}, podNames)
}

func TestGetPodContainer(t *testing.T) {
	pod := corev1.Pod{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: t.Name(),
				},
			},
		},
	}
	assert.NotNil(t, getPodContainer(&pod, t.Name()))
	assert.Nil(t, getPodContainer(&pod, "doesnt exist"))
}

func TestNewPSMDBPodAffinity(t *testing.T) {
	labels := map[string]string{
		"test": t.Name(),
	}
	psmdb := &v1alpha1.PerconaServerMongoDB{
		Spec: v1alpha1.PerconaServerMongoDBSpec{
			Mongod: &v1alpha1.MongodSpec{
				Affinity: &v1alpha1.AffinitySpec{
					UniqueHostname: v1alpha1.AffinityModePreferred,
				},
			},
		},
	}

	// enable just uniqueHostname in preferred mode
	affinity := newPSMDBPodAffinity(psmdb, labels)
	assert.NotNil(t, affinity)
	assert.NotNil(t, affinity.PodAntiAffinity)
	assert.Nil(t, affinity.PodAffinity)
	assert.Len(t, affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, 1)

	// enable uniqueHostname + uniqueRegion in preferred mode
	psmdb.Spec.Mongod.Affinity.UniqueRegion = v1alpha1.AffinityModePreferred
	affinity = newPSMDBPodAffinity(psmdb, labels)
	assert.Len(t, affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, 2)

	// enable uniqueHostname + uniqueRegion + uniqueZone in preferred mode
	psmdb.Spec.Mongod.Affinity.UniqueZone = v1alpha1.AffinityModePreferred
	affinity = newPSMDBPodAffinity(psmdb, labels)
	assert.Len(t, affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution, 3)

	// test required mode
	psmdb.Spec.Mongod.Affinity.UniqueHostname = v1alpha1.AffinityModeRequired
	psmdb.Spec.Mongod.Affinity.UniqueRegion = v1alpha1.AffinityModeDisabled
	psmdb.Spec.Mongod.Affinity.UniqueZone = v1alpha1.AffinityModeDisabled
	affinity = newPSMDBPodAffinity(psmdb, labels)
	assert.Len(t, affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 1)
	psmdb.Spec.Mongod.Affinity.UniqueRegion = v1alpha1.AffinityModeRequired
	psmdb.Spec.Mongod.Affinity.UniqueZone = v1alpha1.AffinityModeRequired
	affinity = newPSMDBPodAffinity(psmdb, labels)
	assert.Len(t, affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 3)

	// test regions
	assert.Nil(t, affinity.PodAffinity)
	psmdb.Spec.Mongod.Affinity.Regions = []string{"testRegion"}
	affinity = newPSMDBPodAffinity(psmdb, labels)
	assert.Len(t, affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 1)
	assert.Equal(t, topologyKeyFailureDomainRegion, affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution[0].TopologyKey)

	// test zones
	psmdb.Spec.Mongod.Affinity.Zones = []string{"testZone"}
	affinity = newPSMDBPodAffinity(psmdb, labels)
	assert.Len(t, affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 2)
}
