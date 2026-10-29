package clustersync

import (
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// Container builds the PCSM container.
func Container(cr *api.PerconaServerMongoDBClusterSync, cluster *api.PerconaServerMongoDB) corev1.Container {
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
		Env:             pcsmEnv(cr, cluster),
		Resources:       cr.Spec.Resources,
		SecurityContext: cr.Spec.ContainerSecurityContext,
		LivenessProbe:   probeOrDefault(cluster, cr.Spec.LivenessProbe, livenessProbe),
		ReadinessProbe:  probeOrDefault(cluster, cr.Spec.ReadinessProbe, readinessProbe),
	}
}

// ReservedEnv holds the operator-managed vars. A spec.env duplicate would
// win over ours, so entries using these names are dropped.
var ReservedEnv = map[string]struct{}{
	"PCSM_SOURCE_URI": {},
	"PCSM_TARGET_URI": {},
	"PCSM_GROUP_NAME": {},
}

func pcsmEnv(cr *api.PerconaServerMongoDBClusterSync, cluster *api.PerconaServerMongoDB) []corev1.EnvVar {
	secretName := URISecretName(cr)
	env := []corev1.EnvVar{
		{Name: "PCSM_SOURCE_URI", ValueFrom: uriEnvSource(secretName, URISecretSourceKey)},
		{Name: "PCSM_TARGET_URI", ValueFrom: uriEnvSource(secretName, URISecretTargetKey)},
	}
	if groupNameEnabled(cr, cluster) {
		env = append(env, corev1.EnvVar{Name: "PCSM_GROUP_NAME", Value: GroupName(cr)})
	}
	for _, e := range cr.Spec.Env {
		if _, ok := ReservedEnv[e.Name]; ok {
			continue
		}
		env = append(env, e)
	}
	return env
}

// groupNameEnabled withholds the group name from pre-1.24 single instances:
// adding it on an operator upgrade would restart the only replicating one.
func groupNameEnabled(cr *api.PerconaServerMongoDBClusterSync, cluster *api.PerconaServerMongoDB) bool {
	if Size(cr) > 1 {
		return true
	}
	return cluster != nil && cluster.CompareVersion("1.24.0") >= 0
}

// GroupName scopes PCSM's leader election to this CR. PCSM defaults to
// "default", which two ClusterSync CRs sharing a target would collide on.
func GroupName(cr *api.PerconaServerMongoDBClusterSync) string {
	return cr.Namespace + "-" + cr.Name
}

func uriEnvSource(secretName, key string) *corev1.EnvVarSource {
	return &corev1.EnvVarSource{
		SecretKeyRef: &corev1.SecretKeySelector{
			Name: secretName,
			Key:  key,
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

func probeOrDefault(cluster *api.PerconaServerMongoDB, override *corev1.Probe, def func(path string) *corev1.Probe) *corev1.Probe {
	if override != nil {
		return override.DeepCopy()
	}
	return def(probePath(cluster))
}

// probePath keeps pre-1.24 deployments on the /status probes they were
// created with; HA moved to /metrics because a standby refuses /status.
func probePath(cluster *api.PerconaServerMongoDB) string {
	if cluster == nil || cluster.CompareVersion("1.24.0") < 0 {
		return StatusPath
	}
	return MetricsPath
}

// loopbackProbe builds an exec probe that curls path on the container's
// own loopback address.
func loopbackProbe(path string, timeoutSeconds int32) corev1.ProbeHandler {
	maxTime := timeoutSeconds
	if maxTime > 1 {
		maxTime--
	}
	connectTimeout := min(int32(1), maxTime)
	return corev1.ProbeHandler{
		Exec: &corev1.ExecAction{
			Command: []string{
				"curl", "-fsS",
				"--connect-timeout", strconv.Itoa(int(connectTimeout)),
				"--max-time", strconv.Itoa(int(maxTime)),
				fmt.Sprintf("http://127.0.0.1:%d%s", HTTPPort, path),
			},
		},
	}
}

func livenessProbe(path string) *corev1.Probe {
	const timeoutSeconds int32 = 5
	return &corev1.Probe{
		ProbeHandler:        loopbackProbe(path, timeoutSeconds),
		InitialDelaySeconds: 30,
		PeriodSeconds:       10,
		TimeoutSeconds:      timeoutSeconds,
		FailureThreshold:    5,
	}
}

func readinessProbe(path string) *corev1.Probe {
	const timeoutSeconds int32 = 5
	return &corev1.Probe{
		ProbeHandler:        loopbackProbe(path, timeoutSeconds),
		InitialDelaySeconds: 5,
		PeriodSeconds:       10,
		TimeoutSeconds:      timeoutSeconds,
		FailureThreshold:    3,
	}
}
