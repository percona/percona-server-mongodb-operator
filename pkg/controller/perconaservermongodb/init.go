package perconaservermongodb

import (
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	corev1 "k8s.io/api/core/v1"
)

func EntrypointInitContainer(initImageName string) corev1.Container {
	return corev1.Container{
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      psmdb.MongodDataVolClaimName,
				MountPath: "/data/db",
			},
		},
		Image:   initImageName,
		Name:    "pxc-init",
		Command: []string{"/init-entrypoint.sh"},
	}
}
