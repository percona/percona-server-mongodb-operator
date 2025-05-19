package logcollector

import (
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func Containers(cr *api.PerconaServerMongoDB) ([]corev1.Container, error) {
	if cr.Spec.LogCollector == nil || !cr.Spec.LogCollector.Enabled {
		return nil, nil
	}

	logCont, err := logContainer(cr)
	if err != nil {
		return nil, err
	}

	logRotationCont, err := logRotationContainer(cr)
	if err != nil {
		return nil, err
	}

	return []corev1.Container{*logCont, *logRotationCont}, nil
}

func logContainer(cr *api.PerconaServerMongoDB) (*corev1.Container, error) {
	if cr.Spec.LogCollector == nil {
		return nil, errors.New("logcollector can't be nil")
	}

	envs := []corev1.EnvVar{
		{
			Name:  "LOG_DATA_DIR",
			Value: config.MongodContainerDataLogsDir,
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
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: "metadata.name",
				},
			},
		},
	}

	container := corev1.Container{
		Name:            "logs",
		Image:           cr.Spec.LogCollector.Image,
		ImagePullPolicy: cr.Spec.LogCollector.ImagePullPolicy,
		Env:             envs,
		Args: []string{
			"fluent-bit",
		},
		Command:         []string{"/opt/percona/logcollector/entrypoint.sh"},
		SecurityContext: cr.Spec.LogCollector.ContainerSecurityContext,
		Resources:       cr.Spec.LogCollector.Resources,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      config.MongodDataLogsVolClaimName,
				MountPath: config.MongodContainerDataLogsDir,
			},
			{
				Name:      config.BinVolumeName,
				MountPath: config.BinMountPath,
			},
		},
	}

	return &container, nil
}

func logRotationContainer(cr *api.PerconaServerMongoDB) (*corev1.Container, error) {
	if cr.Spec.LogCollector == nil {
		return nil, errors.New("logcollector can't be nil")
	}

	container := corev1.Container{
		Name:            "logrotate",
		Image:           cr.Spec.LogCollector.Image,
		ImagePullPolicy: cr.Spec.LogCollector.ImagePullPolicy,
		SecurityContext: cr.Spec.LogCollector.ContainerSecurityContext,
		Resources:       cr.Spec.LogCollector.Resources,
		Args: []string{
			"logrotate",
		},
		Command: []string{"/opt/percona/logcollector/entrypoint.sh"},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      config.MongodDataLogsVolClaimName,
				MountPath: config.MongodContainerDataLogsDir,
			},
			{
				Name:      config.BinVolumeName,
				MountPath: config.BinMountPath,
			},
		},
	}

	if cr.Spec.LogCollector.Configuration != "" {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "logcollector-config",
			MountPath: "/etc/fluentbit/custom",
		})
	}
	return &container, nil
}
