package logrotate

import (
	"fmt"
	"net"
	"strconv"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
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

func Container(cr *api.PerconaServerMongoDB, mongoAddr string, logVol config.LogVolume) (*corev1.Container, error) {
	if cr.Spec.LogCollector == nil {
		return nil, errors.New("logcollector can't be nil")
	}

	host, port, err := net.SplitHostPort(mongoAddr)
	if err != nil {
		return nil, errors.Wrap(err, "parse mongo address")
	}

	usersSecretName := api.UserSecretName(cr)

	envs := []corev1.EnvVar{
		{
			Name:  "MONGODB_HOST",
			Value: host,
		},
		{
			Name:  "MONGODB_PORT",
			Value: port,
		},
		{
			Name: "MONGODB_USER",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key:      "MONGODB_CLUSTER_ADMIN_USER_ESCAPED",
					Name:     usersSecretName,
					Optional: new(false),
				},
			},
		},
		{
			Name: "MONGODB_PASSWORD",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					Key:      "MONGODB_CLUSTER_ADMIN_PASSWORD_ESCAPED",
					Name:     usersSecretName,
					Optional: new(false),
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
				Name:      logVol.Name,
				MountPath: logVol.MountPath,
			},
			{
				Name:      config.BinVolumeName,
				MountPath: config.BinMountPath,
			},
		},
	}
	if cr.CompareVersion("1.24.0") >= 0 {
		container.Env = append([]corev1.EnvVar{{
			Name: "POD_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			},
		}}, container.Env...)
		container.Env = append(container.Env, corev1.EnvVar{
			Name:  "MONGODB_TLS_ENABLED",
			Value: strconv.FormatBool(cr.TLSEnabled()),
		})
		if cr.TLSEnabled() {
			if cr.Spec.TLS != nil && cr.Spec.TLS.AllowInvalidCertificates != nil && *cr.Spec.TLS.AllowInvalidCertificates {
				container.Env = append(container.Env, corev1.EnvVar{
					Name:  "MONGODB_TLS_INSECURE",
					Value: "true",
				})
			}
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				Name:      "ssl",
				MountPath: config.SSLDir,
				ReadOnly:  true,
			})
		}
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

		if cr.CompareVersion("1.23.0") >= 0 {
			if cr.Spec.LogCollector.LogRotate.LivenessProbe != nil {
				container.LivenessProbe = cr.Spec.LogCollector.LogRotate.LivenessProbe
			}
			if cr.Spec.LogCollector.LogRotate.ReadinessProbe != nil {
				container.ReadinessProbe = cr.Spec.LogCollector.LogRotate.ReadinessProbe
			}
		}
	}

	return &container, nil
}
