package stub

import (
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (h *Handler) ensureArbiter(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec, podList *corev1.PodList) (*appsv1.Deployment, error) {
}

func arbiterMeta(namespace string, replsetName string) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Deployment",
			APIVersion: "apps/v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      namespace + "-" + replsetName,
			Namespace: namespace,
		},
	}
}

func (h *Handler) newArbiter(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) (*appsv1.Deployment, error) {
	ls := util.LabelsForPerconaServerMongoDBReplset(m, replset)
	ls["arbiter"] = "true"
	runUID := util.GetContainerRunUID(m, h.serverVersion)

	arbiter := arbiterMeta(m.Namespace, replset.Name)
	arbiter.Spec = appsv1.DeploymentSpec{
		Replicas: &replset.Arbiter.Size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ls,
			},
			Spec: corev1.PodSpec{
				Containers: h.newArbiterContainers(m, replset),

				RestartPolicy: corev1.RestartPolicyAlways,
				SecurityContext: &corev1.PodSecurityContext{
					FSGroup: runUID,
				},
				Volumes: []corev1.Volume{
					{
						Name: m.Spec.Secrets.Key,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &secretFileMode,
								SecretName:  m.Spec.Secrets.Key,
								Optional:    &util.FalseVar,
							},
						},
					},
				},
			},
		},
	}
	util.AddOwnerRefToObject(arbiter, util.AsOwner(m))
	return arbiter, nil
}

func (h *Handler) newArbiterContainers(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) []corev1.Container {
	return mongod.NewArbiterContainer(m, replset)
}
