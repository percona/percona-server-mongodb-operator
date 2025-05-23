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

// containerForPMM2 returns a pmm2 container from the given spec.
func containerForPMM2(cr *api.PerconaServerMongoDB, secret *corev1.Secret, dbPort int32, customAdminParams string) corev1.Container {
	_, oka := secret.Data[api.PMMAPIKey]
	_, okl := secret.Data[api.PMMUserKey]
	_, okp := secret.Data[api.PMMPasswordKey]
	customLogin := oka || (okl && okp)

	spec := cr.Spec.PMM
	ports := []corev1.ContainerPort{{ContainerPort: 7777}}

	for i := 30100; i <= 30105; i++ {
		ports = append(ports, corev1.ContainerPort{ContainerPort: int32(i)})
	}

	dbEnv := []corev1.EnvVar{
		{
			Name: "DB_USER",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_CLUSTER_MONITOR_USER",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
				},
			},
		},
		{
			Name: "DB_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_CLUSTER_MONITOR_PASSWORD",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
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
			Name:  "DB_PORT_MIN",
			Value: "30100",
		},
		{
			Name:  "DB_PORT_MAX",
			Value: "30105",
		},
	}
	pmm := corev1.Container{
		Name:            "pmm-client",
		Image:           spec.Image,
		ImagePullPolicy: cr.Spec.ImagePullPolicy,
		Resources:       cr.Spec.PMM.Resources,
		Env: []corev1.EnvVar{
			{
				Name:  "PMM_SERVER",
				Value: spec.ServerHost,
			},
			{
				Name:  "DB_TYPE",
				Value: "mongodb",
			},
		},
		Ports: ports,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "ssl",
				MountPath: config.SSLDir,
				ReadOnly:  true,
			},
		},
	}

	pmm.Env = append(pmm.Env, dbEnv...)

	if customLogin {
		pmmPassKey := api.PMMAPIKey
		if spec.ShouldUseAPIKeyAuth(secret) {
			pmm.Env = append(pmm.Env, corev1.EnvVar{
				Name:  "PMM_USER",
				Value: "api_key",
			})
		} else {
			pmmPassKey = api.PMMPasswordKey
			pmm.Env = append(pmm.Env, corev1.EnvVar{
				Name: "PMM_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: api.PMMUserKey,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secret.Name,
						},
					},
				},
			})
		}
		pmm.Env = append(pmm.Env, corev1.EnvVar{
			Name: "PMM_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: pmmPassKey,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
				},
			},
		})
	}

	if cr.CompareVersion("1.6.0") >= 0 {
		pmm.LivenessProbe = &corev1.Probe{
			InitialDelaySeconds: 60,
			TimeoutSeconds:      5,
			PeriodSeconds:       10,
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Port: intstr.FromInt(7777),
					Path: "/local/Status",
				},
			},
		}
		pmm.Env = append(pmm.Env, pmmAgentEnvs(spec, secret, customLogin, customAdminParams)...)
	}

	if cr.CompareVersion("1.21.0") >= 0 {
		pmm.VolumeMounts = append(pmm.VolumeMounts, corev1.VolumeMount{
			Name:      config.MongodDataVolClaimName,
			MountPath: config.MongodContainerDataDir,
			ReadOnly:  true,
		})
	}

	return pmm
}

func pmmAgentEnvs(spec api.PMMSpec, secret *corev1.Secret, customLogin bool, customAdminParams string) []corev1.EnvVar {
	pmmAgentEnvs := []corev1.EnvVar{
		{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
		{
			Name: "POD_NAMESPASE",
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
			Value: "/usr/local/percona/pmm2/config/pmm-agent.yaml",
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
			Value: "$(POD_NAMESPASE)-$(POD_NAME)",
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
	}

	if customLogin {
		pmmPassKey := api.PMMAPIKey
		if spec.ShouldUseAPIKeyAuth(secret) {
			pmmAgentEnvs = append(pmmAgentEnvs, corev1.EnvVar{
				Name:  "PMM_AGENT_SERVER_USERNAME",
				Value: "api_key",
			})
		} else {
			pmmPassKey = api.PMMPasswordKey
			pmmAgentEnvs = append(pmmAgentEnvs, corev1.EnvVar{
				Name: "PMM_AGENT_SERVER_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: api.PMMUserKey,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secret.Name,
						},
					},
				},
			})
		}
		pmmAgentEnvs = append(pmmAgentEnvs, corev1.EnvVar{
			Name: "PMM_AGENT_SERVER_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: pmmPassKey,
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
				},
			},
		})
	}

	return pmmAgentEnvs
}

func PMMAgentScript(cr *api.PerconaServerMongoDB) []corev1.EnvVar {
	// handle disabled TLS

	pmmServerArgs := "$(PMM_ADMIN_CUSTOM_PARAMS) --skip-connection-check --metrics-mode=push "
	pmmServerArgs += " --username=$(DB_USER) --password=$(DB_PASSWORD) --cluster=$(CLUSTER_NAME) "
	pmmServerArgs += "--service-name=$(PMM_AGENT_SETUP_NODE_NAME) --host=$(DB_HOST) --port=$(DB_PORT)"

	if cr.TLSEnabled() {
		tlsParams := []string{
			"--tls",
			"--tls-skip-verify",
			"--tls-certificate-key-file=/tmp/tls.pem",
			fmt.Sprintf("--tls-ca-file=%s/ca.crt", config.SSLDir),
			"--authentication-mechanism=SCRAM-SHA-1",
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
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Port: intstr.FromInt32(7777),
					Path: "/local/Status",
				},
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
						Key: "MONGODB_CLUSTER_MONITOR_USER",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secret.Name,
						},
					},
				},
			},
			{
				Name: "DB_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: "MONGODB_CLUSTER_MONITOR_PASSWORD",
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secret.Name,
						},
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
						Key: api.PMMServerToken,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secret.Name,
						},
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
				Value: "/tmp",
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

	pmmAgentScriptEnv := PMMAgentScript(cr)
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

	if v, exists := secret.Data[api.PMMServerToken]; exists && len(v) != 0 {
		return containerForPMM3(cr, secret, dbPort, customAdminParams)
	}

	if !cr.Spec.PMM.HasSecret(secret) {
		return nil
	}

	pmmC := containerForPMM2(cr, secret, dbPort, customAdminParams)

	clusterName := cr.Name
	if len(cr.Spec.PMM.CustomClusterName) > 0 {
		clusterName = cr.Spec.PMM.CustomClusterName

	}
	clusterPmmEnvs := []corev1.EnvVar{
		{
			Name:  "CLUSTER_NAME",
			Value: clusterName,
		},
	}
	pmmC.Env = append(pmmC.Env, clusterPmmEnvs...)

	pmmAgentScriptEnv := PMMAgentScript(cr)
	pmmC.Env = append(pmmC.Env, pmmAgentScriptEnv...)

	if cr.CompareVersion("1.10.0") >= 0 {
		// PMM team added these flags which allows us to avoid
		// container crash, but just restart pmm-agent till it recovers
		// the connection.
		sidecarEnvs := []corev1.EnvVar{
			{
				Name:  "PMM_AGENT_SIDECAR",
				Value: "true",
			},
			{
				Name:  "PMM_AGENT_SIDECAR_SLEEP",
				Value: "5",
			},
		}
		pmmC.Env = append(pmmC.Env, sidecarEnvs...)
	}
	if cr.CompareVersion("1.15.0") >= 0 {
		// PMM team moved temp directory to /usr/local/percona/pmm2/tmp
		// but it doesn't work on OpenShift so we set it back to /tmp
		sidecarEnvs := []corev1.EnvVar{
			{
				Name:  "PMM_AGENT_PATHS_TEMPDIR",
				Value: "/tmp",
			},
		}
		pmmC.Env = append(pmmC.Env, sidecarEnvs...)
	}

	if cr.CompareVersion("1.16.0") >= 0 {
		pmmC.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{
						"bash",
						"-c",
						"pmm-admin unregister --force",
					},
				},
			},
		}
	}

	if cr.CompareVersion("1.17.0") >= 0 {
		pmmC.SecurityContext = cr.Spec.PMM.ContainerSecurityContext
	}

	return &pmmC
}
