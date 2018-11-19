package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// podList returns a v1.PodList object
func podList() *corev1.PodList {
	return &corev1.PodList{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
	}
}

// getContainer returns a container, if it exists
func getContainer(pod corev1.Pod, containerName string) *corev1.Container {
	for _, cont := range pod.Spec.Containers {
		if cont.Name == containerName {
			return &cont
		}
	}
	return nil
}

// getPodNames returns the pod names of the array of pods passed in
func getPodNames(pods []corev1.Pod) []string {
	var podNames []string
	for _, pod := range pods {
		podNames = append(podNames, pod.Name)
	}
	return podNames
}

// statusHasPod returns a boolean reflecting if a ReplsetSTatus contains a
// pod name
func statusHasPod(status *v1alpha1.ReplsetStatus, podName string) bool {
	for _, pod := range status.Pods {
		if pod == podName {
			return true
		}
	}
	return false
}

// isMongodPod returns a boolean reflecting if a pod
// is running a mongod container
func isMongodPod(pod corev1.Pod) bool {
	container := getContainer(pod, mongodContainerName)
	return container != nil
}

// isPodReady returns a boolean reflecting if a pod is in a "ready" state
func isPodReady(pod corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Status != corev1.ConditionTrue {
			continue
		}
		if condition.Type == corev1.PodReady {
			return true
		}
	}
	return false
}

// newPSMDBPodAffinity returns an Affinity configuration that aims to avoid deploying more than
// one pod on the same Kubernetes failure-domain zone (failure-domain.beta.kubernetes.io/zone)
// and hostname (kubernetes.io/hostname)
func newPSMDBPodAffinity(replset *v1alpha1.ReplsetSpec, ls map[string]string) *corev1.Affinity {
	hostnameAffinity := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		TopologyKey: "kubernetes.io/hostname",
	}
	failureDomainZoneAffinity := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		TopologyKey: "failure-domain.beta.kubernetes.io/zone",
	}
	switch replset.Affinity.Mode {
	case v1alpha1.AffinityModeRequired:
		var terms []corev1.PodAffinityTerm
		if replset.Affinity.UniqueHostname {
			terms = append(terms, hostnameAffinity)
		} else if replset.Affinity.UniqueZone {
			terms = append(terms, failureDomainZoneAffinity)

		}
		return &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: terms,
			},
		}
	default:
		var terms []corev1.WeightedPodAffinityTerm
		if replset.Affinity.UniqueHostname {
			terms = append(terms, corev1.WeightedPodAffinityTerm{
				Weight:          100,
				PodAffinityTerm: hostnameAffinity,
			})
		}
		if replset.Affinity.UniqueZone {
			terms = append(terms, corev1.WeightedPodAffinityTerm{
				Weight:          100,
				PodAffinityTerm: failureDomainZoneAffinity,
			})
		}
		return &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: terms,
			},
		}
	}
}
