package util

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// LabelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given PerconaServerMongoDB CR name.
func LabelsForPerconaServerMongoDB(m *v1alpha1.PerconaServerMongoDB, labels map[string]string) map[string]string {
	ls := map[string]string{
		"app":                       "percona-server-mongodb",
		"percona-server-mongodb_cr": m.Name,
	}
	for k, v := range labels {
		ls[k] = v
	}
	return ls
}

// LabelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given PerconaServerMongoDB Replset.
func LabelsForPerconaServerMongoDBReplset(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) map[string]string {
	return LabelsForPerconaServerMongoDB(m, map[string]string{"replset": replset.Name})
}

// GetLabelSelectorListOpts returns metav1.ListOptions with a label-selector for a given replset
func GetLabelSelectorListOpts(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) *metav1.ListOptions {
	labelSelector := labels.SelectorFromSet(LabelsForPerconaServerMongoDB(
		m,
		map[string]string{"replset": replset.Name},
	)).String()
	return &metav1.ListOptions{LabelSelector: labelSelector}
}

// AddOwnerRefToObject appends the desired OwnerReference to the object
func AddOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// AsOwner returns an OwnerReference set as the PerconaServerMongoDB CR
func AsOwner(m *v1alpha1.PerconaServerMongoDB) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: m.APIVersion,
		Kind:       m.Kind,
		Name:       m.Name,
		UID:        m.UID,
		Controller: &TrueVar,
	}
}
