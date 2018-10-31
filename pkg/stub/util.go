package stub

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	falseVar = false
	trueVar  = true
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

func parseSpecResourceRequirements(rsr *v1alpha1.ResourceSpecRequirements) (corev1.ResourceList, error) {
	rl := corev1.ResourceList{}

	if rsr.Cpu != "" {
		cpu := rsr.Cpu
		if !strings.HasSuffix(cpu, "m") {
			cpuFloat64, err := strconv.ParseFloat(cpu, 64)
			if err != nil {
				return nil, err
			}
			cpu = fmt.Sprintf("%.1f", cpuFloat64)
		}
		cpuQuantity, err := resource.ParseQuantity(cpu)
		if err != nil {
			return nil, err
		}
		rl[corev1.ResourceCPU] = cpuQuantity
	}

	if rsr.Memory != "" {
		memoryQuantity, err := resource.ParseQuantity(rsr.Memory)
		if err != nil {
			return nil, err
		}
		rl[corev1.ResourceMemory] = memoryQuantity
	}

	if rsr.Storage != "" {
		storageQuantity, err := resource.ParseQuantity(rsr.Storage)
		if err != nil {
			return nil, err
		}
		rl[corev1.ResourceStorage] = storageQuantity
	}

	return rl, nil
}
