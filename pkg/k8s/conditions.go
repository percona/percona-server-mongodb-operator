package k8s

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func SetStatusCondition(conditions *[]psmdbv1.ClusterCondition, newCondition psmdbv1.ClusterCondition) (changed bool) {
	if conditions == nil {
		return false
	}
	existingCondition := FindStatusCondition(*conditions, string(newCondition.Type))
	if existingCondition == nil {
		if newCondition.LastTransitionTime.IsZero() {
			newCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
		*conditions = append(*conditions, newCondition)
		return true
	}

	if existingCondition.Status != newCondition.Status {
		existingCondition.Status = newCondition.Status
		if !newCondition.LastTransitionTime.IsZero() {
			existingCondition.LastTransitionTime = newCondition.LastTransitionTime
		} else {
			existingCondition.LastTransitionTime = metav1.NewTime(time.Now())
		}
		changed = true
	}

	if existingCondition.Reason != newCondition.Reason {
		existingCondition.Reason = newCondition.Reason
		changed = true
	}
	if existingCondition.Message != newCondition.Message {
		existingCondition.Message = newCondition.Message
		changed = true
	}

	return changed
}

func FindStatusCondition(conditions []psmdbv1.ClusterCondition, conditionType string) *psmdbv1.ClusterCondition {
	for i := range conditions {
		if string(conditions[i].Type) == conditionType {
			return &conditions[i]
		}
	}

	return nil
}
