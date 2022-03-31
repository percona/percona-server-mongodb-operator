package psmdb

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const (
	PMMUserKey     = "PMM_SERVER_USER"
	PMMPasswordKey = "PMM_SERVER_PASSWORD"
)

// PMMContainer returns a pmm container from given spec
func PMMContainer(cr *api.PerconaServerMongoDB, secrets string, customLogin bool, clusterName string, v120OrGreater bool, v160OrGreater bool, customAdminParams string) corev1.Container {
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
						Name: secrets,
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
						Name: secrets,
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
						Name: secrets,
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
						Name: secrets,
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
			Value: clusterName,
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

	switch v120OrGreater {
	case true:
		pmm.Env = append(pmm.Env, dbEnv...)
	default:
		pmm.Env = append(pmm.Env, dbArgsEnv...)
	}

	if customLogin {
		pmm.Env = append(pmm.Env, []corev1.EnvVar{
			{
				Name: "PMM_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: PMMUserKey,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secrets,
						},
					},
				},
			},
			{
				Name: "PMM_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: PMMPasswordKey,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secrets,
						},
					},
				},
			},
		}...)
	}

	if v160OrGreater {
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
		pmm.Env = append(pmm.Env, pmmAgentEnvs(spec.ServerHost, customLogin, secrets, customAdminParams)...)
	}

	return pmm
}

func pmmAgentEnvs(pmmServerHost string, customLogin bool, secrets, customAdminParams string) []corev1.EnvVar {
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
			Value: pmmServerHost,
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
		pmmAgentEnvs = append(pmmAgentEnvs, []corev1.EnvVar{
			{
				Name: "PMM_AGENT_SERVER_USERNAME",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: PMMUserKey,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secrets,
						},
					},
				},
			},
			{
				Name: "PMM_AGENT_SERVER_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: PMMPasswordKey,
						LocalObjectReference: corev1.LocalObjectReference{
							Name: secrets,
						},
					},
				},
			},
		}...)
	}

	return pmmAgentEnvs
}

func PMMAgentScript() []corev1.EnvVar {
	pmmServerArgs := " $(PMM_ADMIN_CUSTOM_PARAMS) --skip-connection-check --metrics-mode=push "
	pmmServerArgs += " --username=$(DB_USER) --password=$(DB_PASSWORD) --cluster=$(CLUSTER_NAME) "
	pmmServerArgs += "--service-name=$(PMM_AGENT_SETUP_NODE_NAME) --host=$(DB_HOST) --port=$(DB_PORT)"

	return []corev1.EnvVar{
		{
			Name:  "PMM_AGENT_PRERUN_SCRIPT",
			Value: "pmm-admin status --wait=10s;\npmm-admin add $(DB_TYPE)" + pmmServerArgs + ";\npmm-admin annotate --service-name=$(PMM_AGENT_SETUP_NODE_NAME) 'Service restarted'",
		},
	}
}

// AddPMMContainer creates the container object for a pmm-client
func AddPMMContainer(cr *api.PerconaServerMongoDB, usersSecretName string, pmmsec corev1.Secret, customAdminParams string) (corev1.Container, error) {
	_, okl := pmmsec.Data[PMMUserKey]
	_, okp := pmmsec.Data[PMMPasswordKey]
	is120 := cr.CompareVersion("1.2.0") >= 0

	pmmC := PMMContainer(cr, usersSecretName, okl && okp, cr.Name, is120, cr.CompareVersion("1.6.0") >= 0, customAdminParams)
	if is120 {
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
		pmmAgentScriptEnv := PMMAgentScript()
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

	return pmmC, nil
}
