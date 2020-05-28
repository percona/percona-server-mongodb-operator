package psmdb

import (
	corev1 "k8s.io/api/core/v1"
)

func EntrypointInitContainer(initImageName string) corev1.Container {
	return corev1.Container{
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      MongodDataVolClaimName,
				MountPath: "/data/db",
			},
		},
		Image:   initImageName,
		Name:    "mongo-init",
		Command: []string{"/init-entrypoint.sh"},
	}
}
