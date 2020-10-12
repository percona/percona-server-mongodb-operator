package psmdb

import (
	"fmt"
	"strconv"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func NewDeployment(name, namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "apps/v1",
			Kind:       "Deployment",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func DeploymentSpec(m *api.PerconaServerMongoDB, size int32, ikeyName string,
	containerName string, ls map[string]string,
	initContainers []corev1.Container) (appsv1.DeploymentSpec, error) {
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

	resources, err := CreateResources(nil)
	if err != nil {
		return appsv1.DeploymentSpec{}, fmt.Errorf("resource creation: %v", err)
	}

	c, err := mongosContainer(m, containerName, resources, ikeyName)
	if err != nil {
		return appsv1.DeploymentSpec{}, fmt.Errorf("failed to create container %v", err)
	}

	for i := range initContainers {
		initContainers[i].Resources.Limits = c.Resources.Limits
		initContainers[i].Resources.Requests = c.Resources.Requests
	}

	return appsv1.DeploymentSpec{
		// ServiceName: m.Name + "-" + replset.Name,
		Replicas: &size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: ls,
				// Annotations: multiAZ.Annotations,
			},
			Spec: corev1.PodSpec{
				// SecurityContext:   replset.PodSecurityContext,
				// Affinity: PodAffinity(multiAZ.Affinity, ls),
				// NodeSelector:      multiAZ.NodeSelector,
				// Tolerations:       multiAZ.Tolerations,
				// PriorityClassName: multiAZ.PriorityClassName,
				RestartPolicy:    corev1.RestartPolicyAlways,
				ImagePullSecrets: m.Spec.ImagePullSecrets,
				Containers:       []corev1.Container{c},
				InitContainers:   initContainers,
				Volumes:          volumes,
				SchedulerName:    m.Spec.SchedulerName,
			},
		},
	}, nil
}

func mongosContainer(m *api.PerconaServerMongoDB, name string,
	resources corev1.ResourceRequirements, ikeyName string) (corev1.Container, error) {
	fvar := false

	volumes := []corev1.VolumeMount{
		{
			Name:      MongodDataVolClaimName,
			MountPath: MongodContainerDataDir,
		},
		{
			Name:      ikeyName,
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

	if *m.Spec.Mongod.Security.EnableEncryption {
		volumes = append(volumes,
			corev1.VolumeMount{
				Name:      m.Spec.Mongod.Security.EncryptionKeySecret,
				MountPath: mongodRESTencryptDir,
				ReadOnly:  true,
			},
		)
	}

	container := corev1.Container{
		Name:            name,
		Image:           m.Spec.Image,
		ImagePullPolicy: m.Spec.ImagePullPolicy,
		Args:            mongosContainerArgs(m, resources),
		Ports: []corev1.ContainerPort{
			{
				Name:          mongodPortName,
				HostPort:      m.Spec.Mongod.Net.HostPort,
				ContainerPort: m.Spec.Mongod.Net.Port,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "SERVICE_NAME",
				Value: m.Name,
			},
			{
				Name:  "NAMESPACE",
				Value: m.Namespace,
			},
			{
				Name:  "MONGODB_PORT",
				Value: strconv.Itoa(int(m.Spec.Mongod.Net.Port)),
			},
			// {
			// 	Name:  "MONGODB_REPLSET",
			// 	Value: replset.Name,
			// },
		},
		EnvFrom: []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: m.Spec.Secrets.Users,
					},
					Optional: &fvar,
				},
			},
		},
		WorkingDir: MongodContainerDataDir,
		// LivenessProbe:   &replset.LivenessProbe.Probe,
		// ReadinessProbe:  replset.ReadinessProbe,
		// Resources:       resources,
		// SecurityContext: replset.ContainerSecurityContext,
		VolumeMounts: volumes,
	}

	if m.CompareVersion("1.5.0") >= 0 {
		container.EnvFrom = []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "internal-" + m.Name + "-users",
					},
					Optional: &fvar,
				},
			},
		}
		container.Command = []string{"/data/db/ps-entry.sh"}
	}

	return container, nil
}

func mongosContainerArgs(m *api.PerconaServerMongoDB, resources corev1.ResourceRequirements) []string {
	mSpec := m.Spec.Mongod
	configDB := fmt.Sprintf("cfg0/my-cluster-name-cfg0-0.my-cluster-name-cfg0.%s.svc.cluster.local:27017,my-cluster-name-cfg0-1.my-cluster-name-cfg0.%s.svc.cluster.local:27017,my-cluster-name-cfg0-2.my-cluster-name-cfg0.%s.svc.cluster.local:27017", m.Namespace, m.Namespace, m.Namespace)
	args := []string{
		"mongos",
		"--bind_ip_all",
		// "--port=" + strconv.Itoa(int(mSpec.Net.Port)),
		"--sslAllowInvalidCertificates",
		"--configdb",
		configDB,
	}

	if m.Spec.UnsafeConf {
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

	// security
	if mSpec.Security != nil && mSpec.Security.RedactClientLogData {
		args = append(args, "--redactClientLogData")
	}

	// setParameter
	if mSpec.SetParameter != nil {
		if mSpec.SetParameter.CursorTimeoutMillis > 0 {
			args = append(args,
				"--setParameter",
				"cursorTimeoutMillis="+strconv.Itoa(mSpec.SetParameter.CursorTimeoutMillis),
			)
		}
	}

	// auditLog
	if mSpec.AuditLog != nil && mSpec.AuditLog.Destination == api.AuditLogDestinationFile {
		if mSpec.AuditLog.Filter == "" {
			mSpec.AuditLog.Filter = "{}"
		}
		args = append(args,
			"--auditDestination=file",
			"--auditFilter="+mSpec.AuditLog.Filter,
			"--auditFormat="+string(mSpec.AuditLog.Format),
		)
		switch mSpec.AuditLog.Format {
		case api.AuditLogFormatBSON:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.bson")
		default:
			args = append(args, "--auditPath="+MongodContainerDataDir+"/auditLog.json")
		}
	}

	return args
}
