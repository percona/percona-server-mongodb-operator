package logcollector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestContainers(t *testing.T) {
	tests := map[string]struct {
		logCollector           *api.LogCollectorSpec
		secrets                *corev1.Secret
		expectedContainerNames []string
		expectedContainers     []corev1.Container
		expectedErr            error
	}{
		"nil logcollector": {
			logCollector: nil,
		},
		"logcollector disabled": {
			logCollector: &api.LogCollectorSpec{
				Enabled: false,
			},
		},
		"logcollector enabled": {
			logCollector: &api.LogCollectorSpec{
				Enabled:         true,
				Image:           "log-test-image",
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
			expectedContainerNames: []string{"logs", "logrotate"},
			expectedContainers:     expectedContainers(""),
		},
		"logcollector enabled with configuration": {
			logCollector: &api.LogCollectorSpec{
				Enabled:         true,
				Image:           "log-test-image",
				ImagePullPolicy: corev1.PullIfNotPresent,
				Configuration:   "my-config",
			},
			expectedContainerNames: []string{"logs", "logrotate"},
			expectedContainers:     expectedContainers("my-config"),
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion:    version.Version(),
					LogCollector: tt.logCollector,
					Secrets: &api.SecretsSpec{
						Users: "users-secret",
					},
				},
			}

			containers, err := Containers(cr, 27017)

			if tt.expectedContainers != nil {
				var gotNames []string
				for _, c := range containers {
					gotNames = append(gotNames, c.Name)
				}
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedContainerNames, gotNames)
				assert.Equal(t, tt.expectedContainers, containers)
				return
			}
			if tt.expectedErr != nil {
				assert.EqualError(t, err, tt.expectedErr.Error())
			}

		})
	}
}

func expectedContainers(configuration string) []corev1.Container {
	logsC := corev1.Container{
		Name:            "logs",
		Image:           "log-test-image",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env: []corev1.EnvVar{
			{Name: "LOG_DATA_DIR", Value: config.MongodContainerDataLogsDir},
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
		},
		Args:    []string{"fluent-bit"},
		Command: []string{"/opt/percona/logcollector/entrypoint.sh"},
		VolumeMounts: []corev1.VolumeMount{
			{Name: config.MongodDataVolClaimName, MountPath: config.MongodContainerDataDir},
			{Name: config.BinVolumeName, MountPath: config.BinMountPath},
		},
	}

	if configuration != "" {
		logsC.VolumeMounts = append(logsC.VolumeMounts, corev1.VolumeMount{
			Name:      VolumeName,
			MountPath: "/opt/percona/logcollector/fluentbit/custom",
		})
	}

	boolFalse := false

	logRotateC := corev1.Container{
		Name:            "logrotate",
		Image:           "log-test-image",
		ImagePullPolicy: corev1.PullIfNotPresent,
		Args:            []string{"logrotate"},
		Command:         []string{"/opt/percona/logcollector/entrypoint.sh"},
		Env: []corev1.EnvVar{
			{Name: "MONGODB_HOST", Value: "localhost"},
			{Name: "MONGODB_PORT", Value: "27017"},
			{
				Name: "MONGODB_USER",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "internal-my-cluster-users",
						},
						Key:      "MONGODB_CLUSTER_ADMIN_USER_ESCAPED",
						Optional: &boolFalse,
					},
				},
			},
			{
				Name: "MONGODB_PASSWORD",
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: "internal-my-cluster-users",
						},
						Key:      "MONGODB_CLUSTER_ADMIN_PASSWORD_ESCAPED",
						Optional: &boolFalse,
					},
				},
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: config.MongodDataVolClaimName, MountPath: config.MongodContainerDataDir},
			{Name: config.BinVolumeName, MountPath: config.BinMountPath},
		},
	}

	return []corev1.Container{logsC, logRotateC}
}
