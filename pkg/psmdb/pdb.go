package psmdb

import (
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func PodDisruptionBudget(spec *api.PodDisruptionBudgetSpec, name, namespace string, labels map[string]string) *policyv1.PodDisruptionBudget {
	return &policyv1.PodDisruptionBudget{
		APIVersion: "policy/v1",
		Kind:       "PodDisruptionBudget",
		Name:       name,
		Namespace:  namespace,
		Labels:     labels,
		Spec: policyv1.PodDisruptionBudgetSpec{
			MinAvailable:   spec.MinAvailable,
			MaxUnavailable: spec.MaxUnavailable,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
		},
	}
}
