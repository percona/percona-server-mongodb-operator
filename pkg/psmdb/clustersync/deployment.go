package clustersync

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

const (
	ComponentPCSM = "pcsm"
	ContainerName = "pcsm"

	HTTPPortName       = "pcsm-http"
	HTTPPort     int32 = 2242

	URISecretSourceKey = "source-uri"
	URISecretTargetKey = "target-uri"
)

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
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      DeploymentName(cr),
			Namespace: cr.Namespace,
			Labels:    Labels(cr),
		},
	}
}

func DeploymentSpec(cr *api.PerconaServerMongoDBClusterSync, template corev1.PodTemplateSpec) appsv1.DeploymentSpec {
	return appsv1.DeploymentSpec{
		Replicas: ptr.To(int32(1)),
		Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
		Selector: &metav1.LabelSelector{MatchLabels: Labels(cr)},
		Template: template,
	}
}

// PodTemplateSpec wires the PCSM container into a PodSpec.
func PodTemplateSpec(cr *api.PerconaServerMongoDBClusterSync) corev1.PodTemplateSpec {
	ls := Labels(cr)
	for k, v := range cr.Spec.Labels {
		if _, ok := ls[k]; !ok {
			ls[k] = v
		}
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      ls,
			Annotations: cr.Spec.Annotations,
		},
		Spec: corev1.PodSpec{
			Containers:       []corev1.Container{Container(cr)},
			ImagePullSecrets: cr.Spec.ImagePullSecrets,
			NodeSelector:     cr.Spec.NodeSelector,
			Tolerations:      cr.Spec.Tolerations,
			RuntimeClassName: cr.Spec.RuntimeClassName,
			SecurityContext:  cr.Spec.PodSecurityContext,
			RestartPolicy:    corev1.RestartPolicyAlways,
		},
	}
}
