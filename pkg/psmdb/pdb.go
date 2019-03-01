package psmdb

import (
	policyv1beta1 "k8s.io/api/policy/v1beta1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func PodDisruptionBudget(spec *policyv1beta1.PodDisruptionBudgetSpec, labels map[string]string, namespace string) *policyv1beta1.PodDisruptionBudget {
	spec.Selector = &metav1.LabelSelector{
		MatchLabels: labels,
	}

	return &policyv1beta1.PodDisruptionBudget{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "policy/v1beta1",
			Kind:       "PodDisruptionBudget",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      labels["cluster"] + "-" + labels["component"] + "-" + labels["replset"],
			Namespace: namespace,
		},
		Spec: *spec,
	}
}
