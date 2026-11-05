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

func targetCluster(crVersion string) *api.PerconaServerMongoDB {
	return &api.PerconaServerMongoDB{
		Spec: api.PerconaServerMongoDBSpec{CRVersion: crVersion},
	}
}

func TestDeploymentSpec(t *testing.T) {
	recreate := appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	one, zero := intstr.FromInt32(1), intstr.FromInt32(0)
	rolling := appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &one,
			MaxSurge:       &zero,
		},
	}
	// Scaling must change replicas only: a pod-template change rolls the
	// whole group through a new ReplicaSet and forces a PCSM failover.
	baseTemplate := PodTemplateSpec(syncCR(api.PerconaServerMongoDBClusterSyncSpec{Size: 1}), targetCluster("1.24.0"), TLS{})

	tests := map[string]struct {
		size         int32
		wantReplicas int32
		wantStrategy appsv1.DeploymentStrategy
	}{
		"unset size behaves as a single instance":      {size: 0, wantReplicas: 1, wantStrategy: recreate},
		"negative size behaves as a single instance":   {size: -3, wantReplicas: 1, wantStrategy: recreate},
		"single instance uses Recreate":                {size: 1, wantReplicas: 1, wantStrategy: recreate},
		"two instances switch to RollingUpdate":        {size: 2, wantReplicas: 2, wantStrategy: rolling},
		"larger groups keep the same rolling strategy": {size: 5, wantReplicas: 5, wantStrategy: rolling},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{Size: tc.size})

			spec := DeploymentSpec(cr, PodTemplateSpec(cr, targetCluster("1.24.0"), TLS{}))

			require.NotNil(t, spec.Replicas)
			assert.Equal(t, tc.wantReplicas, *spec.Replicas)
			assert.Equal(t, tc.wantReplicas, Size(cr))
			assert.Equal(t, tc.wantStrategy, spec.Strategy)
			assert.Equal(t, Labels(cr), spec.Selector.MatchLabels,
				"selector must keep matching the pod labels, otherwise the deployment adopts nothing")
			assert.Equal(t, baseTemplate, spec.Template,
				"size must not change the pod template")
		})
	}
}

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
	preferred := func(topologyKey string) func(t *testing.T, got *corev1.Affinity, labels map[string]string) {
		return func(t *testing.T, got *corev1.Affinity, labels map[string]string) {
			require.NotNil(t, got)
			require.NotNil(t, got.PodAntiAffinity)
			assert.Empty(t, got.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
				"a required term would leave standbys Pending on small clusters")
			terms := got.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution
			require.Len(t, terms, 1)
			assert.Equal(t, int32(100), terms[0].Weight)
			assert.Equal(t, topologyKey, terms[0].PodAffinityTerm.TopologyKey)
			assert.Equal(t, labels, terms[0].PodAffinityTerm.LabelSelector.MatchLabels)
		}
	}
	isNil := func(t *testing.T, got *corev1.Affinity, _ map[string]string) {
		assert.Nil(t, got)
	}

	tests := map[string]struct {
		cluster *api.PerconaServerMongoDB
		size    int32
		labels  map[string]string
		spec    *api.PodAffinity
		assert  func(t *testing.T, got *corev1.Affinity, labels map[string]string)
	}{
		"single instance gets the same default as an HA group": {
			cluster: targetCluster("1.24.0"),
			size:    1,
			assert:  preferred(hostname),
		},
		"HA group defaults to preferred anti-affinity across hosts": {
			cluster: targetCluster("1.24.0"),
			size:    3,
			assert:  preferred(hostname),
		},
		"newer crVersion keeps the default": {
			cluster: targetCluster("1.25.0"),
			size:    3,
			assert:  preferred(hostname),
		},
		"older crVersion gets no default": {
			cluster: targetCluster("1.23.0"),
			size:    3,
			assert:  isNil,
		},
		"nil cluster gets no default": {
			size:   3,
			assert: isNil,
		},
		"user labels never widen the default selector": {
			cluster: targetCluster("1.24.0"),
			size:    3,
			labels:  map[string]string{"team": "platform"},
			assert: func(t *testing.T, got *corev1.Affinity, labels map[string]string) {
				preferred(hostname)(t, got, labels)
				selector := got.PodAntiAffinity.
					PreferredDuringSchedulingIgnoredDuringExecution[0].PodAffinityTerm.LabelSelector
				assert.NotContains(t, selector.MatchLabels, "team")
			},
		},
		"explicit topologyKey produces a required term": {
			cluster: targetCluster("1.24.0"),
			size:    3,
			spec:    &api.PodAffinity{TopologyKey: &zone},
			assert: func(t *testing.T, got *corev1.Affinity, labels map[string]string) {
				require.NotNil(t, got)
				terms := got.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution
				require.Len(t, terms, 1)
				assert.Equal(t, zone, terms[0].TopologyKey)
				assert.Equal(t, labels, terms[0].LabelSelector.MatchLabels)
			},
		},
		"explicit topologyKey is not version gated": {
			cluster: targetCluster("1.23.0"),
			size:    3,
			spec:    &api.PodAffinity{TopologyKey: &zone},
			assert: func(t *testing.T, got *corev1.Affinity, _ map[string]string) {
				require.NotNil(t, got)
				require.Len(t, got.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution, 1)
			},
		},
		"topologyKey none disables spreading": {
			cluster: targetCluster("1.24.0"),
			size:    3,
			spec:    &api.PodAffinity{TopologyKey: &off},
			assert:  isNil,
		},
		"empty affinity spec disables spreading": {
			cluster: targetCluster("1.24.0"),
			size:    3,
			spec:    &api.PodAffinity{},
			assert:  isNil,
		},
		"advanced affinity is passed through untouched": {
			cluster: targetCluster("1.24.0"),
			size:    3,
			spec:    &api.PodAffinity{Advanced: advanced},
			assert: func(t *testing.T, got *corev1.Affinity, _ map[string]string) {
				assert.Equal(t, advanced, got)
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{
				Size:     tc.size,
				Affinity: tc.spec,
				Labels:   tc.labels,
			})

			tmpl := PodTemplateSpec(cr, tc.cluster, TLS{})

			tc.assert(t, tmpl.Spec.Affinity, Labels(cr))
			for k := range tc.labels {
				assert.Contains(t, tmpl.Labels, k, "user labels still reach the pod template")
			}
		})
	}
}

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
			assert.Equal(t, DeploymentSpec(cr, PodTemplateSpec(cr, targetCluster("1.24.0"), TLS{})).Selector.MatchLabels, pdb.Spec.Selector.MatchLabels,
				"a PDB selecting anything other than the deployment's pods protects nothing")
		})
	}
}
