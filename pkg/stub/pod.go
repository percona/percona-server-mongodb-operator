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

// getPodContainer returns a container, if it exists
func getPodContainer(pod *corev1.Pod, containerName string) *corev1.Container {
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
	container := getPodContainer(&pod, mongodContainerName)
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
	affinity := &corev1.Affinity{}

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

	var requiredAffinityTerms []corev1.PodAffinityTerm
	var requiredAntiAffinityTerms []corev1.PodAffinityTerm
	var preferredAntiAffinityTerms []corev1.WeightedPodAffinityTerm

	// force pod to launch in specific regions, if specified
	if len(m.Spec.Mongod.Affinity.Regions) > 0 {
		requiredAffinityTerms = append(requiredAffinityTerms,
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
		requiredAffinityTerms = append(requiredAffinityTerms,
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

	// schedule pods on unique hostnames
	switch m.Spec.Mongod.Affinity.UniqueHostname {
	case v1alpha1.AffinityModeRequired:
		requiredAntiAffinityTerms = append(requiredAntiAffinityTerms, hostnameAffinity)
	case v1alpha1.AffinityModePreferred:
		preferredAntiAffinityTerms = append(preferredAntiAffinityTerms, corev1.WeightedPodAffinityTerm{
			Weight:          100,
			PodAffinityTerm: hostnameAffinity,
		})
	}

	// schedule pods on unique zones
	switch m.Spec.Mongod.Affinity.UniqueRegion {
	case v1alpha1.AffinityModeRequired:
		requiredAntiAffinityTerms = append(requiredAntiAffinityTerms, failureDomainRegionAffinity)
	case v1alpha1.AffinityModePreferred:
		preferredAntiAffinityTerms = append(preferredAntiAffinityTerms, corev1.WeightedPodAffinityTerm{
			Weight:          50,
			PodAffinityTerm: failureDomainRegionAffinity,
		})
	}

	// schedule pods on unique zones
	switch m.Spec.Mongod.Affinity.UniqueZone {
	case v1alpha1.AffinityModeRequired:
		requiredAntiAffinityTerms = append(requiredAntiAffinityTerms, failureDomainZoneAffinity)
	case v1alpha1.AffinityModePreferred:
		preferredAntiAffinityTerms = append(preferredAntiAffinityTerms, corev1.WeightedPodAffinityTerm{
			Weight:          50,
			PodAffinityTerm: failureDomainZoneAffinity,
		})
	}

	if len(requiredAffinityTerms) > 0 {
		affinity.PodAffinity = &corev1.PodAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: requiredAffinityTerms,
		}
	}
	if len(preferredAntiAffinityTerms) > 0 || len(requiredAntiAffinityTerms) > 0 {
		affinity.PodAntiAffinity = &corev1.PodAntiAffinity{}
		if len(preferredAntiAffinityTerms) > 0 {
			affinity.PodAntiAffinity.PreferredDuringSchedulingIgnoredDuringExecution = preferredAntiAffinityTerms
		}
		if len(requiredAntiAffinityTerms) > 0 {
			affinity.PodAntiAffinity.RequiredDuringSchedulingIgnoredDuringExecution = requiredAntiAffinityTerms
		}
	}

	return affinity
}
