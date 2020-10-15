package psmdb

import (
	"fmt"
	"strconv"
	"strings"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/pkg/errors"
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

func DeploymentSpec(cr *api.PerconaServerMongoDB, ikeyName string,
	ls map[string]string, initContainers []corev1.Container) (appsv1.DeploymentSpec, error) {
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

	if *cr.Spec.Mongod.Security.EnableEncryption {
		volumes = append(volumes,
			corev1.Volume{
				Name: cr.Spec.Mongod.Security.EncryptionKeySecret,
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						DefaultMode: &secretFileMode,
						SecretName:  cr.Spec.Mongod.Security.EncryptionKeySecret,
						Optional:    &fvar,
					},
				},
			},
		)
	}

	c, err := mongosContainer(cr, ikeyName)
	if err != nil {
		return appsv1.DeploymentSpec{}, fmt.Errorf("failed to create container %v", err)
	}

	for i := range initContainers {
		initContainers[i].Resources.Limits = c.Resources.Limits
		initContainers[i].Resources.Requests = c.Resources.Requests
	}

	return appsv1.DeploymentSpec{
		Replicas: &cr.Spec.Mongos.Size,
		Selector: &metav1.LabelSelector{
			MatchLabels: ls,
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels:      ls,
				Annotations: cr.Spec.Mongos.MultiAZ.Annotations,
			},
			Spec: corev1.PodSpec{
				SecurityContext:   cr.Spec.Mongos.PodSecurityContext,
				Affinity:          PodAffinity(cr.Spec.Mongos.MultiAZ.Affinity, ls),
				NodeSelector:      cr.Spec.Mongos.MultiAZ.NodeSelector,
				Tolerations:       cr.Spec.Mongos.MultiAZ.Tolerations,
				PriorityClassName: cr.Spec.Mongos.MultiAZ.PriorityClassName,
				RestartPolicy:     corev1.RestartPolicyAlways,
				ImagePullSecrets:  cr.Spec.ImagePullSecrets,
				Containers:        []corev1.Container{c},
				InitContainers:    initContainers,
				Volumes:           volumes,
				SchedulerName:     cr.Spec.SchedulerName,
			},
		},
	}, nil
}

func mongosContainer(cr *api.PerconaServerMongoDB, ikeyName string) (corev1.Container, error) {
	fvar := false

	resources, err := CreateResources(cr.Spec.Mongos.ResourcesSpec)
	if err != nil {
		return corev1.Container{}, fmt.Errorf("resource creation: %v", err)
	}

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

	if *cr.Spec.Mongod.Security.EnableEncryption {
		volumes = append(volumes,
			corev1.VolumeMount{
				Name:      cr.Spec.Mongod.Security.EncryptionKeySecret,
				MountPath: mongodRESTencryptDir,
				ReadOnly:  true,
			},
		)
	}

	mongosArgs, err := mongosContainerArgs(cr, resources)
	if err != nil {
		return corev1.Container{}, err
	}
	container := corev1.Container{
		Name:            "mongos",
		Image:           cr.Spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Args:            mongosArgs,
		Ports: []corev1.ContainerPort{
			{
				Name:          mongosPortName,
				HostPort:      cr.Spec.Mongos.HostPort,
				ContainerPort: cr.Spec.Mongos.Port,
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "SERVICE_NAME",
				Value: cr.Name,
			},
			{
				Name:  "NAMESPACE",
				Value: cr.Namespace,
			},
			{
				Name:  "MONGODB_PORT",
				Value: strconv.Itoa(int(cr.Spec.Mongos.Port)),
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
		},
		WorkingDir:      MongodContainerDataDir,
		LivenessProbe:   &cr.Spec.Mongos.LivenessProbe.Probe,
		ReadinessProbe:  cr.Spec.Mongos.ReadinessProbe,
		SecurityContext: cr.Spec.Mongos.ContainerSecurityContext,
		Resources:       resources,
		VolumeMounts:    volumes,
	}

	if cr.CompareVersion("1.5.0") >= 0 {
		container.EnvFrom = []corev1.EnvFromSource{
			{
				SecretRef: &corev1.SecretEnvSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "internal-" + cr.Name + "-users",
					},
					Optional: &fvar,
				},
			},
		}
		container.Command = []string{"/data/db/ps-entry.sh"}
	}

	return container, nil
}

func findCfgReplset(replsets []*api.ReplsetSpec) (*api.ReplsetSpec, error) {
	for _, rs := range replsets {
		if rs.ClusterRole == "configsvr" {
			return rs, nil
		}
	}

	return nil, errors.New("failed to find config server replset configuration")
}

func mongosContainerArgs(m *api.PerconaServerMongoDB, resources corev1.ResourceRequirements) ([]string, error) {
	mdSpec := m.Spec.Mongod
	msSpec := m.Spec.Mongos
	cfgRs, err := findCfgReplset(m.Spec.Replsets)
	if err != nil {
		return nil, err
	}

	cfgInstanses := make([]string, 0, cfgRs.Size)
	for i := 0; i < int(cfgRs.Size); i++ {
		cfgInstanses = append(cfgInstanses,
			fmt.Sprintf("%s-%s-%d.%s-%s.%s.svc.cluster.local:%d",
				m.Name, cfgRs.Name, i, m.Name, cfgRs.Name, m.Namespace, msSpec.Port))
	}

	configDB := fmt.Sprintf("cfg0/%s", strings.Join(cfgInstanses, ","))
	args := []string{
		"mongos",
		"--bind_ip_all",
		"--port=" + strconv.Itoa(int(msSpec.Port)),
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

	return args, nil
}
