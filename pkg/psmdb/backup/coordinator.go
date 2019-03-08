package backup

import (
	"strconv"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/version"
)

func CoordinatorService(crName, namespace string) *corev1.Service {
	name := crName + coordinatorSuffix

	ls := map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   crName,
		"app.kubernetes.io/replset":    "general",
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/component":  "backup-coordinator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}

	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: corev1.ServiceSpec{
			Selector: ls,
			Ports: []corev1.ServicePort{
				{
					Name: coordinatorRPCPortName,
					Port: coordinatorRPCPort,
				},
				{
					Name: coordinatorAPIPortName,
					Port: coordinatorAPIPort,
				},
			},
		},
	}
}

func CoordinatorStatefulSet(cr *api.PerconaServerMongoDB) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "StatefulSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + coordinatorSuffix,
			Namespace: cr.Namespace,
		},
	}
}

func CoordinatorStatefulSetSpec(cr *api.PerconaServerMongoDB, spec *api.BackupCoordinatorSpec, sv *version.ServerVersion, debug bool) appsv1.StatefulSetSpec {
	var fsgroup *int64
	if sv.Platform == api.PlatformKubernetes {
		var tp int64 = 1001
		fsgroup = &tp
	}

	ls := map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   cr.Name,
		"app.kubernetes.io/replset":    "general",
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/component":  "backup-coordinator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}

	for k, v := range spec.Labels {
		if _, ok := ls[k]; !ok {
			ls[k] = v
		}
	}

	return appsv1.StatefulSetSpec{
		ServiceName: cr.Name + coordinatorSuffix,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      ls,
				Annotations: spec.Annotations,
			},
			Spec: newCoordinatorPodSpec(spec, cr.Spec.Backup.Image, cr.Spec.ImagePullSecrets, cr.Name+coordinatorContainerName, cr.Namespace, ls, fsgroup, debug),
		},
		VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
			coordinatorPersistentVolumeClaim(spec, coordinatorDataVolume, cr.Namespace),
		},
	}
}

func coordinatorPersistentVolumeClaim(spec *api.BackupCoordinatorSpec, name, namespace string) corev1.PersistentVolumeClaim {
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
					corev1.ResourceStorage: spec.Resources.Requests[corev1.ResourceStorage],
				},
			},
		},
	}
	if spec.StorageClass != "" {
		vc.Spec.StorageClassName = &spec.StorageClass
	}

	return vc
}

func newCoordinatorPodSpec(spec *api.BackupCoordinatorSpec, image string, imagePullSecrets []corev1.LocalObjectReference, name, namespace string, labels map[string]string, runUID *int64, debug bool) corev1.PodSpec {
	trueVar := true

	res := &corev1.ResourceRequirements{}
	// TODO: make resources handling idiomatic and consitent across operator
	if spec.Resources != nil {
		res.Limits = make(corev1.ResourceList)
		res.Limits[corev1.ResourceCPU] = spec.Resources.Limits[corev1.ResourceCPU]
		res.Limits[corev1.ResourceMemory] = spec.Resources.Limits[corev1.ResourceMemory]
		res.Requests = make(corev1.ResourceList)
		res.Requests[corev1.ResourceCPU] = spec.Resources.Requests[corev1.ResourceCPU]
		res.Requests[corev1.ResourceMemory] = spec.Resources.Requests[corev1.ResourceMemory]
	}

	return corev1.PodSpec{
		Affinity:          psmdb.PodAffinity(spec.Affinity, labels),
		NodeSelector:      spec.NodeSelector,
		Tolerations:       spec.Tolerations,
		PriorityClassName: spec.PriorityClassName,
		Containers: []corev1.Container{
			{
				Name:            name,
				Image:           image,
				ImagePullPolicy: corev1.PullAlways,
				Command:         []string{"pbm-coordinator"},
				Env: []corev1.EnvVar{
					{
						Name:  "PBM_COORDINATOR_ENABLE_CLIENTS_LOGGING",
						Value: strconv.FormatBool(spec.EnableClientsLogging),
					},
					{
						Name:  "PBM_COORDINATOR_DEBUG",
						Value: strconv.FormatBool(debug),
					},
					{
						Name:  "PBM_COORDINATOR_API_PORT",
						Value: strconv.Itoa(coordinatorAPIPort),
					},
					{
						Name:  "PBM_COORDINATOR_GRPC_PORT",
						Value: strconv.Itoa(coordinatorRPCPort),
					},
					{
						Name:  "PBM_COORDINATOR_WORK_DIR",
						Value: coordinatorDataMount,
					},
				},

				Resources: *res,
				SecurityContext: &corev1.SecurityContext{
					RunAsNonRoot: &trueVar,
					RunAsUser:    runUID,
				},
				VolumeMounts: []corev1.VolumeMount{
					{
						Name:      coordinatorDataVolume,
						MountPath: coordinatorDataMount,
					},
				},
				Ports: []corev1.ContainerPort{
					{
						Name:          coordinatorRPCPortName,
						ContainerPort: coordinatorRPCPort,
					},
					{
						Name:          coordinatorAPIPortName,
						ContainerPort: coordinatorAPIPort,
					},
				},
				LivenessProbe: &corev1.Probe{
					InitialDelaySeconds: int32(5),
					TimeoutSeconds:      int32(3),
					Handler: corev1.Handler{
						TCPSocket: &corev1.TCPSocketAction{
							Port: intstr.FromInt(coordinatorRPCPort),
						},
					},
				},
			},
		},
		ImagePullSecrets: imagePullSecrets,
		SecurityContext: &corev1.PodSecurityContext{
			FSGroup: runUID,
		},
	}
}
