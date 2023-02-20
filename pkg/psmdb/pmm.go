package psmdb

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

// PMMContainer returns a pmm container from given spec
func PMMContainer(cr *api.PerconaServerMongoDB, secret *corev1.Secret, customAdminParams string) corev1.Container {
	_, oka := secret.Data[api.PMMAPIKey]
	_, okl := secret.Data[api.PMMUserKey]
	_, okp := secret.Data[api.PMMPasswordKey]
	customLogin := oka || (okl && okp)

	spec := cr.Spec.PMM
	ports := []corev1.ContainerPort{{ContainerPort: 7777}}

	for i := 30100; i <= 30105; i++ {
		ports = append(ports, corev1.ContainerPort{ContainerPort: int32(i)})
	}

	dbArgsEnv := []corev1.EnvVar{
		{
			Name: "MONGODB_USER",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_CLUSTER_MONITOR_USER_ESCAPED",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
				},
			},
		},
		{
			Name: "MONGODB_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_CLUSTER_MONITOR_PASSWORD_ESCAPED",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: secret.Name,
					},
				},
			},
		},
		{
			Name:  "DB_ARGS",
			Value: "--uri=mongodb://$(MONGODB_USER):$(MONGODB_PASSWORD)@127.0.0.1:27017/",
		},
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
			Value: "27017",
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
	}

	if cr.CompareVersion("1.2.0") >= 0 {
		pmm.Env = append(pmm.Env, dbEnv...)
	} else {
		pmm.Env = append(pmm.Env, dbArgsEnv...)
	}

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

	if cr.CompareVersion("1.13.0") >= 0 && !cr.Spec.UnsafeConf {
		pmm.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      "ssl",
				MountPath: SSLDir,
				ReadOnly:  true,
			},
		}
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
	pmmServerArgs := "$(PMM_ADMIN_CUSTOM_PARAMS) --skip-connection-check --metrics-mode=push "
	pmmServerArgs += " --username=$(DB_USER) --password=$(DB_PASSWORD) --cluster=$(CLUSTER_NAME) "
	pmmServerArgs += "--service-name=$(PMM_AGENT_SETUP_NODE_NAME) --host=$(DB_HOST) --port=$(DB_PORT)"

	if cr.CompareVersion("1.13.0") >= 0 && !cr.Spec.UnsafeConf {
		tlsParams := []string{
			"--tls",
			"--tls-skip-verify",
			"--tls-certificate-key-file=/tmp/tls.pem",
			fmt.Sprintf("--tls-ca-file=%s/ca.crt", SSLDir),
			"--authentication-mechanism=SCRAM-SHA-1",
			"--authentication-database=admin",
		}
		pmmServerArgs += " " + strings.Join(tlsParams, " ")
	}

	pmmWait := "pmm-admin status --wait=10s;"
	pmmAddService := fmt.Sprintf("pmm-admin add $(DB_TYPE) %s;", pmmServerArgs)
	pmmAnnotate := "pmm-admin annotate --service-name=$(PMM_AGENT_SETUP_NODE_NAME) 'Service restarted'"
	prerunScript := pmmWait + "\n" + pmmAddService + "\n" + pmmAnnotate

	if cr.CompareVersion("1.13.0") >= 0 && !cr.Spec.UnsafeConf {
		prepareTLS := fmt.Sprintf("cat %[1]s/tls.key %[1]s/tls.crt > /tmp/tls.pem;", SSLDir)
		prerunScript = prepareTLS + "\n" + prerunScript
	}

	return []corev1.EnvVar{
		{
			Name:  "PMM_AGENT_PRERUN_SCRIPT",
			Value: prerunScript,
		},
	}
}

// AddPMMContainer creates the container object for a pmm-client
func AddPMMContainer(ctx context.Context, cr *api.PerconaServerMongoDB, secret *corev1.Secret, customAdminParams string) *corev1.Container {
	if !cr.Spec.PMM.Enabled {
		return nil
	}

	if !cr.Spec.PMM.HasSecret(secret) {
		return nil
	}

	pmmC := PMMContainer(cr, secret, customAdminParams)
	if cr.CompareVersion("1.2.0") >= 0 {
		pmmC.Resources = cr.Spec.PMM.Resources
	}
	if cr.CompareVersion("1.6.0") >= 0 {
		pmmC.Lifecycle = &corev1.Lifecycle{
			PreStop: &corev1.LifecycleHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"bash", "-c", "pmm-admin inventory remove node --force $(pmm-admin status --json | python -c \"import sys, json; print(json.load(sys.stdin)['pmm_agent_status']['node_id'])\")"},
				},
			},
		}
		clusterPmmEnvs := []corev1.EnvVar{
			{
				Name:  "CLUSTER_NAME",
				Value: cr.Name,
			},
		}
		pmmC.Env = append(pmmC.Env, clusterPmmEnvs...)
		pmmAgentScriptEnv := PMMAgentScript(cr)
		pmmC.Env = append(pmmC.Env, pmmAgentScriptEnv...)
	}
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

	return &pmmC
}
