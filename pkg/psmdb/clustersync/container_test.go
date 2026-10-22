package clustersync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func TestContainerProbes(t *testing.T) {
	customLiveness := &corev1.Probe{
		Exec:          &corev1.ExecAction{Command: []string{"sh", "-c", "true"}},
		PeriodSeconds: 7,
	}
	customReadiness := &corev1.Probe{
		HTTPGet: &corev1.HTTPGetAction{
			Path: "/healthz",
			Port: intstr.FromInt32(HTTPPort),
		},
		PeriodSeconds: 3,
	}

	tests := map[string]struct {
		spec           api.PerconaServerMongoDBClusterSyncSpec
		wantLiveness   *corev1.Probe
		wantReadiness  *corev1.Probe
		mutateOverride func(spec *api.PerconaServerMongoDBClusterSyncSpec)
		assertNoAlias  bool
	}{
		"defaults when probes unset": {
			spec:          api.PerconaServerMongoDBClusterSyncSpec{},
			wantLiveness:  livenessProbe(),
			wantReadiness: readinessProbe(),
		},
		"liveness override applies, readiness still default": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				LivenessProbe: customLiveness,
			},
			wantLiveness:  customLiveness,
			wantReadiness: readinessProbe(),
		},
		"both overrides apply": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				LivenessProbe:  customLiveness,
				ReadinessProbe: customReadiness,
			},
			wantLiveness:  customLiveness,
			wantReadiness: customReadiness,
		},
		"override is deep-copied (no shared pointer with spec)": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				LivenessProbe: customLiveness,
			},
			wantLiveness:  customLiveness,
			wantReadiness: readinessProbe(),
			mutateOverride: func(spec *api.PerconaServerMongoDBClusterSyncSpec) {
				spec.LivenessProbe.PeriodSeconds = 99
			},
			assertNoAlias: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDBClusterSync{Spec: tc.spec}
			c := Container(cr, TLS{})

			require.NotNil(t, c.LivenessProbe)
			require.NotNil(t, c.ReadinessProbe)

			if tc.assertNoAlias {
				before := c.LivenessProbe.PeriodSeconds
				tc.mutateOverride(&cr.Spec)
				assert.Equal(t, before, c.LivenessProbe.PeriodSeconds,
					"container probe must not alias spec pointer")
				return
			}

			assert.Equal(t, tc.wantLiveness.ProbeHandler, c.LivenessProbe.ProbeHandler)
			assert.Equal(t, tc.wantLiveness.PeriodSeconds, c.LivenessProbe.PeriodSeconds)
			assert.Equal(t, tc.wantReadiness.ProbeHandler, c.ReadinessProbe.ProbeHandler)
			assert.Equal(t, tc.wantReadiness.PeriodSeconds, c.ReadinessProbe.PeriodSeconds)
		})
	}
}

func TestContainerEnv(t *testing.T) {
	const crName = "mysync"
	managed := []corev1.EnvVar{
		{Name: "PCSM_SOURCE_URI", ValueFrom: uriEnvSource(crName+"-pcsm-secret", URISecretSourceKey)},
		{Name: "PCSM_TARGET_URI", ValueFrom: uriEnvSource(crName+"-pcsm-secret", URISecretTargetKey)},
	}

	tests := map[string]struct {
		spec api.PerconaServerMongoDBClusterSyncSpec
		want []corev1.EnvVar
	}{
		"defaults: only managed URIs, source before target": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{},
			want: managed,
		},
		"user Env appended after managed URIs": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				Env: []corev1.EnvVar{
					{Name: "PCSM_REPL_NUM_WORKERS", Value: "16"},
					{Name: "PCSM_REPL_BULK_OPS_SIZE", Value: "1000"},
				},
			},
			want: append(append([]corev1.EnvVar{}, managed...),
				corev1.EnvVar{Name: "PCSM_REPL_NUM_WORKERS", Value: "16"},
				corev1.EnvVar{Name: "PCSM_REPL_BULK_OPS_SIZE", Value: "1000"},
			),
		},
		"user Env preserves entry order": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				Env: []corev1.EnvVar{
					{Name: "PCSM_Z", Value: "z"},
					{Name: "PCSM_A", Value: "a"},
					{Name: "PCSM_M", Value: "m"},
				},
			},
			want: append(append([]corev1.EnvVar{}, managed...),
				corev1.EnvVar{Name: "PCSM_Z", Value: "z"},
				corev1.EnvVar{Name: "PCSM_A", Value: "a"},
				corev1.EnvVar{Name: "PCSM_M", Value: "m"},
			),
		},
		"valueFrom EnvVar passes through unchanged": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				Env: []corev1.EnvVar{{
					Name: "PCSM_REPL_BULK_OPS_SIZE",
					ValueFrom: &corev1.EnvVarSource{
						ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
							Name: "pcsm-tuning",
							Key:  "bulkOpsSize",
						},
					},
				}},
			},
			want: append(append([]corev1.EnvVar{}, managed...), corev1.EnvVar{
				Name: "PCSM_REPL_BULK_OPS_SIZE",
				ValueFrom: &corev1.EnvVarSource{
					ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
						Name: "pcsm-tuning",
						Key:  "bulkOpsSize",
					},
				},
			}),
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDBClusterSync{
				Name: crName,
				Spec: tc.spec,
			}
			c := Container(cr, TLS{})
			assert.Equal(t, tc.want, c.Env)
		})
	}
}

func TestContainerArgs(t *testing.T) {
	tests := map[string]struct {
		pcsm api.ClusterSyncPCSMConfig
		want []string
	}{
		"empty config: nil args": {
			pcsm: api.ClusterSyncPCSMConfig{},
			want: nil,
		},
		"LogLevel only": {
			pcsm: api.ClusterSyncPCSMConfig{LogLevel: "debug"},
			want: []string{"--log-level=debug"},
		},
		"LogJSON true only": {
			pcsm: api.ClusterSyncPCSMConfig{LogJSON: new(true)},
			want: []string{"--log-json"},
		},
		"LogJSON explicitly false: no arg emitted": {
			pcsm: api.ClusterSyncPCSMConfig{LogJSON: new(false)},
			want: nil,
		},
		"both set: LogLevel precedes LogJSON for stable arg order": {
			pcsm: api.ClusterSyncPCSMConfig{
				LogLevel: "warn",
				LogJSON:  new(true),
			},
			want: []string{"--log-level=warn", "--log-json"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDBClusterSync{
				Spec: api.PerconaServerMongoDBClusterSyncSpec{PCSMConfig: tc.pcsm},
			}
			c := Container(cr, TLS{})
			assert.Equal(t, tc.want, c.Args)
		})
	}
}

func TestContainerTLS(t *testing.T) {
	readOnlyRoot := &corev1.SecurityContext{ReadOnlyRootFilesystem: new(true)}
	tls := TLS{SecretName: "target-ssl", Hash: "abc123"}

	t.Run("tls disabled: image entrypoint and no volumes", func(t *testing.T) {
		pod := PodTemplateSpec(&api.PerconaServerMongoDBClusterSync{Name: "mysync"}, TLS{})

		assert.Empty(t, pod.Spec.Containers[0].Command)
		assert.Empty(t, pod.Spec.Containers[0].VolumeMounts)
		assert.Empty(t, pod.Spec.Volumes)
		assert.NotContains(t, pod.Annotations, naming.AnnotationSSLHash)
	})

	t.Run("tls enabled: ssl secret mounted and PEM assembled before entrypoint", func(t *testing.T) {
		pod := PodTemplateSpec(&api.PerconaServerMongoDBClusterSync{Name: "mysync"}, tls)

		require.Len(t, pod.Spec.Volumes, 1)
		require.NotNil(t, pod.Spec.Volumes[0].Secret)
		assert.Equal(t, sslVolumeName, pod.Spec.Volumes[0].Name)
		assert.Equal(t, "target-ssl", pod.Spec.Volumes[0].Secret.SecretName)

		c := pod.Spec.Containers[0]
		require.Len(t, c.VolumeMounts, 1)
		assert.Equal(t, sslVolumeName, c.VolumeMounts[0].Name)
		assert.Equal(t, config.SSLDir, c.VolumeMounts[0].MountPath)
		assert.True(t, c.VolumeMounts[0].ReadOnly)

		require.Len(t, c.Command, 4)
		assert.Equal(t, "/bin/bash", c.Command[0])
		assert.Contains(t, c.Command[2], "cat "+config.SSLDir+"/tls.key "+config.SSLDir+"/tls.crt > "+TLSPEMFile)
		assert.Contains(t, c.Command[2], `exec `+imageEntrypoint+` pcsm "$@"`)
		assert.Equal(t, "abc123", pod.Annotations[naming.AnnotationSSLHash])
	})

	t.Run("tls enabled with read-only root: writable /tmp added", func(t *testing.T) {
		cr := &api.PerconaServerMongoDBClusterSync{
			Name: "mysync",
			Spec: api.PerconaServerMongoDBClusterSyncSpec{ContainerSecurityContext: readOnlyRoot},
		}
		pod := PodTemplateSpec(cr, tls)

		require.Len(t, pod.Spec.Volumes, 2)
		assert.Equal(t, tmpVolumeName, pod.Spec.Volumes[1].Name)
		require.NotNil(t, pod.Spec.Volumes[1].EmptyDir)

		mounts := pod.Spec.Containers[0].VolumeMounts
		require.Len(t, mounts, 2)
		assert.Equal(t, tmpVolumeName, mounts[1].Name)
		assert.Equal(t, tmpMountPath, mounts[1].MountPath)
	})

	t.Run("user annotations are preserved and not mutated", func(t *testing.T) {
		userAnnotations := map[string]string{"team": "sre"}
		cr := &api.PerconaServerMongoDBClusterSync{
			Name: "mysync",
			Spec: api.PerconaServerMongoDBClusterSyncSpec{Annotations: userAnnotations},
		}
		pod := PodTemplateSpec(cr, tls)

		assert.Equal(t, "sre", pod.Annotations["team"])
		assert.Equal(t, "abc123", pod.Annotations[naming.AnnotationSSLHash])
		assert.NotContains(t, userAnnotations, naming.AnnotationSSLHash, "spec annotations must not be mutated")
	})
}
