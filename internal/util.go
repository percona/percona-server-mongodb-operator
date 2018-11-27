package internal

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/operator-framework/operator-sdk/pkg/k8sclient"
	corev1 "k8s.io/api/core/v1"
	kapierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// getPlatform returns the Kubernetes platform type, first using the Spec 'platform'
// field or the serverVersion.Platform field if the Spec 'platform' field is not set
func getPlatform(m *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion) v1alpha1.Platform {
	if m.Spec.Platform != nil {
		return *m.Spec.Platform
	}
	if serverVersion != nil {
		return serverVersion.Platform
	}
	return v1alpha1.PlatformKubernetes
}

// labelsForPerconaServerMongoDB returns the labels for selecting the resources
// belonging to the given PerconaServerMongoDB CR name.
func LabelsForPerconaServerMongoDB(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) map[string]string {
	return map[string]string{
		"app":                       "percona-server-mongodb",
		"percona-server-mongodb_cr": m.Name,
		"replset":                   replset.Name,
	}
}

// getLabelSelectorListOpts returns metav1.ListOptions with a label-selector for a given replset
func getLabelSelectorListOpts(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) *metav1.ListOptions {
	labelSelector := labels.SelectorFromSet(LabelsForPerconaServerMongoDB(m, replset)).String()
	return &metav1.ListOptions{LabelSelector: labelSelector}
}

// addOwnerRefToObject appends the desired OwnerReference to the object
func AddOwnerRefToObject(obj metav1.Object, ownerRef metav1.OwnerReference) {
	obj.SetOwnerReferences(append(obj.GetOwnerReferences(), ownerRef))
}

// asOwner returns an OwnerReference set as the PerconaServerMongoDB CR
func AsOwner(m *v1alpha1.PerconaServerMongoDB) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: m.APIVersion,
		Kind:       m.Kind,
		Name:       m.Name,
		UID:        m.UID,
		Controller: &TrueVar,
	}
}

// parseSpecResourceRequirements parses resource requirements to a corev1.ResourceList
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

// getServerVersion returns server version and platform (k8s|oc)
// stolen from: https://github.com/openshift/origin/blob/release-3.11/pkg/oc/cli/version/version.go#L106
func getServerVersion() (*v1alpha1.ServerVersion, error) {
	version := &v1alpha1.ServerVersion{}

	client := k8sclient.GetKubeClient().Discovery().RESTClient()

	kubeVersionBody, err := client.Get().AbsPath("/version").Do().Raw()
	switch {
	case err == nil:
		err = json.Unmarshal(kubeVersionBody, &version.Info)
		if err != nil && len(kubeVersionBody) > 0 {
			return nil, err
		}
		version.Platform = v1alpha1.PlatformKubernetes
	case kapierrors.IsNotFound(err) || kapierrors.IsUnauthorized(err) || kapierrors.IsForbidden(err):
		// this is fine! just try to get /version/openshift
	default:
		return nil, err
	}

	ocVersionBody, err := client.Get().AbsPath("/version/openshift").Do().Raw()
	switch {
	case err == nil:
		err = json.Unmarshal(ocVersionBody, &version.Info)
		if err != nil && len(ocVersionBody) > 0 {
			return nil, err
		}
		version.Platform = v1alpha1.PlatformOpenshift
	case kapierrors.IsNotFound(err) || kapierrors.IsUnauthorized(err) || kapierrors.IsForbidden(err):
	default:
		return nil, err
	}

	return version, nil
}

// GetContainerRunUID returns an int64-pointer reflecting the user ID a container
// should run as
func GetContainerRunUID(m *v1alpha1.PerconaServerMongoDB, serverVersion *v1alpha1.ServerVersion) *int64 {
	if getPlatform(m, serverVersion) != v1alpha1.PlatformOpenshift {
		return &m.Spec.RunUID
	}
	return nil
}
