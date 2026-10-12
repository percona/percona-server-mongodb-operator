package pmm

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

const (
	scramSHA1AuthMechanism   = "SCRAM-SHA-1"
	scramSHA256AuthMechanism = "SCRAM-SHA-256"
)

func pmmAgentScript(cr *api.PerconaServerMongoDB) []corev1.EnvVar {
	// handle disabled TLS

	pmmServerArgs := "$(PMM_ADMIN_CUSTOM_PARAMS) --skip-connection-check --metrics-mode=push "
	pmmServerArgs += " --username=$(DB_USER) --password=$(DB_PASSWORD) --cluster=$(CLUSTER_NAME) "
	pmmServerArgs += "--service-name=$(PMM_AGENT_SETUP_NODE_NAME) --host=$(DB_HOST) --port=$(DB_PORT)"

	if cr.Spec.PMM.QuerySource != "" {
		pmmServerArgs += " --query-source=" + cr.Spec.PMM.QuerySource
	}

	if cr.TLSEnabled() {
		authMechanism := scramSHA256AuthMechanism
		switch {
		case cr.CompareVersion("1.23.0") < 0:
			authMechanism = scramSHA1AuthMechanism
		case cr.Spec.PMM.AuthenticationMechanism != "":
			authMechanism = cr.Spec.PMM.AuthenticationMechanism
		}
		tlsParams := []string{
			"--tls",
			"--tls-skip-verify",
			"--tls-certificate-key-file=/tmp/tls.pem",
			fmt.Sprintf("--tls-ca-file=%s/ca.crt", config.SSLDir),
			fmt.Sprintf("--authentication-mechanism=%s", authMechanism),
			"--authentication-database=admin",
		}
		pmmServerArgs += " " + strings.Join(tlsParams, " ")
	}

	pmmWait := "pmm-admin status --wait=10s;"
	pmmAddService := fmt.Sprintf("pmm-admin add $(DB_TYPE) %s;", pmmServerArgs)
	pmmAnnotate := "pmm-admin annotate --service-name=$(PMM_AGENT_SETUP_NODE_NAME) 'Service restarted'"
	prerunScript := pmmWait + "\n" + pmmAddService + "\n" + pmmAnnotate

	if cr.TLSEnabled() {
		prepareTLS := fmt.Sprintf("cat %[1]s/tls.key %[1]s/tls.crt > /tmp/tls.pem;", config.SSLDir)
		prerunScript = prepareTLS + "\n" + prerunScript
	}

	return []corev1.EnvVar{
		{
			Name:  "PMM_AGENT_PRERUN_SCRIPT",
			Value: prerunScript,
		},
	}
}

// containerForPMM3 builds a container that is supporting PMM3.
func containerForPMM3(cr *api.PerconaServerMongoDB, secret *corev1.Secret, dbPort int32, customAdminParams string) *corev1.Container {
	spec := cr.Spec.PMM
	ports := []corev1.ContainerPort{{ContainerPort: 7777}}

	for i := 30100; i <= 30105; i++ {
		ports = append(ports, corev1.ContainerPort{ContainerPort: int32(i)})
	}

	clusterName := cr.Name
	if len(cr.Spec.PMM.CustomClusterName) > 0 {
		clusterName = cr.Spec.PMM.CustomClusterName

	}

	pmm := corev1.Container{
		Name:            "pmm-client",
		Image:           spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Resources:       cr.Spec.PMM.Resources,
		LivenessProbe: &corev1.Probe{
			InitialDelaySeconds: 60,
			TimeoutSeconds:      5,
			PeriodSeconds:       10,
			HTTPGet: &corev1.HTTPGetAction{
				Port: intstr.FromInt32(7777),
				Path: "/local/Status",
			},
		},
		Env: []corev1.EnvVar{
			{
				Name:  "DB_TYPE",
				Value: "mongodb",
			},
			{
				Name: "DB_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key:  "MONGODB_CLUSTER_MONITOR_USER",
						Name: secret.Name,
					},
				},
			},
			{
				Name: "DB_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key:  "MONGODB_CLUSTER_MONITOR_PASSWORD",
						Name: secret.Name,
					},
				},
			},
			{
				Name:  "DB_HOST",
				Value: "localhost",
			},
			{
				Name:  "DB_CLUSTER",
				Value: cr.Name,
			},
			{
				Name:  "DB_PORT",
				Value: strconv.Itoa(int(dbPort)),
			},
			{
				Name:  "CLUSTER_NAME",
				Value: clusterName,
			},
			{
				Name: "POD_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
			{
				Name: "POD_NAMESPACE",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.namespace",
					},
				},
			},
			{
				Name:  "PMM_AGENT_SERVER_ADDRESS",
				Value: spec.ServerHost,
			},
			{
				Name:  "PMM_AGENT_SERVER_USERNAME",
				Value: "service_token",
			}, {
				Name: "PMM_AGENT_SERVER_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key:  api.PMMServerToken,
						Name: secret.Name,
					},
				},
			},
			{
				Name:  "PMM_AGENT_LISTEN_PORT",
				Value: "7777",
			},
			{
				Name:  "PMM_AGENT_PORTS_MIN",
				Value: "30100",
			},
			{
				Name:  "PMM_AGENT_PORTS_MAX",
				Value: "30105",
			},
			{
				Name:  "PMM_AGENT_CONFIG_FILE",
				Value: "/usr/local/percona/pmm/config/pmm-agent.yaml",
			},
			{
				Name:  "PMM_AGENT_SERVER_INSECURE_TLS",
				Value: "1",
			},
			{
				Name:  "PMM_AGENT_LISTEN_ADDRESS",
				Value: "0.0.0.0",
			},
			{
				Name:  "PMM_AGENT_SETUP_NODE_NAME",
				Value: "$(POD_NAMESPACE)-$(POD_NAME)",
			},
			{
				Name:  "PMM_AGENT_SETUP",
				Value: "1",
			},
			{
				Name:  "PMM_AGENT_SETUP_FORCE",
				Value: "1",
			},
			{
				Name:  "PMM_AGENT_SETUP_NODE_TYPE",
				Value: "container",
			},
			{
				Name:  "PMM_AGENT_SETUP_METRICS_MODE",
				Value: "push",
			},
			{
				Name:  "PMM_ADMIN_CUSTOM_PARAMS",
				Value: customAdminParams,
			},
			{
				Name:  "PMM_AGENT_SIDECAR",
				Value: "true",
			},
			{
				Name:  "PMM_AGENT_SIDECAR_SLEEP",
				Value: "5",
			},
			{
				Name:  "PMM_AGENT_PATHS_TEMPDIR",
				Value: "/tmp/pmm",
			},
		},
		Ports:           ports,
		SecurityContext: spec.ContainerSecurityContext,
		Lifecycle: &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"bash",
						"-c",
						"pmm-admin unregister --force",
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "ssl",
				MountPath: config.SSLDir,
				ReadOnly:  true,
			},
		},
	}

	if cr.CompareVersion("1.22.0") >= 0 {
		pmm.VolumeMounts = append(pmm.VolumeMounts, corev1.VolumeMount{
			Name:      config.MongodDataVolClaimName,
			MountPath: config.MongodContainerDataDir,
			ReadOnly:  true,
		})
	}

	pmmAgentScriptEnv := pmmAgentScript(cr)
	pmm.Env = append(pmm.Env, pmmAgentScriptEnv...)

	return &pmm
}

// Container creates the container object for a pmm-client
func Container(ctx context.Context, cr *api.PerconaServerMongoDB, secret *corev1.Secret, dbPort int32, customAdminParams string) *corev1.Container {
	log := logf.FromContext(ctx)

	if !cr.Spec.PMM.Enabled {
		return nil
	}
	if secret == nil {
		log.Info("pmm is enabled but the secret is nil, cannot create pmm container")
		return nil
	}

	if !SecretHasToken(secret) {
		log.Info(fmt.Sprintf("Secret is missing the required PMM credentials: PMM is enabled and requires the configuration of %s", api.PMMServerToken))
		return nil
	}

	c := containerForPMM3(cr, secret, dbPort, customAdminParams)
	applyCustomProbes(cr, c)
	return c
}

// applyCustomProbes overrides the liveness and readiness probes of the
// pmm-client container with the ones defined in the CR, if any. When the
// corresponding field is not set the Operator keeps its default behavior:
// the built-in liveness probe and no readiness probe.
func applyCustomProbes(cr *api.PerconaServerMongoDB, container *corev1.Container) {
	if container == nil || cr.CompareVersion("1.23.0") < 0 {
		return
	}

	if cr.Spec.PMM.LivenessProbe != nil {
		container.LivenessProbe = cr.Spec.PMM.LivenessProbe
	}

	if cr.Spec.PMM.ReadinessProbe != nil {
		container.ReadinessProbe = cr.Spec.PMM.ReadinessProbe
	}
}

// SecretHasToken checks if the PMM3 token is configured as part of the given secret.
func SecretHasToken(secret *corev1.Secret) bool {
	if len(secret.Data) == 0 {
		return false
	}
	if v, exists := secret.Data[api.PMMServerToken]; exists && len(v) != 0 {
		return true
	}
	return false
}
