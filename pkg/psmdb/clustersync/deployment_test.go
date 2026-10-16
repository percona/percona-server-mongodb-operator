package clustersync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func syncCR(spec api.PerconaServerMongoDBClusterSyncSpec) *api.PerconaServerMongoDBClusterSync {
	return &api.PerconaServerMongoDBClusterSync{
		Name:      "mysync",
		Namespace: "ns",
		Spec:      spec,
	}
}

// TestDeploymentSpecSizeAndStrategy pins the two properties that make an
// HA group behave: how many PCSM instances run, and how they are replaced
// during an update.
//
// The strategy is size-dependent on purpose. With a single instance there
// is no standby to take over, and PCSM must never have two processes
// writing to the same target, so Recreate (stop the old pod before
// starting the new one) is the only safe choice. From two instances up,
// PCSM's own lease on the target arbitrates who writes, so a rolling
// update is both safe and strictly better — the standby is promoted
// within the lease TTL instead of the sync pausing for a full pod
// restart. maxSurge stays 0 so a rollout never pushes group membership
// above the configured size; every extra member is another connection to
// the target and another participant in the election.
//
// Size 0 is the upgrade case: CRs stored before spec.size existed come
// back with the zero value, and must behave exactly like size 1 rather
// than scaling the deployment to zero.
func TestDeploymentSpecSizeAndStrategy(t *testing.T) {
	recreate := appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	one, zero := intstr.FromInt32(1), intstr.FromInt32(0)
	rolling := appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &one,
			MaxSurge:       &zero,
		},
	}

	tests := map[string]struct {
		size         int32
		wantReplicas int32
		wantStrategy appsv1.DeploymentStrategy
	}{
		"unset size behaves as a single instance":      {size: 0, wantReplicas: 1, wantStrategy: recreate},
		"single instance uses Recreate":                {size: 1, wantReplicas: 1, wantStrategy: recreate},
		"two instances switch to RollingUpdate":        {size: 2, wantReplicas: 2, wantStrategy: rolling},
		"larger groups keep the same rolling strategy": {size: 5, wantReplicas: 5, wantStrategy: rolling},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{Size: tc.size})

			spec := DeploymentSpec(cr, PodTemplateSpec(cr))

			require.NotNil(t, spec.Replicas)
			assert.Equal(t, tc.wantReplicas, *spec.Replicas)
			assert.Equal(t, tc.wantStrategy, spec.Strategy)
			assert.Equal(t, Labels(cr), spec.Selector.MatchLabels,
				"selector must keep matching the pod labels, otherwise the deployment adopts nothing")
		})
	}
}

// TestPodAffinity covers the scheduling defaults for an HA group.
//
// Spreading matters here: three PCSM instances on one node fail together
// and the group loses its standby. The default is *preferred* rather than
// required anti-affinity, which is a deliberate departure from the data
// pods in psmdb_defaults.go. A required term that cannot be satisfied
// leaves the standby Pending forever with no error anywhere in the CR,
// and since PCSM standbys hold no data the cost of co-scheduling is far
// lower than the cost of never scheduling. A single-instance deployment
// gets no affinity at all — there is nothing to spread away from.
//
// User intent always wins: `advanced` is passed through verbatim and
// `topologyKey: none` disables spreading entirely.
func TestPodAffinity(t *testing.T) {
	hostname := "kubernetes.io/hostname"
	zone := "topology.kubernetes.io/zone"
	off := api.AffinityOff
	advanced := &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key:      "disk",
						Operator: corev1.NodeSelectorOpIn,
						Values:   []string{"ssd"},
					}},
				}},
			},
		},
	}

	tests := map[string]struct {
		size   int32
		spec   *api.PodAffinity
		assert func(t *testing.T, got *corev1.Affinity, labels map[string]string)
	}{
		"single instance gets no affinity": {
			size: 1,
			assert: func(t *testing.T, got *corev1.Affinity, _ map[string]string) {
				assert.Nil(t, got)
			},
		},
		"HA group defaults to preferred anti-affinity across hosts": {
			size: 3,
			assert: func(t *testing.T, got *corev1.Affinity, labels map[string]string) {
				require.NotNil(t, got)
				require.NotNil(t, got.PodAntiAffinity)
				assert.Empty(t, got.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
					"a required term would leave standbys Pending on small clusters")
				terms := got.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
				require.Len(t, terms, 1)
				assert.Equal(t, int32(100), terms[0].Weight)
				assert.Equal(t, hostname, terms[0].PodAffinityTerm.TopologyKey)
				assert.Equal(t, labels, terms[0].PodAffinityTerm.LabelSelector.MatchLabels)
			},
		},
		"explicit topologyKey produces a required term": {
			size: 3,
			spec: &api.PodAffinity{TopologyKey: &zone},
			assert: func(t *testing.T, got *corev1.Affinity, labels map[string]string) {
				require.NotNil(t, got)
				terms := got.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
				require.Len(t, terms, 1)
				assert.Equal(t, zone, terms[0].TopologyKey)
				assert.Equal(t, labels, terms[0].LabelSelector.MatchLabels)
			},
		},
		"topologyKey none disables spreading even for an HA group": {
			size: 3,
			spec: &api.PodAffinity{TopologyKey: &off},
			assert: func(t *testing.T, got *corev1.Affinity, _ map[string]string) {
				assert.Nil(t, got)
			},
		},
		"advanced affinity is passed through untouched": {
			size: 3,
			spec: &api.PodAffinity{Advanced: advanced},
			assert: func(t *testing.T, got *corev1.Affinity, _ map[string]string) {
				assert.Equal(t, advanced, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{Size: tc.size, Affinity: tc.spec})

			tc.assert(t, PodTemplateSpec(cr).Spec.Affinity, Labels(cr))
		})
	}
}

// TestPodAffinityDoesNotAliasLabels guards a subtle sharing bug: the
// affinity selector is built from the same label map the pod template
// uses, and the template merges spec.labels into its copy. If the
// selector aliased that map, a user label would silently widen the
// anti-affinity selector to pods it was never meant to match.
func TestPodAffinityDoesNotAliasLabels(t *testing.T) {
	cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{
		Size:   3,
		Labels: map[string]string{"team": "platform"},
	})

	tmpl := PodTemplateSpec(cr)

	require.NotNil(t, tmpl.Spec.Affinity)
	selector := tmpl.Spec.Affinity.PodAntiAffinity.
		PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.LabelSelector
	assert.Equal(t, Labels(cr), selector.MatchLabels)
	assert.NotContains(t, selector.MatchLabels, "team")
	assert.Contains(t, tmpl.Labels, "team", "user labels still reach the pod template")
}

// TestPodDisruptionBudget covers the voluntary-disruption guard for an HA
// group. The budget only exists above one instance: at size 1 a PDB can
// only block node drains — there is no second instance for it to protect
// — so the builder returns nil and the controller deletes any leftover.
// The default of maxUnavailable=1 is what keeps a multi-node drain from
// evicting the whole group at once; an explicit spec replaces it wholesale
// so a user can express minAvailable instead.
func TestPodDisruptionBudget(t *testing.T) {
	two := intstr.FromInt32(2)
	oneStr := intstr.FromString("50%")

	tests := map[string]struct {
		size               int32
		spec               *api.PodDisruptionBudgetSpec
		wantNil            bool
		wantMaxUnavailable *intstr.IntOrString
		wantMinAvailable   *intstr.IntOrString
	}{
		"no budget for a single instance": {
			size:    1,
			wantNil: true,
		},
		"explicit budget is still skipped at size 1": {
			size:    1,
			spec:    &api.PodDisruptionBudgetSpec{MaxUnavailable: &two},
			wantNil: true,
		},
		"HA group defaults to maxUnavailable 1": {
			size:               3,
			wantMaxUnavailable: new(intstr.FromInt32(1)),
		},
		"explicit maxUnavailable overrides the default": {
			size:               3,
			spec:               &api.PodDisruptionBudgetSpec{MaxUnavailable: &two},
			wantMaxUnavailable: &two,
		},
		"explicit minAvailable replaces the default entirely": {
			size:             3,
			spec:             &api.PodDisruptionBudgetSpec{MinAvailable: &oneStr},
			wantMinAvailable: &oneStr,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{Size: tc.size, PodDisruptionBudget: tc.spec})

			pdb := PodDisruptionBudget(cr)

			if tc.wantNil {
				assert.Nil(t, pdb)
				return
			}
			require.NotNil(t, pdb)
			assert.Equal(t, DeploymentName(cr), pdb.Name, "PDB shares the deployment name so the controller can delete it by name")
			assert.Equal(t, cr.Namespace, pdb.Namespace)
			assert.Equal(t, tc.wantMaxUnavailable, pdb.Spec.MaxUnavailable)
			assert.Equal(t, tc.wantMinAvailable, pdb.Spec.MinAvailable)

			require.NotNil(t, pdb.Spec.Selector)
			assert.Equal(t, DeploymentSpec(cr, PodTemplateSpec(cr)).Selector.MatchLabels, pdb.Spec.Selector.MatchLabels,
				"a PDB selecting anything other than the deployment's pods protects nothing")
		})
	}
}

// TestSize documents the single place that interprets spec.size, since
// every other builder reads it through here.
func TestSize(t *testing.T) {
	tests := map[string]struct {
		size int32
		want int32
	}{
		"unset falls back to one": {size: 0, want: 1},
		"negative falls back to one (CRD minimum guards the API, not old objects)": {size: -3, want: 1},
		"explicit size is honoured": {size: 4, want: 4},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, Size(syncCR(api.PerconaServerMongoDBClusterSyncSpec{Size: tc.size})))
		})
	}
}
