package clustersync

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// PodDisruptionBudget returns nil for a single-instance deployment: a PDB
// there could only block drains without protecting anything.
func PodDisruptionBudget(cr *api.PerconaServerMongoDBClusterSync) *policyv1.PodDisruptionBudget {
	if Size(cr) < 2 {
		return nil
	}

	spec := cr.Spec.PodDisruptionBudget
	if spec == nil {
		maxUnavailable := intstr.FromInt32(1)
		spec = &api.PodDisruptionBudgetSpec{MaxUnavailable: &maxUnavailable}
	}

	labels := Labels(cr)
	return &policyv1.PodDisruptionBudget{
		APIVersion: "policy/v1",
		Kind:       "PodDisruptionBudget",
		Name:       DeploymentName(cr),
		Namespace:  cr.Namespace,
		Labels:     labels,
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable:   spec.MinAvailable,
			MaxUnavailable: spec.MaxUnavailable,
			Selector:       &metav1.LabelSelector{MatchLabels: labels},
		},
	}
}
