package internal

import (
	appsv1 "k8s.io/api/apps/v1"
)

// IsStatefulSetUpdating returns a boolean reflecting if a StatefulSet is updating or
// scaling. If the currentRevision is different than the updateRevision or if the
// number of readyReplicas is different than currentReplicas, the set is updating
func IsStatefulSetUpdating(set *appsv1.StatefulSet) bool {
	if set.Status.CurrentRevision != set.Status.UpdateRevision {
		return true
	}
	return set.Status.ReadyReplicas != set.Status.CurrentReplicas
}
