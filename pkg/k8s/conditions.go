package k8s

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func SetStatusCondition(conditions *[]psmdbv1.ClusterCondition, newCondition psmdbv1.ClusterCondition) {
	if newCondition.Reason == "" {
		newCondition.Reason = string(newCondition.Type)
	}

	if newCondition.Message == "" {
		newCondition.Message = newCondition.Reason
	}

	existingCondition := FindStatusCondition(*conditions, string(newCondition.Type))
	if existingCondition == nil {
		if newCondition.LastTransitionTime.IsZero() {
			newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
		*conditions = append(*conditions, newCondition)
	}
}

func FindStatusCondition(conditions []psmdbv1.ClusterCondition, conditionType string) *psmdbv1.ClusterCondition {
	for i := range conditions {
		if string(conditions[i].Type) == conditionType {
			return &conditions[i]
		}
	}

	return nil
}
