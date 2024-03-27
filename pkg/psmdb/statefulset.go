package psmdb

import (
	"context"
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

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
func StatefulSpec(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec,
	ls map[string]string, initImage string, customConf CustomConfig, usersSecret *corev1.Secret,
) (appsv1.StatefulSetSpec, error) {
	log := logf.FromContext(ctx)
	size := replset.Size
	containerName := "mongod"
	multiAZ := replset.MultiAZ
	resources := replset.Resources
	volumeSpec := replset.VolumeSpec
	podSecurityContext := replset.PodSecurityContext
	containerSecurityContext := replset.ContainerSecurityContext
	livenessProbe := replset.LivenessProbe
	readinessProbe := replset.ReadinessProbe
	configName := MongodCustomConfigName(cr.Name, replset.Name)

	switch ls["app.kubernetes.io/component"] {
	case "arbiter":
		containerName += "-arbiter"
		size = replset.Arbiter.Size
		multiAZ = replset.Arbiter.MultiAZ
		resources = replset.Arbiter.Resources
	case "nonVoting":
		containerName += "-nv"
		size = replset.NonVoting.Size
		multiAZ = replset.NonVoting.MultiAZ
		resources = replset.NonVoting.Resources
		podSecurityContext = replset.NonVoting.PodSecurityContext
		containerSecurityContext = replset.NonVoting.ContainerSecurityContext
		configName = MongodCustomConfigName(cr.Name, replset.Name+"-nv")
		livenessProbe = replset.NonVoting.LivenessProbe
		readinessProbe = replset.NonVoting.ReadinessProbe
		volumeSpec = replset.NonVoting.VolumeSpec
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

	fvar := false

	volumes := []corev1.Volume{
		{
			Name: InternalKey(cr),
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					DefaultMode: &secretFileMode,
					SecretName:  InternalKey(cr),
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
					Name: cr.Spec.Secrets.EncryptionKey,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							DefaultMode: &secretFileMode,
							SecretName:  cr.Spec.Secrets.EncryptionKey,
							Optional:    &fvar,
						},
					},
				},
			)
		}
	}

	c, err := container(ctx, cr, replset, containerName, resources, InternalKey(cr), customConf.Type.IsUsable(),
		livenessProbe, readinessProbe, containerSecurityContext)
	if err != nil {
		return appsv1.StatefulSetSpec{}, fmt.Errorf("failed to create container %v", err)
	}

	initContainers := InitContainers(cr, initImage)
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

	volumeClaimTemplates := []corev1.PersistentVolumeClaim{}

	// add TLS/SSL Volume
	t := true
	volumes = append(volumes,
		corev1.Volume{
			Name: "ssl",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.Secrets.SSL,
					Optional:    &cr.Spec.UnsafeConf,
					DefaultMode: &secretFileMode,
				},
			},
		},
		corev1.Volume{
			Name: "ssl-internal",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.Secrets.SSLInternal,
					Optional:    &t,
					DefaultMode: &secretFileMode,
				},
			},
		},
		corev1.Volume{
			Name: "users-secret-file",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: api.InternalUserSecretName(cr),
				},
			},
		},
	)
	if cr.CompareVersion("1.16.0") >= 0 && cr.Spec.Secrets.LDAPSecret != "" {
		volumes = append(volumes,
			corev1.Volume{
				Name: LDAPTLSVolClaimName,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  cr.Spec.Secrets.LDAPSecret,
						Optional:    &t,
						DefaultMode: &secretFileMode,
					},
				},
			},
			corev1.Volume{
				Name: LDAPConfVolClaimName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)
	}

	if ls["app.kubernetes.io/component"] == "arbiter" {
		volumes = append(volumes,
			corev1.Volume{
				Name: MongodDataVolClaimName,
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				},
			},
		)
	} else {
		if volumeSpec.PersistentVolumeClaim.PersistentVolumeClaimSpec != nil {
			volumeClaimTemplates = []corev1.PersistentVolumeClaim{
				PersistentVolumeClaim(MongodDataVolClaimName, cr.Namespace, volumeSpec),
			}
		} else {
			volumes = append(volumes,
				corev1.Volume{
					Name: MongodDataVolClaimName,
					VolumeSource: corev1.VolumeSource{
						HostPath: volumeSpec.HostPath,
						EmptyDir: volumeSpec.EmptyDir,
					},
				},
			)
		}

		if cr.Spec.Backup.Enabled {
			rsName := replset.Name
			if name, err := replset.CustomReplsetName(); err == nil {
				rsName = name
			}
			containers = append(containers, backupAgentContainer(cr, rsName))
		}

		pmmC := AddPMMContainer(ctx, cr, usersSecret, cr.Spec.PMM.MongodParams)
		if pmmC != nil {
			containers = append(containers, *pmmC)
		}
	}

	volumes = multiAZ.WithSidecarVolumes(logf.FromContext(ctx), volumes)
	volumeClaimTemplates = multiAZ.WithSidecarPVCs(logf.FromContext(ctx), volumeClaimTemplates)

	var updateStrategy appsv1.StatefulSetUpdateStrategy
	switch cr.Spec.UpdateStrategy {
	case appsv1.OnDeleteStatefulSetStrategyType:
		updateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
	case api.SmartUpdateStatefulSetStrategyType:
		updateStrategy = appsv1.StatefulSetUpdateStrategy{Type: appsv1.OnDeleteStatefulSetStrategyType}
	default:
		var zero int32 = 0
		updateStrategy = appsv1.StatefulSetUpdateStrategy{
			Type: appsv1.RollingUpdateStatefulSetStrategyType,
			RollingUpdate: &appsv1.RollingUpdateStatefulSetStrategy{
				Partition: &zero,
			},
		}
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
				HostAliases:                   replset.HostAliases,
				SecurityContext:               podSecurityContext,
				Affinity:                      PodAffinity(cr, multiAZ.Affinity, customLabels),
				TopologySpreadConstraints:     PodTopologySpreadConstraints(cr, multiAZ.TopologySpreadConstraints, customLabels),
				NodeSelector:                  multiAZ.NodeSelector,
				Tolerations:                   multiAZ.Tolerations,
				TerminationGracePeriodSeconds: multiAZ.TerminationGracePeriodSeconds,
				PriorityClassName:             multiAZ.PriorityClassName,
				ServiceAccountName:            multiAZ.ServiceAccountName,
				RestartPolicy:                 corev1.RestartPolicyAlways,
				ImagePullSecrets:              cr.Spec.ImagePullSecrets,
				Containers:                    containers,
				InitContainers:                initContainers,
				Volumes:                       volumes,
				SchedulerName:                 cr.Spec.SchedulerName,
				RuntimeClassName:              multiAZ.RuntimeClassName,
			},
		},
		UpdateStrategy:       updateStrategy,
		VolumeClaimTemplates: volumeClaimTemplates,
	}, nil
}

const agentContainerName = "backup-agent"

// backupAgentContainer creates the container object for a backup agent
func backupAgentContainer(cr *api.PerconaServerMongoDB, replsetName string) corev1.Container {
	fvar := false
	usersSecretName := api.UserSecretName(cr)

	c := corev1.Container{
		Name:            agentContainerName,
		Image:           cr.Spec.Backup.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Env: []corev1.EnvVar{
			{
				Name: "PBM_AGENT_MONGODB_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: "MONGODB_BACKUP_USER",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: usersSecretName,
						},
						Optional: &fvar,
					},
				},
			},
			{
				Name: "PBM_AGENT_MONGODB_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: "MONGODB_BACKUP_PASSWORD",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: usersSecretName,
						},
						Optional: &fvar,
					},
				},
			},
			{
				Name:  "PBM_MONGODB_REPLSET",
				Value: replsetName,
			},
			{
				Name:  "PBM_MONGODB_PORT",
				Value: strconv.Itoa(int(api.DefaultMongodPort)),
			},
		},
		SecurityContext: cr.Spec.Backup.ContainerSecurityContext,
		Resources:       cr.Spec.Backup.Resources,
	}

	if cr.CompareVersion("1.13.0") >= 0 {
		c.Command = []string{BinMountPath + "/pbm-entry.sh"}
		c.Args = []string{"pbm-agent"}
		if cr.CompareVersion("1.14.0") >= 0 {
			c.Args = []string{"pbm-agent-entrypoint"}
			c.Env = append(c.Env, []corev1.EnvVar{
				{
					Name:  "PBM_AGENT_SIDECAR",
					Value: "true",
				},
				{
					Name:  "PBM_AGENT_SIDECAR_SLEEP",
					Value: "5",
				},
			}...)
		}
		c.VolumeMounts = append(c.VolumeMounts, []corev1.VolumeMount{
			{
				Name:      "ssl",
				MountPath: SSLDir,
				ReadOnly:  true,
			},
			{
				Name:      BinVolumeName,
				MountPath: BinMountPath,
				ReadOnly:  true,
			},
		}...)
	}

	if cr.Spec.Sharding.Enabled {
		c.Env = append(c.Env, corev1.EnvVar{Name: "SHARDED", Value: "TRUE"})
	}

	if cr.CompareVersion("1.14.0") >= 0 {
		c.Env = append(c.Env, []corev1.EnvVar{
			{
				Name: "POD_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
			{
				Name:  "PBM_MONGODB_URI",
				Value: "mongodb://$(PBM_AGENT_MONGODB_USERNAME):$(PBM_AGENT_MONGODB_PASSWORD)@$(POD_NAME)",
			},
		}...)

		c.VolumeMounts = append(c.VolumeMounts, []corev1.VolumeMount{
			{
				Name:      "mongod-data",
				MountPath: MongodContainerDataDir,
				ReadOnly:  false,
			},
		}...)
	}

	return c
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

func PodTopologySpreadConstraints(cr *api.PerconaServerMongoDB, tscs []corev1.TopologySpreadConstraint, ls map[string]string) []corev1.TopologySpreadConstraint {
	result := make([]corev1.TopologySpreadConstraint, 0, len(tscs))

	for _, tsc := range tscs {
		if tsc.LabelSelector == nil && tsc.MatchLabelKeys == nil {
			tsc.LabelSelector = &metav1.LabelSelector{
				MatchLabels: ls,
			}
		}

		result = append(result, tsc)
	}
	return result
}

func isEncryptionEnabled(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) (bool, error) {
	enabled, err := replset.Configuration.IsEncryptionEnabled()
	if err != nil {
		return false, errors.Wrap(err, "failed to parse replset configuration")
	}
	if enabled == nil {
		return true, nil // true by default
	}
	return *enabled, nil
}
