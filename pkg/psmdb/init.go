package psmdb

import (
	"strings"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func EntrypointInitContainer(cr *api.PerconaServerMongoDB, name, image string, pullPolicy corev1.PullPolicy, command []string) corev1.Container {
	if len(command) == 0 {
		command = []string{"/init-entrypoint.sh"}
	}

	container := corev1.Container{
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      config.MongodDataVolClaimName,
				MountPath: config.MongodContainerDataDir,
			},
		},
		Image:           image,
		Name:            name,
		Command:         command,
		ImagePullPolicy: pullPolicy,
	}

	if cr.CompareVersion("1.13.0") >= 0 {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      config.BinVolumeName,
			MountPath: config.BinMountPath,
		})
	}

	return container
}

func InitContainers(cr *api.PerconaServerMongoDB, initImage string) []corev1.Container {
	image := cr.Spec.InitImage
	if len(image) == 0 {
		if cr.CompareVersion(version.Version()) != 0 {
			image = strings.Split(initImage, ":")[0] + ":" + cr.Spec.CRVersion
		} else {
			image = initImage
		}
	}

	init := EntrypointInitContainer(cr, "mongo-init", image, cr.Spec.ImagePullPolicy, nil)

	if cr.CompareVersion("1.14.0") >= 0 {
		init.SecurityContext = cr.Spec.InitContainerSecurityContext
	}

	return []corev1.Container{init}
}
