package psmdb

import (
	"context"
	"fmt"

	"github.com/pkg/errors"

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
func StatefulSpec(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, containerName string,
	ls map[string]string, customLabels map[string]string, multiAZ api.MultiAZ, size int32, ikeyName string,
	initContainers []corev1.Container, log logr.Logger, customConf CustomConfig,
	resources corev1.ResourceRequirements, podSecurityContext *corev1.PodSecurityContext,
	containerSecurityContext *corev1.SecurityContext, livenessProbe *api.LivenessProbeExtended,
	readinessProbe *corev1.Probe, configName string) (appsv1.StatefulSetSpec, error) {

	fvar := false

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

	if cr.CompareVersion("1.13.0") >= 0 {
		volumes = append(volumes, corev1.Volume{
			Name: BinVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	if cr.CompareVersion("1.9.0") >= 0 && customConf.Type.IsUsable() {
		volumes = append(volumes, corev1.Volume{
			Name:         "config",
			VolumeSource: customConf.Type.VolumeSource(configName),
		})
	}
	encryptionEnabled, err := isEncryptionEnabled(cr, replset)
	if err != nil {
		return appsv1.StatefulSetSpec{}, errors.Wrap(err, "failed to check if encryption is enabled")
	}

	if encryptionEnabled {
		if len(cr.Spec.Secrets.Vault) != 0 {
			volumes = append(volumes,
				corev1.Volume{
					Name: cr.Spec.Secrets.Vault,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							DefaultMode: &secretFileMode,
							SecretName:  cr.Spec.Secrets.Vault,
							Optional:    &fvar,
						},
					},
				},
			)
		} else {
			volumes = append(volumes,
				corev1.Volume{
					Name: cr.Spec.EncryptionKeySecretName(),
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							DefaultMode: &secretFileMode,
							SecretName:  cr.Spec.EncryptionKeySecretName(),
							Optional:    &fvar,
						},
					},
				},
			)
		}
	}

	c, err := container(ctx, cr, replset, containerName, resources, ikeyName, customConf.Type.IsUsable(),
		livenessProbe, readinessProbe, containerSecurityContext)
	if err != nil {
		return appsv1.StatefulSetSpec{}, fmt.Errorf("failed to create container %v", err)
	}

	for i := range initContainers {
		initContainers[i].Resources = c.Resources
	}

	containers, ok := multiAZ.WithSidecars(c)
	if !ok {
		log.Info("Wrong sidecar container name, it is skipped", "containerName", c.Name)
	}

	annotations := multiAZ.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if cr.CompareVersion("1.9.0") >= 0 && customConf.Type.IsUsable() {
		annotations["percona.com/configuration-hash"] = customConf.HashHex
	}

	return appsv1.StatefulSetSpec{
		ServiceName: cr.Name + "-" + replset.Name,
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
				Affinity:           PodAffinity(cr, multiAZ.Affinity, customLabels),
				NodeSelector:       multiAZ.NodeSelector,
				Tolerations:        multiAZ.Tolerations,
				PriorityClassName:  multiAZ.PriorityClassName,
				ServiceAccountName: multiAZ.ServiceAccountName,
				RestartPolicy:      corev1.RestartPolicyAlways,
				ImagePullSecrets:   cr.Spec.ImagePullSecrets,
				Containers:         containers,
				InitContainers:     initContainers,
				Volumes:            volumes,
				SchedulerName:      cr.Spec.SchedulerName,
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
func PersistentVolumeClaim(name, namespace string, spec *api.VolumeSpec) corev1.PersistentVolumeClaim {
	pvc := corev1.PersistentVolumeClaim{
		TypeMeta: metav1.TypeMeta{
			Kind:       "PersistentVolumeClaim",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	if spec.PersistentVolumeClaim.PersistentVolumeClaimSpec != nil {
		pvc.Spec = *spec.PersistentVolumeClaim.PersistentVolumeClaimSpec
	}
	return pvc
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

func isEncryptionEnabled(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) (bool, error) {
	if cr.CompareVersion("1.12.0") >= 0 {
		enabled, err := replset.Configuration.IsEncryptionEnabled()
		if err != nil {
			return false, errors.Wrap(err, "failed to parse replset configuration")
		}
		if enabled == nil {
			if cr.Spec.Mongod.Security != nil && cr.Spec.Mongod.Security.EnableEncryption != nil {
				return *cr.Spec.Mongod.Security.EnableEncryption, nil
			}
			return true, nil // true by default
		}
		return *enabled, nil
	}
	return *cr.Spec.Mongod.Security.EnableEncryption, nil
}
