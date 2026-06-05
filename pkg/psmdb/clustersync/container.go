package clustersync

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// Container builds the PCSM container.
func Container(cr *api.PerconaServerMongoDBClusterSync) corev1.Container {
	return corev1.Container{
		Name:            ContainerName,
		Image:           cr.Spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Args:            pcsmArgs(cr),
		Ports: []corev1.ContainerPort{{
			Name:          HTTPPortName,
			ContainerPort: HTTPPort,
			Protocol:      corev1.ProtocolTCP,
		}},
		Env:             pcsmEnv(cr),
		Resources:       cr.Spec.Resources,
		SecurityContext: cr.Spec.ContainerSecurityContext,
		LivenessProbe:   probeOrDefault(cr.Spec.LivenessProbe, livenessProbe),
		ReadinessProbe:  probeOrDefault(cr.Spec.ReadinessProbe, readinessProbe),
	}
}

func pcsmEnv(cr *api.PerconaServerMongoDBClusterSync) []corev1.EnvVar {
	secretName := URISecretName(cr)
	env := []corev1.EnvVar{
		{Name: "PCSM_SOURCE_URI", ValueFrom: uriEnvSource(secretName, URISecretSourceKey)},
		{Name: "PCSM_TARGET_URI", ValueFrom: uriEnvSource(secretName, URISecretTargetKey)},
	}
	env = append(env, cr.Spec.Env...)
	return env
}

func uriEnvSource(secretName, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
			Key:                  key,
		},
	}
}

func pcsmArgs(cr *api.PerconaServerMongoDBClusterSync) []string {
	var args []string
	if lvl := cr.Spec.PCSMConfig.LogLevel; lvl != "" {
		args = append(args, "--log-level="+lvl)
	}
	if j := cr.Spec.PCSMConfig.LogJSON; j != nil && *j {
		args = append(args, "--log-json")
	}
	return args
}

func probeOrDefault(override *corev1.Probe, def func() *corev1.Probe) *corev1.Probe {
	if override != nil {
		return override.DeepCopy()
	}
	return def()
}

// PCSM 0.9.0 binds its HTTP server to 127.0.0.1 only, so TCP/HTTPGet probes
// against the pod IP get connection-refused. Until PCSM-345 lands upstream
// (https://perconadev.atlassian.net/browse/PCSM-345) we probe via curl on
// loopback from inside the container. Revert to TCPSocket/HTTPGet once
// PCSM binds to 0.0.0.0.
func loopbackStatusProbe() corev1.ProbeHandler {
	return corev1.ProbeHandler{
		Exec: &corev1.ExecAction{
			Command: []string{"curl", "-fsS", "--max-time", "3", fmt.Sprintf("http://127.0.0.1:%d/status", HTTPPort)},
		},
	}
}

func livenessProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:        loopbackStatusProbe(),
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		FailureThreshold:    5,
	}
}

func readinessProbe() *corev1.Probe {
	return &corev1.Probe{
		ProbeHandler:        loopbackStatusProbe(),
		InitialDelaySeconds: 5,
		PeriodSeconds:       10,
		TimeoutSeconds:      5,
		FailureThreshold:    3,
	}
}
