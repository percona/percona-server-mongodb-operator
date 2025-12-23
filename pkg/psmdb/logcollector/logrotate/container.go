package logrotate

import (
	"errors"
	"fmt"
	"strconv"

	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	corev1 "k8s.io/api/core/v1"
)

const (
	ConfigMapNameSuffix = "logrotate-config"
	VolumeName          = "logrotate-config"
	MongodbConfig       = "mongodb.conf"

	configDir = "/opt/percona/logcollector/logrotate/conf.d"
)

func ConfigMapName(prefix string) string {
	if prefix == "" {
		return ConfigMapNameSuffix
	}
	return fmt.Sprintf("%s-%s", prefix, ConfigMapNameSuffix)
}

func Container(cr *api.PerconaServerMongoDB, mongoPort int32) (*corev1.Container, error) {
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

	if cr.Spec.LogCollector.LogRotate != nil {
		if cr.Spec.LogCollector.LogRotate.Configuration != "" || cr.Spec.LogCollector.LogRotate.ExtraConfig.Name != "" {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      VolumeName,
				MountPath: configDir,
			})
		}
		if cr.Spec.LogCollector.LogRotate.Schedule != "" {
			container.Env = append(container.Env, corev1.EnvVar{
				Name:  "LOGROTATE_SCHEDULE",
				Value: cr.Spec.LogCollector.LogRotate.Schedule,
			})
		}
	}

	return &container, nil
}
