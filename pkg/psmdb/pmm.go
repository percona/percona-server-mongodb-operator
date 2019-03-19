package psmdb

import (
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

// PMMContainer returns a pmm container from given spec
func PMMContainer(spec api.PMMSpec, secrets string) corev1.Container {
	return corev1.Container{
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
				Name: "PMM_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						Key: "PMM_SERVER_PASSWORD",
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
						Key: "PMM_SERVER_PASSWORD",
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
		},
	}
}
