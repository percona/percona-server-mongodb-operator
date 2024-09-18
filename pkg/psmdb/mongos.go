package psmdb

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func MongosStatefulset(cr *api.PerconaServerMongoDB) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.MongosNamespacedName().Name,
			Namespace: cr.MongosNamespacedName().Namespace,
			Labels:    naming.MongosLabels(cr),
		},
	}
}

func MongosStatefulsetSpec(cr *api.PerconaServerMongoDB, template corev1.PodTemplateSpec) appsv1.StatefulSetSpec {
	var updateStrategy appsv1.StatefulSetUpdateStrategy
	switch cr.Spec.UpdateStrategy {
	case api.SmartUpdateStatefulSetStrategyType, appsv1.OnDeleteStatefulSetStrategyType:
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
		Replicas: &cr.Spec.Sharding.Mongos.Size,
		Selector: &metav1.LabelSelector{
			MatchLabels: naming.MongosLabels(cr),
		},
		Template:       template,
		UpdateStrategy: updateStrategy,
	}
}

func MongosTemplateSpec(cr *api.PerconaServerMongoDB, initImage string, log logr.Logger, customConf CustomConfig, cfgInstances []string) (corev1.PodTemplateSpec, error) {
	ls := naming.MongosLabels(cr)

	if cr.Spec.Sharding.Mongos.Labels != nil {
		for k, v := range cr.Spec.Sharding.Mongos.Labels {
			ls[k] = v
		}
	}

	c, err := mongosContainer(cr, customConf.Type.IsUsable(), cfgInstances)
	if err != nil {
		return corev1.PodTemplateSpec{}, fmt.Errorf("failed to create container %v", err)
	}

	initContainers := InitContainers(cr, initImage)
	for i := range initContainers {
		initContainers[i].Resources = c.Resources
	}

	containers, ok := cr.Spec.Sharding.Mongos.MultiAZ.WithSidecars(c)
	if !ok {
		log.Info("Wrong sidecar container name, it is skipped", "containerName", c.Name)
	}

	annotations := cr.Spec.Sharding.Mongos.MultiAZ.Annotations
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if cr.CompareVersion("1.9.0") >= 0 && customConf.Type.IsUsable() {
		annotations["percona.com/configuration-hash"] = customConf.HashHex
	}

	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels:      ls,
			Annotations: annotations,
		},
		Spec: corev1.PodSpec{
			HostAliases:                   cr.Spec.Sharding.Mongos.HostAliases,
			SecurityContext:               cr.Spec.Sharding.Mongos.PodSecurityContext,
			Affinity:                      PodAffinity(cr, cr.Spec.Sharding.Mongos.MultiAZ.Affinity, ls),
			TopologySpreadConstraints:     PodTopologySpreadConstraints(cr, cr.Spec.Sharding.Mongos.MultiAZ.TopologySpreadConstraints, ls),
			NodeSelector:                  cr.Spec.Sharding.Mongos.MultiAZ.NodeSelector,
			Tolerations:                   cr.Spec.Sharding.Mongos.MultiAZ.Tolerations,
			TerminationGracePeriodSeconds: cr.Spec.Sharding.Mongos.MultiAZ.TerminationGracePeriodSeconds,
			PriorityClassName:             cr.Spec.Sharding.Mongos.MultiAZ.PriorityClassName,
			ServiceAccountName:            cr.Spec.Sharding.Mongos.MultiAZ.ServiceAccountName,
			RestartPolicy:                 corev1.RestartPolicyAlways,
			ImagePullSecrets:              cr.Spec.ImagePullSecrets,
			Containers:                    containers,
			InitContainers:                initContainers,
			Volumes:                       volumes(cr, customConf.Type),
			SchedulerName:                 cr.Spec.SchedulerName,
			RuntimeClassName:              cr.Spec.Sharding.Mongos.MultiAZ.RuntimeClassName,
		},
	}, nil
}

func mongosContainer(cr *api.PerconaServerMongoDB, useConfigFile bool, cfgInstances []string) (corev1.Container, error) {
	fvar := false

	volumes := []corev1.VolumeMount{
		{
			Name:      MongodDataVolClaimName,
			MountPath: MongodContainerDataDir,
		},
		{
			Name:      InternalKey(cr),
			MountPath: mongodSecretsDir,
			ReadOnly:  true,
		},
		{
			Name:      "ssl",
			MountPath: SSLDir,
			ReadOnly:  true,
		},
		{
			Name:      "ssl-internal",
			MountPath: sslInternalDir,
			ReadOnly:  true,
		},
	}

	if cr.CompareVersion("1.9.0") >= 0 && useConfigFile {
		volumes = append(volumes, corev1.VolumeMount{
			Name:      "config",
			MountPath: mongosConfigDir,
		})
	}

	if cr.CompareVersion("1.8.0") >= 0 {
		volumes = append(volumes, corev1.VolumeMount{
			Name:      "users-secret-file",
			MountPath: "/etc/users-secret",
			ReadOnly:  true,
		})
	}

	if cr.CompareVersion("1.14.0") >= 0 {
		volumes = append(volumes, corev1.VolumeMount{Name: BinVolumeName, MountPath: BinMountPath})
	}

	if cr.CompareVersion("1.16.0") >= 0 && cr.Spec.Secrets.LDAPSecret != "" {
		volumes = append(volumes, []corev1.VolumeMount{
			{
				Name:      LDAPTLSVolClaimName,
				MountPath: ldapTLSDir,
				ReadOnly:  true,
			},
			{
				Name:      LDAPConfVolClaimName,
				MountPath: ldapConfDir,
			},
		}...)
	}

	container := corev1.Container{
		Name:            "mongos",
		Image:           cr.Spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Args:            mongosContainerArgs(cr, useConfigFile, cfgInstances),
		Ports: []corev1.ContainerPort{
			{
				Name:          mongosPortName,
				HostPort:      cr.Spec.Sharding.Mongos.HostPort,
				ContainerPort: cr.Spec.Sharding.Mongos.Port,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "MONGODB_PORT",
				Value: strconv.Itoa(int(cr.Spec.Sharding.Mongos.Port)),
			},
		},
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: cr.Spec.Secrets.Users,
					},
					Optional: &fvar,
				},
			},
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: api.UserSecretName(cr),
					},
					Optional: &fvar,
				},
			},
		},
		WorkingDir:      MongodContainerDataDir,
		LivenessProbe:   &cr.Spec.Sharding.Mongos.LivenessProbe.Probe,
		ReadinessProbe:  cr.Spec.Sharding.Mongos.ReadinessProbe,
		SecurityContext: cr.Spec.Sharding.Mongos.ContainerSecurityContext,
		Resources:       cr.Spec.Sharding.Mongos.Resources,
		VolumeMounts:    volumes,
		Command:         []string{"/data/db/ps-entry.sh"},
	}

	if cr.CompareVersion("1.14.0") >= 0 {
		container.Command = []string{BinMountPath + "/ps-entry.sh"}
	}

	if cr.CompareVersion("1.15.0") >= 0 {
		container.LivenessProbe.Exec.Command[0] = "/opt/percona/mongodb-healthcheck"
		container.ReadinessProbe.Exec.Command[0] = "/opt/percona/mongodb-healthcheck"
	}

	return container, nil
}

func mongosContainerArgs(cr *api.PerconaServerMongoDB, useConfigFile bool, cfgInstances []string) []string {
	msSpec := cr.Spec.Sharding.Mongos
	cfgRs := cr.Spec.Sharding.ConfigsvrReplSet

	cfgRsName := cfgRs.Name
	name, err := cfgRs.CustomReplsetName()
	if err == nil {
		cfgRsName = name
	}

	// sort config instances to prevent unnecessary updates
	sort.Strings(cfgInstances)
	configDB := fmt.Sprintf("%s/%s", cfgRsName, strings.Join(cfgInstances, ","))
	args := []string{
		"mongos",
		"--bind_ip_all",
		"--port=" + strconv.Itoa(int(msSpec.Port)),
		"--sslAllowInvalidCertificates",
		"--configdb",
		configDB,
	}
	if cr.CompareVersion("1.7.0") >= 0 {
		args = append(args,
			"--relaxPermChecks",
		)
	}

	if cr.TLSEnabled() {
		if !*cr.Spec.TLS.AllowInvalidCertificates {
			// remove --sslAllowInvalidCertificates
			args = append(args[:3], args[3+1:]...)
		}

		args = append(args,
			"--clusterAuthMode=x509",
		)
	} else if (cr.CompareVersion("1.16.0") >= 0 && cr.Spec.Unsafe.TLS) || (cr.CompareVersion("1.16.0") < 0 && cr.Spec.UnsafeConf) {
		args = append(args,
			"--clusterAuthMode=keyFile",
			"--keyFile="+mongodSecretsDir+"/mongodb-key",
		)
	}

	if cr.CompareVersion("1.16.0") >= 0 {
		args = append(args, "--tlsMode="+string(cr.Spec.TLS.Mode))
	}

	if msSpec.SetParameter != nil {
		if msSpec.SetParameter.CursorTimeoutMillis > 0 {
			args = append(args,
				"--setParameter",
				"cursorTimeoutMillis="+strconv.Itoa(msSpec.SetParameter.CursorTimeoutMillis),
			)
		}
	}

	if useConfigFile {
		args = append(args, fmt.Sprintf("--config=%s/mongos.conf", mongosConfigDir))
	}

	return args
}

func volumes(cr *api.PerconaServerMongoDB, configSource VolumeSourceType) []corev1.Volume {
	fvar, tvar := false, true

	sslVolumeOptional := &cr.Spec.UnsafeConf
	if cr.CompareVersion("1.16.0") >= 0 {
		sslVolumeOptional = &cr.Spec.Unsafe.TLS
	}

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
		{
			Name: "ssl",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  api.SSLSecretName(cr),
					Optional:    sslVolumeOptional,
					DefaultMode: &secretFileMode,
				},
			},
		},
		{
			Name: "ssl-internal",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  api.SSLInternalSecretName(cr),
					Optional:    &tvar,
					DefaultMode: &secretFileMode,
				},
			},
		},
		{
			Name: MongodDataVolClaimName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	}

	if cr.CompareVersion("1.8.0") >= 0 {
		volumes = append(volumes, corev1.Volume{
			Name: "users-secret-file",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: api.InternalUserSecretName(cr),
				},
			},
		})
	}

	if cr.CompareVersion("1.11.0") >= 0 && cr.Spec.Sharding.Mongos != nil {
		volumes = append(volumes, cr.Spec.Sharding.Mongos.SidecarVolumes...)

		for _, v := range cr.Spec.Sharding.Mongos.SidecarPVCs {
			volumes = append(volumes, corev1.Volume{
				Name: v.Name,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: v.Name,
					},
				},
			})
		}
	}

	if cr.CompareVersion("1.9.0") >= 0 && configSource.IsUsable() {
		volumes = append(volumes, corev1.Volume{
			Name:         "config",
			VolumeSource: configSource.VolumeSource(MongosCustomConfigName(cr.Name)),
		})
	}

	if cr.CompareVersion("1.13.0") >= 0 {
		volumes = append(volumes, corev1.Volume{
			Name: BinVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		})
	}

	if cr.CompareVersion("1.16.0") >= 0 {
		if cr.Spec.Secrets.LDAPSecret != "" {
			volumes = append(volumes, []corev1.Volume{
				{
					Name: LDAPTLSVolClaimName,
					VolumeSource: corev1.VolumeSource{
						Secret: &corev1.SecretVolumeSource{
							SecretName:  cr.Spec.Secrets.LDAPSecret,
							Optional:    &tvar,
							DefaultMode: &secretFileMode,
						},
					},
				},
				{
					Name: LDAPConfVolClaimName,
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
			}...)
		}
	}

	return volumes
}

func MongosService(cr *api.PerconaServerMongoDB, name string) corev1.Service {
	svc := corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cr.Namespace,
		},
	}
	if cr.CompareVersion("1.12.0") >= 0 {
		svc.Labels = naming.MongosLabels(cr)
	}

	if cr.Spec.Sharding.Mongos != nil {
		svc.Annotations = cr.Spec.Sharding.Mongos.Expose.ServiceAnnotations
		for k, v := range cr.Spec.Sharding.Mongos.Expose.ServiceLabels {
			if _, ok := svc.Labels[k]; !ok {
				svc.Labels[k] = v
			}
		}
	}

	return svc
}

func MongosServiceSpec(cr *api.PerconaServerMongoDB, podName string) corev1.ServiceSpec {
	ls := naming.MongosLabels(cr)

	if cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
		ls["statefulset.kubernetes.io/pod-name"] = podName
	}
	spec := corev1.ServiceSpec{
		Ports: []corev1.ServicePort{
			{
				Name:       mongosPortName,
				Port:       cr.Spec.Sharding.Mongos.Port,
				TargetPort: intstr.FromInt(int(cr.Spec.Sharding.Mongos.Port)),
			},
		},
		Selector:              ls,
		InternalTrafficPolicy: cr.Spec.Sharding.Mongos.Expose.InternalTrafficPolicy,
		ExternalTrafficPolicy: cr.Spec.Sharding.Mongos.Expose.ExternalTrafficPolicy,
	}

	switch cr.Spec.Sharding.Mongos.Expose.ExposeType {
	case corev1.ServiceTypeNodePort:
		spec.Type = corev1.ServiceTypeNodePort
		spec.ExternalTrafficPolicy = "Local"
		if !cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
			for i, port := range spec.Ports {
				port.NodePort = cr.Spec.Sharding.Mongos.Expose.NodePort
				spec.Ports[i] = port
			}
		}
	case corev1.ServiceTypeLoadBalancer:
		spec.Type = corev1.ServiceTypeLoadBalancer
		spec.ExternalTrafficPolicy = "Cluster"
		spec.LoadBalancerSourceRanges = cr.Spec.Sharding.Mongos.Expose.LoadBalancerSourceRanges
		spec.LoadBalancerIP = cr.Spec.Sharding.Mongos.Expose.LoadBalancerIP
	default:
		spec.Type = corev1.ServiceTypeClusterIP
	}

	return spec
}
