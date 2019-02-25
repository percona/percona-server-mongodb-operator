package psmdb

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/version"
)

// NewStatefulSet returns a StatefulSet object configured for a name
func NewStatefulSet(name, namespace string) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

var secretFileMode int32 = 0060

func StatefulSpec(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, containerName string, ls map[string]string, size int32, ikeyName string, sv *version.ServerVersion) (appsv1.StatefulSetSpec, error) {
	var fsgroup *int64
	if sv.Platform == api.PlatformKubernetes {
		var tp int64 = 1001
		fsgroup = &tp
	}

	fvar := false

	// TODO: do as the backup - serialize resources straight via cr.yaml
	resources, err := CreateResources(replset.Resources)
	if err != nil {
		return appsv1.StatefulSetSpec{}, fmt.Errorf("resource creation: %v", err)
	}

	return appsv1.StatefulSetSpec{
		ServiceName: m.Name + "-" + replset.Name,
		Replicas:    &size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ls,
			},
			Spec: corev1.PodSpec{
				Affinity:      podAffinity(ls),
				RestartPolicy: corev1.RestartPolicyAlways,
				Containers: []corev1.Container{
					container(m, replset, containerName, resources, fsgroup, ikeyName),
				},
				SecurityContext: &corev1.PodSecurityContext{
					FSGroup: fsgroup,
				},
				Volumes: []corev1.Volume{
					{
						Name: ikeyName,
						VolumeSource: corev1.VolumeSource{
							Secret: &corev1.SecretVolumeSource{
								DefaultMode: &secretFileMode,
								SecretName:  ikeyName,
								Optional:    &fvar,
							},
						},
					},
				},
			},
		},
	}, nil
}

// PersistentVolumeClaim returns a Persistent Volume Claims for Mongod pod
func PersistentVolumeClaim(name, namespace string, replset *api.ReplsetSpec) (corev1.PersistentVolumeClaim, error) {
	size, err := resource.ParseQuantity(replset.Resources.Storage)
	if err != nil {
		return corev1.PersistentVolumeClaim{}, fmt.Errorf("wrong volume size value %q: %v", replset.Resources.Storage, err)
	}

	vc := corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				corev1.ReadWriteOnce,
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: size,
				},
			},
		},
	}
	if replset.StorageClass != "" {
		vc.Spec.StorageClassName = &replset.StorageClass
	}

	return vc, nil
}

// podAffinity returns an Affinity configuration that aims to
// avoid deploying more than one pod on the same kubelet hostname
func podAffinity(ls map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{
				{
					Weight: 100,
					PodAffinityTerm: corev1.PodAffinityTerm{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: ls,
						},
						TopologyKey: "kubernetes.io/hostname",
					},
				},
			},
		},
	}
}
