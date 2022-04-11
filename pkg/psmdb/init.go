package psmdb

import (
	corev1 "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

var log = logf.Log.WithName("psmdb")

func EntrypointInitContainer(initImageName string, pullPolicy corev1.PullPolicy) corev1.Container {
	return corev1.Container{
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      MongodDataVolClaimName,
				MountPath: "/data/db",
			},
		},
		Image:           initImageName,
		Name:            "mongo-init",
		Command:         []string{"/init-entrypoint.sh"},
		ImagePullPolicy: pullPolicy,
	}
}
