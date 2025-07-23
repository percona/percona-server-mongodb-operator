package logcollector

import (
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

const (
	ConfigMapNameSuffix = "log-collector-config"
	VolumeName          = "log-collector-volume"

	FluentBitCustomConfigurationFile = "fluentbit_custom.conf"
)

func ConfigMapName(prefix string) string {
	if prefix == "" {
		return ConfigMapNameSuffix
	}
	return fmt.Sprintf("%s-%s", prefix, ConfigMapNameSuffix)
}

func Containers(cr *api.PerconaServerMongoDB, mongoPort int32) ([]corev1.Container, error) {
	if cr.Spec.LogCollector == nil || !cr.Spec.LogCollector.Enabled {
		return nil, nil
	}

	logCont, err := logContainer(cr)
	if err != nil {
		return nil, err
	}

	logRotationCont, err := logRotationContainer(cr, mongoPort)
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
				Name:      config.MongodDataVolClaimName,
				MountPath: config.MongodContainerDataDir,
			},
			{
				Name:      config.BinVolumeName,
				MountPath: config.BinMountPath,
			},
		},
	}

	if cr.Spec.LogCollector.Configuration != "" {
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      VolumeName,
			MountPath: "/opt/percona/logcollector/fluentbit/custom",
		})
	}

	return &container, nil
}

func logRotationContainer(cr *api.PerconaServerMongoDB, mongoPort int32) (*corev1.Container, error) {
	if cr.Spec.LogCollector == nil {
		return nil, errors.New("logcollector can't be nil")
	}

	boolFalse := false

	usersSecretName := api.UserSecretName(cr)

	envs := []corev1.EnvVar{
		{
			Name:  "MONGODB_HOST",
			Value: "localhost",
		},
		{
			Name:  "MONGODB_PORT",
			Value: strconv.Itoa(int(mongoPort)),
		},
		{
			Name: "MONGODB_USER",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_CLUSTER_ADMIN_USER_ESCAPED",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: usersSecretName,
					},
					Optional: &boolFalse,
				},
			},
		},
		{
			Name: "MONGODB_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key: "MONGODB_CLUSTER_ADMIN_PASSWORD_ESCAPED",
					LocalObjectReference: corev1.LocalObjectReference{
						Name: usersSecretName,
					},
					Optional: &boolFalse,
				},
			},
		},
	}

	container := corev1.Container{
		Name:            "logrotate",
		Image:           cr.Spec.LogCollector.Image,
		Env:             envs,
		ImagePullPolicy: cr.Spec.LogCollector.ImagePullPolicy,
		SecurityContext: cr.Spec.LogCollector.ContainerSecurityContext,
		Resources:       cr.Spec.LogCollector.Resources,
		Args: []string{
			"logrotate",
		},
		Command: []string{"/opt/percona/logcollector/entrypoint.sh"},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      config.MongodDataVolClaimName,
				MountPath: config.MongodContainerDataDir,
			},
			{
				Name:      config.BinVolumeName,
				MountPath: config.BinMountPath,
			},
		},
	}
	return &container, nil
}
