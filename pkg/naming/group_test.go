package naming_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func testCR() (*api.PerconaServerMongoDB, *api.ReplsetSpec) {
	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "psmdb"},
	}
	return cr, &api.ReplsetSpec{Name: "rs0"}
}

// Every reserved group must derive exactly what the legacy helpers derive.
// This is the migration identity contract; if it breaks, a format-only
// conversion renames workloads.
func TestReservedGroupIdentityMatchesLegacyHelpers(t *testing.T) {
	cr, rs := testCR()

	tests := []struct {
		group     string
		sts       string
		component string
		container string
		configMap string
	}{
		{naming.GroupMongod, naming.MongodStatefulSetName(cr, rs), naming.ComponentMongod,
			naming.ContainerMongod, naming.MongodCustomConfigName(cr, rs)},
		{naming.GroupNonVoting, naming.NonVotingStatefulSetName(cr, rs), naming.ComponentNonVoting,
			naming.ContainerNonVoting, naming.NonVotingConfigMapName(cr, rs)},
		{naming.GroupHidden, naming.HiddenStatefulSetName(cr, rs), naming.ComponentHidden,
			naming.ContainerHidden, naming.HiddenConfigMapName(cr, rs)},
		{naming.GroupArbiter, naming.ArbiterStatefulSetName(cr, rs), naming.ComponentArbiter,
			naming.ContainerArbiter, naming.MongodCustomConfigName(cr, rs)},
	}

	for _, tt := range tests {
		t.Run(tt.group, func(t *testing.T) {
			assert.Equal(t, tt.sts, naming.GroupStatefulSetName(cr, rs, tt.group))
			assert.Equal(t, tt.component, naming.GroupComponent(tt.group))
			assert.Equal(t, tt.container,
				naming.GroupContainerName(tt.group, tt.group == naming.GroupArbiter))
			assert.Equal(t, tt.configMap, naming.GroupConfigMapName(cr, rs, tt.group))
		})
	}
}

func TestReservedGroupLiteralNames(t *testing.T) {
	cr, rs := testCR()

	assert.Equal(t, "cluster1-rs0", naming.GroupStatefulSetName(cr, rs, naming.GroupMongod))
	assert.Equal(t, "cluster1-rs0-nv", naming.GroupStatefulSetName(cr, rs, naming.GroupNonVoting))
	assert.Equal(t, "cluster1-rs0-hidden", naming.GroupStatefulSetName(cr, rs, naming.GroupHidden))
	assert.Equal(t, "cluster1-rs0-arbiter", naming.GroupStatefulSetName(cr, rs, naming.GroupArbiter))
	assert.Equal(t, "cluster1-rs0-mongod", naming.GroupConfigMapName(cr, rs, naming.GroupArbiter),
		"the reserved arbiter group shares the base mongod config map")
}

func TestCustomGroupIdentity(t *testing.T) {
	cr, rs := testCR()

	assert.Equal(t, "cluster1-rs0-hot", naming.GroupStatefulSetName(cr, rs, "hot"))
	assert.Equal(t, "hot", naming.GroupComponent("hot"))
	assert.Equal(t, "mongod", naming.GroupContainerName("hot", false))
	assert.Equal(t, "mongod-arbiter", naming.GroupContainerName("hot", true),
		"a custom arbiter-only group stays out of mongod exec paths")
	assert.Equal(t, "cluster1-rs0-hot", naming.GroupConfigMapName(cr, rs, "hot"))
	assert.Equal(t, "cluster1-rs0-hot-2", naming.GroupPodName(cr, rs, "hot", 2))
	assert.Equal(t, "cluster1-rs0-2", naming.GroupPodName(cr, rs, naming.GroupMongod, 2))
	assert.Equal(t, "cluster1-rs0-hot-hookscript",
		naming.GroupHookScriptConfigMapName(cr, rs, "hot"))
}

func TestGroupLabelsCarryTheComponent(t *testing.T) {
	cr, rs := testCR()

	ls := naming.GroupLabels(cr, rs, "hot")
	assert.Equal(t, "hot", ls[naming.LabelKubernetesComponent])
	assert.Equal(t, "rs0", ls[naming.LabelKubernetesReplset])
	assert.Equal(t, "cluster1", ls[naming.LabelKubernetesInstance])

	// Legacy label helpers must be byte-identical to their group equivalents.
	assert.Equal(t, naming.MongodLabels(cr, rs), naming.GroupLabels(cr, rs, naming.GroupMongod))
	assert.Equal(t, naming.NonVotingLabels(cr, rs), naming.GroupLabels(cr, rs, naming.GroupNonVoting))
	assert.Equal(t, naming.HiddenLabels(cr, rs), naming.GroupLabels(cr, rs, naming.GroupHidden))
	assert.Equal(t, naming.ArbiterLabels(cr, rs), naming.GroupLabels(cr, rs, naming.GroupArbiter))
}

// Pod ordinals must be parsed numerically. Lexical ordering puts "10" before
// "2", which silently truncates the wrong pods out of the desired membership.
func TestPodOrdinal(t *testing.T) {
	tests := map[string]struct {
		podName string
		want    int
		ok      bool
	}{
		"base":          {"cluster1-rs0-0", 0, true},
		"double digit":  {"cluster1-rs0-10", 10, true},
		"custom group":  {"cluster1-rs0-hot-7", 7, true},
		"no ordinal":    {"cluster1-rs0", -1, false},
		"trailing dash": {"cluster1-rs0-", -1, false},
		"not a number":  {"cluster1-rs0-abc", -1, false},
		"empty":         {"", -1, false},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			got, ok := naming.PodOrdinal(tt.podName)
			assert.Equal(t, tt.ok, ok)
			assert.Equal(t, tt.want, got)
		})
	}
}
