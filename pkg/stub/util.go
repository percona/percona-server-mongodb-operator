package stub

import (
	"fmt"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// labelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given PerconaServerMongoDB CR name.
func labelsForPerconaServerMongoDB(m *v1alpha1.PerconaServerMongoDB) map[string]string {
	return map[string]string{
		"app":                       "percona-server-mongodb",
		"percona-server-mongodb_cr": m.Name,
		"replset":                   m.Spec.Mongod.ReplsetName,
	}
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func addOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// asOwner returns an OwnerReference set as the PerconaServerMongoDB CR
func asOwner(m *v1alpha1.PerconaServerMongoDB) metav1.OwnerReference {
	trueVar := true
	return metav1.OwnerReference{
		APIVersion: m.APIVersion,
		Kind:       m.Kind,
		Name:       m.Name,
		UID:        m.UID,
		Controller: &trueVar,
	}
}

// podList returns a v1.PodList object
func podList() *corev1.PodList {
	return &corev1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
	}
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

func parseSpecResources(m *v1alpha1.PerconaServerMongoDB) (*corev1.ResourceRequirements, error) {
	limitsCpu, err := resource.ParseQuantity(fmt.Sprintf("%.1f", m.Spec.Mongod.Limits.Cpu))
	if err != nil {
		return nil, err
	}

	limitsMemory, err := resource.ParseQuantity(m.Spec.Mongod.Limits.Memory)
	if err != nil {
		return nil, err
	}

	limitsStorage, err := resource.ParseQuantity(m.Spec.Mongod.Limits.Storage)
	if err != nil {
		return nil, err
	}

	requestsCpu, err := resource.ParseQuantity(fmt.Sprintf("%.1f", m.Spec.Mongod.Requests.Cpu))
	if err != nil {
		return nil, err
	}

	requestsMemory, err := resource.ParseQuantity(m.Spec.Mongod.Requests.Memory)
	if err != nil {
		return nil, err
	}

	requestsStorage, err := resource.ParseQuantity(m.Spec.Mongod.Requests.Storage)
	if err != nil {
		return nil, err
	}

	return &corev1.ResourceRequirements{
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:     limitsCpu,
			corev1.ResourceMemory:  limitsMemory,
			corev1.ResourceStorage: limitsStorage,
		},
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:     requestsCpu,
			corev1.ResourceMemory:  requestsMemory,
			corev1.ResourceStorage: requestsStorage,
		},
	}, nil
}
