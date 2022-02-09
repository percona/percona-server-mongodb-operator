package psmdb

import (
	"fmt"

	"github.com/go-logr/logr"

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
func StatefulSpec(m *api.PerconaServerMongoDB, replset *api.ReplsetSpec, containerName string,
	ls map[string]string, multiAZ api.MultiAZ, size int32, ikeyName string,
	initContainers []corev1.Container, log logr.Logger, customConf CustomConfig,
	resourcesSpec *api.ResourcesSpec, podSecurityContext *corev1.PodSecurityContext,
	containerSecurityContext *corev1.SecurityContext, livenessProbe *api.LivenessProbeExtended,
	readinessProbe *corev1.Probe, configuration string, configName string) (appsv1.StatefulSetSpec, error) {

	fvar := false

	// TODO: do as the backup - serialize resources straight via cr.yaml
	resources, err := CreateResources(resourcesSpec)
	if err != nil {
		return appsv1.StatefulSetSpec{}, fmt.Errorf("resource creation: %v", err)
	}

	customLabels := make(map[string]string, len(ls))
	for k, v := range ls {
		customLabels[k] = v
	}

	for k, v := range multiAZ.Labels {
		if _, ok := customLabels[k]; !ok {
			customLabels[k] = v
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

	if customConf.Type.IsUsable() {
		volumes = append(volumes, corev1.Volume{
			Name:         "config",
			VolumeSource: customConf.Type.VolumeSource(configName),
		})
	}

	volumes = append(volumes,
		corev1.Volume{
			Name: m.Spec.EncryptionKeySecretName(),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: &secretFileMode,
					SecretName:  m.Spec.EncryptionKeySecretName(),
					Optional:    &fvar,
				},
			},
		},
	)

	c, err := container(m, replset, containerName, resources, ikeyName, customConf.Type.IsUsable(),
		livenessProbe, readinessProbe, containerSecurityContext)
	if err != nil {
		return appsv1.StatefulSetSpec{}, fmt.Errorf("failed to create container %v", err)
	}

	for i := range initContainers {
		initContainers[i].Resources.Limits = c.Resources.Limits
		initContainers[i].Resources.Requests = c.Resources.Requests
	}

	containers, ok := multiAZ.WithSidecars(c)
	if !ok {
		log.Info(fmt.Sprintf("Sidecar container name cannot be %s. It's skipped", c.Name))
	}

	annotations := multiAZ.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if customConf.Type.IsUsable() {
		annotations["percona.com/configuration-hash"] = customConf.HashHex
	}

	return appsv1.StatefulSetSpec{
		ServiceName: m.Name + "-" + replset.Name,
		Replicas:    &size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      customLabels,
				Annotations: annotations,
			},
			Spec: corev1.PodSpec{
				SecurityContext:    podSecurityContext,
				Affinity:           PodAffinity(m, multiAZ.Affinity, customLabels),
				NodeSelector:       multiAZ.NodeSelector,
				Tolerations:        multiAZ.Tolerations,
				PriorityClassName:  multiAZ.PriorityClassName,
				ServiceAccountName: multiAZ.ServiceAccountName,
				RestartPolicy:      corev1.RestartPolicyAlways,
				ImagePullSecrets:   m.Spec.ImagePullSecrets,
				Containers:         containers,
				InitContainers:     initContainers,
				Volumes:            volumes,
				SchedulerName:      m.Spec.SchedulerName,
				RuntimeClassName:   multiAZ.RuntimeClassName,
			},
		},
	}, nil
}

func MongodCustomConfigName(clusterName, replicaSetName string) string {
	return fmt.Sprintf("%s-%s-mongod", clusterName, replicaSetName)
}

func MongosCustomConfigName(clusterName string) string {
	return clusterName + "-mongos"
}

// PersistentVolumeClaim returns a Persistent Volume Claims for Mongod pod
func PersistentVolumeClaim(name, namespace string, labels map[string]string, spec *corev1.PersistentVolumeClaimSpec) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: *spec,
	}
}

// PodAffinity returns podAffinity options for the pod
func PodAffinity(cr *api.PerconaServerMongoDB, af *api.PodAffinity, labels map[string]string) *corev1.Affinity {
	if af == nil {
		return nil
	}

	labelsCopy := make(map[string]string)
	for k, v := range labels {
		labelsCopy[k] = v
	}

	if cr.CompareVersion("1.6.0") < 0 {
		delete(labelsCopy, "app.kubernetes.io/component")
	}

	switch {
	case af.Advanced != nil:
		return af.Advanced
	case af.TopologyKey != nil:
		if *af.TopologyKey == api.AffinityOff {
			return nil
		}

		return &corev1.Affinity{
			PodAntiAffinity: &corev1.PodAntiAffinity{
				RequiredDuringSchedulingIgnoredDuringExecution: []corev1.PodAffinityTerm{
					{
						LabelSelector: &metav1.LabelSelector{
							MatchLabels: labelsCopy,
						},
						TopologyKey: *af.TopologyKey,
					},
				},
			},
		}
	}

	return nil
}
