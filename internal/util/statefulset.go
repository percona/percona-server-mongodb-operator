package util

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewStatefulSet returns a StatefulSet object configured for a name
func NewStatefulSet(m *v1alpha1.PerconaServerMongoDB, name string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: m.Namespace,
		},
	}
}

// IsStatefulSetUpdating returns a boolean reflecting if a StatefulSet is updating or
// scaling. If the currentRevision is different than the updateRevision or if the
// number of readyReplicas is different than currentReplicas, the set is updating
func IsStatefulSetUpdating(set *appsv1.StatefulSet) bool {
	if set.Status.CurrentRevision != set.Status.UpdateRevision {
		return true
	}
	return set.Status.ReadyReplicas != set.Status.CurrentReplicas
}
