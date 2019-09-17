package psmdb

import (
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const (
	PMMUserKey     = "PMM_SERVER_USER"
	PMMPasswordKey = "PMM_SERVER_PASSWORD"
)

// PMMContainer returns a pmm container from given spec
func PMMContainer(spec api.PMMSpec, secrets string, customLogin bool, clusterName string, v120OrGreater bool) corev1.Container {
	ports := []corev1.ContainerPort{{ContainerPort: 7777}}

	for i := 30100; i <= 30105; i++ {
		ports = append(ports, corev1.ContainerPort{ContainerPort: int32(i)})
	}

	dbArgsEnv := []corev1.EnvVar{
		{
			Name: "MONGODB_USER",
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
			Name: "MONGODB_PASSWORD",
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
			Name:  "DB_ARGS",
			Value: "--uri=mongodb://$(MONGODB_USER):$(MONGODB_PASSWORD)@127.0.0.1:27017/ --use-profiler",
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
		ImagePullPolicy: corev1.PullAlways,
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

	return pmm
}
