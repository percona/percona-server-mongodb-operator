package psmdb

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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

var secretFileMode int32 = 288

// StatefulSpec returns spec for stateful set
// TODO: Unify Arbiter and Node. Shoudn't be 100500 parameters
func StatefulSpec(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, containerName string, ls map[string]string, multiAZ api.MultiAZ, size int32, ikeyName string) (appsv1.StatefulSetSpec, error) {
	fvar := false

	// TODO: do as the backup - serialize resources straight via cr.yaml
	resources, err := CreateResources(replset.Resources)
	if err != nil {
		return appsv1.StatefulSetSpec{}, fmt.Errorf("resource creation: %v", err)
	}

	for k, v := range multiAZ.Labels {
		if _, ok := ls[k]; !ok {
			ls[k] = v
		}
	}
	volumes := []corev1.Volume{
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
	}

	if *m.Spec.Mongod.Security.EnableEncryption {
		volumes = append(volumes,
			corev1.Volume{
				Name: m.Spec.Mongod.Security.EncryptionKeySecret,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						DefaultMode: &secretFileMode,
						SecretName:  m.Spec.Mongod.Security.EncryptionKeySecret,
						Optional:    &fvar,
					},
				},
			},
		)
	}

	return appsv1.StatefulSetSpec{
		ServiceName: m.Name + "-" + replset.Name,
		Replicas:    &size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      ls,
				Annotations: multiAZ.Annotations,
			},
			Spec: corev1.PodSpec{
				SecurityContext:   replset.PodSecurityContext,
				Affinity:          PodAffinity(multiAZ.Affinity, ls),
				NodeSelector:      multiAZ.NodeSelector,
				Tolerations:       multiAZ.Tolerations,
				PriorityClassName: multiAZ.PriorityClassName,
				RestartPolicy:     corev1.RestartPolicyAlways,
				ImagePullSecrets:  m.Spec.ImagePullSecrets,
				Containers: []corev1.Container{
					container(m, replset, containerName, resources, ikeyName),
				},
				Volumes:       volumes,
				SchedulerName: m.Spec.SchedulerName,
			},
		},
	}, nil
}

// PersistentVolumeClaim returns a Persistent Volume Claims for Mongod pod
func PersistentVolumeClaim(name, namespace string, spec *corev1.PersistentVolumeClaimSpec) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: *spec,
	}
}

// PodAffinity returns podAffinity options for the pod
func PodAffinity(af *api.PodAffinity, labels map[string]string) *corev1.Affinity {
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

		lablesCopy := make(map[string]string)
		for k, v := range labels {
			if k != "app.kubernetes.io/component" {
				lablesCopy[k] = v
			}
		}
		return &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: lablesCopy,
						},
						TopologyKey: *af.TopologyKey,
					},
				},
			},
		}
	}

	return nil
}
