package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	topologyKeyHostname            = "kubernetes.io/hostname"
	topologyKeyFailureDomainRegion = "failure-domain.beta.kubernetes.io/region"
	topologyKeyFailureDomainZone   = "failure-domain.beta.kubernetes.io/zone"
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
func newPSMDBPodAffinity(m *v1alpha1.PerconaServerMongoDB, ls map[string]string) *corev1.Affinity {
	affinity := corev1.Affinity{
		PodAffinity: &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{},
		},
		PodAntiAffinity: &corev1.PodAntiAffinity{},
	}

	hostnameAffinity := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		TopologyKey: topologyKeyHostname,
	}

	failureDomainRegionAffinity := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		TopologyKey: topologyKeyFailureDomainRegion,
	}

	failureDomainZoneAffinity := corev1.PodAffinityTerm{
		LabelSelector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		TopologyKey: topologyKeyFailureDomainZone,
	}

	// force pod to launch in specific regions, if specified
	if len(m.Spec.Mongod.Affinity.Regions) > 0 {
		affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: ls,
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      topologyKeyFailureDomainRegion,
							Operator: metav1.LabelSelectorOpIn,
							Values:   m.Spec.Mongod.Affinity.Regions,
						},
					},
				},
				TopologyKey: topologyKeyFailureDomainRegion,
			},
		)
	}

	// force pod to launch in specific zones, if specified
	if len(m.Spec.Mongod.Affinity.Zones) > 0 {
		affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution = append(
			affinity.PodAffinity.RequiredDuringSchedulingIgnoredDuringExecution,
			corev1.PodAffinityTerm{
				LabelSelector: &metav1.LabelSelector{
					MatchLabels: ls,
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      topologyKeyFailureDomainZone,
							Operator: metav1.LabelSelectorOpIn,
							Values:   m.Spec.Mongod.Affinity.Zones,
						},
					},
				},
				TopologyKey: topologyKeyFailureDomainZone,
			},
		)
	}

	// setup pod anti-affinity based on affinity mode (none, required or preferred)
	switch m.Spec.Mongod.Affinity.Mode {
	case v1alpha1.AffinityModeRequired:
		var terms []corev1.PodAffinityTerm
		if m.Spec.Mongod.Affinity.UniqueHostname {
			terms = append(terms, hostnameAffinity)
		} else if m.Spec.Mongod.Affinity.UniqueRegion {
			terms = append(terms, failureDomainRegionAffinity)
		} else if m.Spec.Mongod.Affinity.UniqueZone {
			terms = append(terms, failureDomainZoneAffinity)

		}
		affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = terms
	case v1alpha1.AffinityModePreferred:
		var terms []corev1.WeightedPodAffinityTerm
		if m.Spec.Mongod.Affinity.UniqueHostname {
			terms = append(terms, corev1.WeightedPodAffinityTerm{
				Weight:          100,
				PodAffinityTerm: hostnameAffinity,
			})
		}
		if m.Spec.Mongod.Affinity.UniqueRegion {
			terms = append(terms, corev1.WeightedPodAffinityTerm{
				Weight:          50,
				PodAffinityTerm: failureDomainRegionAffinity,
			})
		}
		if m.Spec.Mongod.Affinity.UniqueZone {
			terms = append(terms, corev1.WeightedPodAffinityTerm{
				Weight:          50,
				PodAffinityTerm: failureDomainZoneAffinity,
			})
		}
		affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = terms
	}

	return &affinity
}
