package psmdb

import (
	"fmt"
	"strconv"
	"strings"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func MongosDeployment(cr *api.PerconaServerMongoDB) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + "mongos",
			Namespace: cr.Namespace,
		},
	}
}

func MongosDeploymentSpec(cr *api.PerconaServerMongoDB, operatorPod corev1.Pod) (appsv1.DeploymentSpec, error) {
	ls := map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   cr.Name,
		"app.kubernetes.io/component":  "mongos",
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
	}

	c, err := mongosContainer(cr)
	if err != nil {
		return appsv1.DeploymentSpec{}, fmt.Errorf("failed to create container %v", err)
	}

	initContainers := initContainers(cr, operatorPod)
	for i := range initContainers {
		initContainers[i].Resources.Limits = c.Resources.Limits
		initContainers[i].Resources.Requests = c.Resources.Requests
	}

	return appsv1.DeploymentSpec{
		Replicas: &cr.Spec.Sharding.Mongos.Size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      ls,
				Annotations: cr.Spec.Sharding.Mongos.MultiAZ.Annotations,
			},
			Spec: corev1.PodSpec{
				SecurityContext:   cr.Spec.Sharding.Mongos.PodSecurityContext,
				Affinity:          PodAffinity(cr.Spec.Sharding.Mongos.MultiAZ.Affinity, ls),
				NodeSelector:      cr.Spec.Sharding.Mongos.MultiAZ.NodeSelector,
				Tolerations:       cr.Spec.Sharding.Mongos.MultiAZ.Tolerations,
				PriorityClassName: cr.Spec.Sharding.Mongos.MultiAZ.PriorityClassName,
				RestartPolicy:     corev1.RestartPolicyAlways,
				ImagePullSecrets:  cr.Spec.ImagePullSecrets,
				Containers:        []corev1.Container{c},
				InitContainers:    initContainers,
				Volumes:           volumes(cr),
				SchedulerName:     cr.Spec.SchedulerName,
			},
		},
	}, nil
}

func initContainers(cr *api.PerconaServerMongoDB, operatorPod corev1.Pod) []corev1.Container {
	inits := []corev1.Container{}
	if cr.CompareVersion("1.5.0") >= 0 {
		inits = append(inits, EntrypointInitContainer(operatorPod.Spec.Containers[0].Image))
	}

	return inits
}

func mongosContainer(cr *api.PerconaServerMongoDB) (corev1.Container, error) {
	fvar := false

	resources, err := CreateResources(cr.Spec.Sharding.Mongos.ResourcesSpec)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("resource creation: %v", err)
	}

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
			MountPath: sslDir,
			ReadOnly:  true,
		},
		{
			Name:      "ssl-internal",
			MountPath: sslInternalDir,
			ReadOnly:  true,
		},
	}

	container := corev1.Container{
		Name:            "mongos",
		Image:           cr.Spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Args:            mongosContainerArgs(cr, resources),
		Ports: []corev1.ContainerPort{
			{
				Name:          mongosPortName,
				HostPort:      cr.Spec.Sharding.Mongos.HostPort,
				ContainerPort: cr.Spec.Sharding.Mongos.Port,
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
						Name: "internal-" + cr.Name + "-users",
					},
					Optional: &fvar,
				},
			},
		},
		WorkingDir:      MongodContainerDataDir,
		LivenessProbe:   &cr.Spec.Sharding.Mongos.LivenessProbe.Probe,
		ReadinessProbe:  cr.Spec.Sharding.Mongos.ReadinessProbe,
		SecurityContext: cr.Spec.Sharding.Mongos.ContainerSecurityContext,
		Resources:       resources,
		VolumeMounts:    volumes,
		Command:         []string{"/data/db/ps-entry.sh"},
	}

	return container, nil
}

func mongosContainerArgs(cr *api.PerconaServerMongoDB, resources corev1.ResourceRequirements) []string {
	mdSpec := cr.Spec.Mongod
	msSpec := cr.Spec.Sharding.Mongos
	cfgRs := cr.Spec.Sharding.ConfigsvrReplSet

	cfgInstanses := make([]string, 0, cfgRs.Size)
	for i := 0; i < int(cfgRs.Size); i++ {
		cfgInstanses = append(cfgInstanses,
			fmt.Sprintf("%s-%s-%d.%s-%s.%s.svc.cluster.local:%d",
				cr.Name, cfgRs.Name, i, cr.Name, cfgRs.Name, cr.Namespace, msSpec.Port))
	}

	configDB := fmt.Sprintf("%s/%s", cfgRs.Name, strings.Join(cfgInstanses, ","))
	args := []string{
		"mongos",
		"--bind_ip_all",
		"--port=" + strconv.Itoa(int(msSpec.Port)),
		"--sslAllowInvalidCertificates",
		"--configdb",
		configDB,
	}

	if cr.Spec.UnsafeConf {
		args = append(args,
			"--clusterAuthMode=keyFile",
			"--keyFile="+mongodSecretsDir+"/mongodb-key",
		)
	} else {
		args = append(args,
			"--sslMode=preferSSL",
			"--clusterAuthMode=x509",
		)
	}

	if mdSpec.Security != nil && mdSpec.Security.RedactClientLogData {
		args = append(args, "--redactClientLogData")
	}

	if msSpec.SetParameter != nil {
		if msSpec.SetParameter.CursorTimeoutMillis > 0 {
			args = append(args,
				"--setParameter",
				"cursorTimeoutMillis="+strconv.Itoa(msSpec.SetParameter.CursorTimeoutMillis),
			)
		}
	}

	if msSpec.AuditLog != nil && msSpec.AuditLog.Destination == api.AuditLogDestinationFile {
		if msSpec.AuditLog.Filter == "" {
			msSpec.AuditLog.Filter = "{}"
		}
		args = append(args,
			"--auditDestination=file",
			"--auditFilter="+msSpec.AuditLog.Filter,
			"--auditFormat="+string(msSpec.AuditLog.Format),
		)
		switch msSpec.AuditLog.Format {
		case api.AuditLogFormatBSON:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.bson")
		default:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.json")
		}
	}

	return args
}

func volumes(cr *api.PerconaServerMongoDB) []corev1.Volume {
	fvar := false
	t := true
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
					SecretName:  cr.Spec.Secrets.SSL,
					Optional:    &cr.Spec.UnsafeConf,
					DefaultMode: &secretFileMode,
				},
			},
		},
		{
			Name: "ssl-internal",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  cr.Spec.Secrets.SSLInternal,
					Optional:    &t,
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

	return volumes
}

func MongosService(cr *api.PerconaServerMongoDB) corev1.Service {
	svc := corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + "mongos",
			Namespace: cr.Namespace,
		},
	}

	if cr.Spec.Sharding.Mongos != nil {
		svc.Annotations = cr.Spec.Sharding.Mongos.Expose.ServiceAnnotations
	}

	return svc
}

func MongosServiceSpec(cr *api.PerconaServerMongoDB) corev1.ServiceSpec {
	ls := map[string]string{
		"app.kubernetes.io/name":       "percona-server-mongodb",
		"app.kubernetes.io/instance":   cr.Name,
		"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
		"app.kubernetes.io/part-of":    "percona-server-mongodb",
		"app.kubernetes.io/component":  "mongos",
	}

	spec := corev1.ServiceSpec{
		Ports: []corev1.ServicePort{
			{
				Name:       mongosPortName,
				Port:       cr.Spec.Sharding.Mongos.Port,
				TargetPort: intstr.FromInt(int(cr.Spec.Sharding.Mongos.Port)),
			},
		},
		Selector:                 ls,
		LoadBalancerSourceRanges: cr.Spec.Sharding.Mongos.Expose.LoadBalancerSourceRanges,
		ClusterIP:                "None",
	}

	switch cr.Spec.Sharding.Mongos.Expose.ExposeType {
	case corev1.ServiceTypeNodePort:
		spec.Type = corev1.ServiceTypeNodePort
		spec.ExternalTrafficPolicy = "Local"
	case corev1.ServiceTypeLoadBalancer:
		spec.Type = corev1.ServiceTypeLoadBalancer
		spec.ExternalTrafficPolicy = "Cluster"
	default:
		spec.Type = corev1.ServiceTypeClusterIP
	}

	return spec
}
