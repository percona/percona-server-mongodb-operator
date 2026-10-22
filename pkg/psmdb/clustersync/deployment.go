package clustersync

import (
	"maps"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

const (
	ComponentPCSM = "pcsm"
	ContainerName = "pcsm"

	HTTPPortName       = "pcsm-http"
	HTTPPort     int32 = 2242

	URISecretSourceKey = "source-uri"
	URISecretTargetKey = "target-uri"

	sslVolumeName = "ssl"
	tmpVolumeName = "tmp"
	tmpMountPath  = "/tmp"

	TLSPEMFile = tmpMountPath + "/tls.pem"
	TLSCAFile  = config.SSLDir + "/ca.crt"
)

var secretFileMode int32 = 0o444

type TLS struct {
	SecretName string
	Hash       string
}

func (t TLS) Enabled() bool {
	return t.SecretName != ""
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
	return appsv1.DeploymentSpec{
		Replicas: new(int32(1)),
		Strategy: appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType},
		Selector: &metav1.LabelSelector{MatchLabels: Labels(cr)},
		Template: template,
	}
}

// PodTemplateSpec wires the PCSM container into a PodSpec.
func PodTemplateSpec(cr *api.PerconaServerMongoDBClusterSync, tls TLS) corev1.PodTemplateSpec {
	ls := Labels(cr)
	for k, v := range cr.Spec.Labels {
		if _, ok := ls[k]; !ok {
			ls[k] = v
		}
	}

	annotations := cr.Spec.Annotations
	if tls.Enabled() {
		annotations = maps.Clone(annotations)
		if annotations == nil {
			annotations = make(map[string]string)
		}
		annotations[naming.AnnotationSSLHash] = tls.Hash
	}

	return corev1.PodTemplateSpec{
		Labels:      ls,
		Annotations: annotations,
		Spec: corev1.PodSpec{
			Containers:       []corev1.Container{Container(cr, tls)},
			Volumes:          volumes(cr, tls),
			ImagePullSecrets: cr.Spec.ImagePullSecrets,
			NodeSelector:     cr.Spec.NodeSelector,
			Tolerations:      cr.Spec.Tolerations,
			Affinity:         podAffinity(cr.Spec.Affinity, ls),
			RuntimeClassName: cr.Spec.RuntimeClassName,
			SecurityContext:  cr.Spec.PodSecurityContext,
		},
	}
}

func volumes(cr *api.PerconaServerMongoDBClusterSync, tls TLS) []corev1.Volume {
	if !tls.Enabled() {
		return nil
	}

	volumes := []corev1.Volume{{
		Name: sslVolumeName,
		Secret: &corev1.SecretVolumeSource{
			SecretName:  tls.SecretName,
			DefaultMode: &secretFileMode,
		},
	}}
	if readOnlyRootFilesystemEnabled(cr.Spec.ContainerSecurityContext) {
		volumes = append(volumes, corev1.Volume{
			Name:     tmpVolumeName,
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		})
	}
	return volumes
}

func readOnlyRootFilesystemEnabled(sc *corev1.SecurityContext) bool {
	return sc != nil && sc.ReadOnlyRootFilesystem != nil && *sc.ReadOnlyRootFilesystem
}

func podAffinity(af *api.PodAffinity, labels map[string]string) *corev1.Affinity {
	if af == nil {
		return nil
	}
	switch {
	case af.Advanced != nil:
		return af.Advanced
	case af.TopologyKey != nil:
		if *af.TopologyKey == api.AffinityOff {
			return nil
		}
		labelsCopy := make(map[string]string, len(labels))
		maps.Copy(labelsCopy, labels)
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
