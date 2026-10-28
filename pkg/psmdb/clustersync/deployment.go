package clustersync

import (
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

const (
	ComponentPCSM = "pcsm"
	ContainerName = "pcsm"

	HTTPPortName       = "pcsm-http"
	HTTPPort     int32 = 2242
	MetricsPath        = "/metrics"
	StatusPath         = "/status"

	URISecretSourceKey = "source-uri"
	URISecretTargetKey = "target-uri"

	defaultTopologyKey = "kubernetes.io/hostname"
)

// Size treats an unset spec.size as 1: the CRD defaults it, but objects
// stored before the field existed come back as 0.
func Size(cr *api.PerconaServerMongoDBClusterSync) int32 {
	if cr.Spec.Size < 1 {
		return 1
	}
	return cr.Spec.Size
}

func DeploymentName(cr *api.PerconaServerMongoDBClusterSync) string {
	return cr.Name + "-pcsm"
}

// TargetUserSecretName holds the syncTargetUser credentials the controller
// provisions on the target cluster.
func TargetUserSecretName(cr *api.PerconaServerMongoDBClusterSync) string {
	return cr.Name + "-pcsm-target-user"
}

func URISecretName(cr *api.PerconaServerMongoDBClusterSync) string {
	return cr.Name + "-pcsm-secret"
}

func Labels(cr *api.PerconaServerMongoDBClusterSync) map[string]string {
	l := naming.Labels()
	l[naming.LabelKubernetesInstance] = cr.Name
	l[naming.LabelKubernetesComponent] = ComponentPCSM
	return l
}

func Deployment(cr *api.PerconaServerMongoDBClusterSync) *appsv1.Deployment {
	return &appsv1.Deployment{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       DeploymentName(cr),
		Namespace:  cr.Namespace,
		Labels:     Labels(cr),
	}
}

func DeploymentSpec(cr *api.PerconaServerMongoDBClusterSync, template corev1.PodTemplateSpec) appsv1.DeploymentSpec {
	size := Size(cr)
	return appsv1.DeploymentSpec{
		Replicas: &size,
		Strategy: deploymentStrategy(size),
		Selector: &metav1.LabelSelector{MatchLabels: Labels(cr)},
		Template: template,
	}
}

// deploymentStrategy keeps Recreate for a single instance so two PCSM
// processes never overlap. Above 1, PCSM's own lease arbitrates, so a
// rolling update is safe; maxSurge stays 0 to keep group membership at
// exactly size.
func deploymentStrategy(size int32) appsv1.DeploymentStrategy {
	if size < 2 {
		return appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	}
	one := intstr.FromInt32(1)
	zero := intstr.FromInt32(0)
	return appsv1.DeploymentStrategy{
		Type: appsv1.RollingUpdateDeploymentStrategyType,
		RollingUpdate: &appsv1.RollingUpdateDeployment{
			MaxUnavailable: &one,
			MaxSurge:       &zero,
		},
	}
}

// PodTemplateSpec wires the PCSM container into a PodSpec.
func PodTemplateSpec(cr *api.PerconaServerMongoDBClusterSync, cluster *api.PerconaServerMongoDB) corev1.PodTemplateSpec {
	ls := Labels(cr)
	for k, v := range cr.Spec.Labels {
		if _, ok := ls[k]; !ok {
			ls[k] = v
		}
	}

	return corev1.PodTemplateSpec{
		Labels:      ls,
		Annotations: cr.Spec.Annotations,
		Spec: corev1.PodSpec{
			Containers:       []corev1.Container{Container(cr, cluster)},
			ImagePullSecrets: cr.Spec.ImagePullSecrets,
			NodeSelector:     cr.Spec.NodeSelector,
			Tolerations:      cr.Spec.Tolerations,
			Affinity:         podAffinity(cluster, cr.Spec.Affinity, Labels(cr)),
			RuntimeClassName: cr.Spec.RuntimeClassName,
			SecurityContext:  cr.Spec.PodSecurityContext,
		},
	}
}

// podAffinity defaults to preferred anti-affinity, from cluster crVersion
// 1.24.0 on. Preferred rather than required: an unschedulable standby
// would otherwise leave the group permanently short of replicas, and
// standbys hold no data. It must not depend on spec.size — that would make
// scaling a pod-template change and force a rollout of the whole group,
// including the active instance.
func podAffinity(cluster *api.PerconaServerMongoDB, af *api.PodAffinity, labels map[string]string) *corev1.Affinity {
	if af == nil {
		if cluster == nil || cluster.CompareVersion("1.24.0") < 0 {
			return nil
		}
		return &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{MatchLabels: maps.Clone(labels)},
						TopologyKey:   defaultTopologyKey,
					},
				}},
			},
		}
	}
	switch {
	case af.Advanced != nil:
		return af.Advanced
	case af.TopologyKey != nil:
		if *af.TopologyKey == api.AffinityOff {
			return nil
		}
		labelsCopy := maps.Clone(labels)
		return &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{MatchLabels: labelsCopy},
						TopologyKey:   *af.TopologyKey,
					},
				},
			},
		}
	}
	return nil
}
