package clustersync

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

// TestDefaultProbesTargetMetrics is the HA-critical probe assertion.
//
// A STANDBY instance answers /status with HTTP 409, so probing /status
// would hold every standby NotReady: the deployment would never finish a
// rollout, a PDB could never allow an eviction, and the group would have
// no eligible successor at the moment it needs one. /metrics is served by
// ACTIVE and STANDBY alike, so readiness means "healthy and eligible"
// rather than "currently the writer" — which is exactly the question
// Kubernetes is asking. Which instance holds the lease is reported in
// status.activeInstance instead.
func TestContainerProbes(t *testing.T) {
	t.Parallel()

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
		cluster       *api.PerconaServerMongoDB
		spec          api.PerconaServerMongoDBClusterSyncSpec
		wantLiveness  *corev1.Probe
		wantReadiness *corev1.Probe
	}{
		"defaults when probes unset": {
			cluster:       targetCluster("1.24.0"),
			wantLiveness:  livenessProbe(MetricsPath),
			wantReadiness: readinessProbe(MetricsPath),
		},
		"newer crVersion keeps the defaults": {
			cluster:       targetCluster("1.25.0"),
			wantLiveness:  livenessProbe(MetricsPath),
			wantReadiness: readinessProbe(MetricsPath),
		},
		"older crVersion keeps the 1.23 /status probes": {
			cluster:       targetCluster("1.23.0"),
			wantLiveness:  livenessProbe(StatusPath),
			wantReadiness: readinessProbe(StatusPath),
		},
		"nil cluster keeps the 1.23 /status probes": {
			wantLiveness:  livenessProbe(StatusPath),
			wantReadiness: readinessProbe(StatusPath),
		},
		"liveness override applies, readiness still default": {
			cluster:       targetCluster("1.24.0"),
			spec:          api.PerconaServerMongoDBClusterSyncSpec{LivenessProbe: customLiveness},
			wantLiveness:  customLiveness,
			wantReadiness: readinessProbe(MetricsPath),
		},
		"both overrides apply": {
			cluster: targetCluster("1.24.0"),
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				LivenessProbe:  customLiveness,
				ReadinessProbe: customReadiness,
			},
			wantLiveness:  customLiveness,
			wantReadiness: customReadiness,
		},
		"overrides are not version gated": {
			cluster: targetCluster("1.23.0"),
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				LivenessProbe:  customLiveness,
				ReadinessProbe: customReadiness,
			},
			wantLiveness:  customLiveness,
			wantReadiness: customReadiness,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			c := Container(syncCR(tc.spec), tc.cluster, TLS{})

			assert.Equal(t, tc.wantLiveness, c.LivenessProbe)
			assert.Equal(t, tc.wantReadiness, c.ReadinessProbe)
		})
	}

	// A STANDBY answers /status with HTTP 409, so probing it would hold
	// every standby NotReady and leave the group with no eligible
	// successor. /metrics is served by ACTIVE and STANDBY alike.
	t.Run("default probes target /metrics, never /status", func(t *testing.T) {
		c := Container(syncCR(api.PerconaServerMongoDBClusterSyncSpec{}), targetCluster("1.24.0"), TLS{})

		for name, probe := range map[string]*corev1.Probe{
			"liveness":  c.LivenessProbe,
			"readiness": c.ReadinessProbe,
		} {
			require.NotNil(t, probe.Exec, name)
			cmd := strings.Join(probe.Exec.Command, " ")
			assert.Contains(t, cmd, "http://127.0.0.1:2242/metrics", name)
			assert.NotContains(t, cmd, "/status", name)
		}
	})

	t.Run("override is deep-copied (no shared pointer with spec)", func(t *testing.T) {
		cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{LivenessProbe: customLiveness.DeepCopy()})

		c := Container(cr, targetCluster("1.24.0"), TLS{})
		before := c.LivenessProbe.PeriodSeconds
		cr.Spec.LivenessProbe.PeriodSeconds = 99

		assert.Equal(t, before, c.LivenessProbe.PeriodSeconds,
			"container probe must not alias spec pointer")
	})
}

func TestContainerEnv(t *testing.T) {
	t.Parallel()

	const (
		crName = "mysync"
		crNS   = "ns"
	)
	managed := []corev1.EnvVar{
		{Name: "PCSM_SOURCE_URI", ValueFrom: uriEnvSource(crName+"-pcsm-secret", URISecretSourceKey)},
		{Name: "PCSM_TARGET_URI", ValueFrom: uriEnvSource(crName+"-pcsm-secret", URISecretTargetKey)},
		{Name: "PCSM_GROUP_NAME", Value: crNS + "-" + crName},
	}

	tests := map[string]struct {
		spec api.PerconaServerMongoDBClusterSyncSpec
		want []corev1.EnvVar
	}{
		"reserved variables cannot be overridden through spec.env": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				Env: []corev1.EnvVar{
					{Name: "PCSM_GROUP_NAME", Value: "custom-group"},
					{Name: "PCSM_SOURCE_URI", Value: "mongodb://evil"},
					{Name: "PCSM_TARGET_URI", Value: "mongodb://evil"},
					{Name: "PCSM_LOG_LEVEL", Value: "debug"},
				},
			},
			want: append(append([]corev1.EnvVar{}, managed...),
				corev1.EnvVar{Name: "PCSM_LOG_LEVEL", Value: "debug"},
			),
		},
		"defaults: managed URIs and the per-CR group name": {
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
			c := Container(syncCR(tc.spec), targetCluster("1.24.0"), TLS{})
			assert.Equal(t, tc.want, c.Env)
		})
	}
}

func TestContainerGroupNameVersionGate(t *testing.T) {
	groupName := func(c corev1.Container) string {
		for _, e := range c.Env {
			if e.Name == "PCSM_GROUP_NAME" {
				return e.Value
			}
		}
		return ""
	}

	tests := map[string]struct {
		cluster *api.PerconaServerMongoDB
		size    int32
		want    string
	}{
		"1.24 single instance gets the group name": {cluster: targetCluster("1.24.0"), size: 1, want: "ns-mysync"},
		"1.23 single instance keeps the 1.23 env":  {cluster: targetCluster("1.23.0"), size: 1},
		"nil cluster keeps the 1.23 env":           {size: 1},
		"1.23 HA group still needs a lease":        {cluster: targetCluster("1.23.0"), size: 3, want: "ns-mysync"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			cr := syncCR(api.PerconaServerMongoDBClusterSyncSpec{Size: tc.size})
			assert.Equal(t, tc.want, groupName(Container(cr, tc.cluster, TLS{})))
		})
	}
}

func TestContainerArgs(t *testing.T) {
	t.Parallel()

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
			c := Container(syncCR(api.PerconaServerMongoDBClusterSyncSpec{PCSMConfig: tc.pcsm}), targetCluster("1.24.0"), TLS{})
			assert.Equal(t, tc.want, c.Args)
		})
	}
}

func TestContainerTLS(t *testing.T) {
	readOnlyRoot := &corev1.SecurityContext{ReadOnlyRootFilesystem: new(true)}
	tls := TLS{SecretName: "target-ssl", Hash: "abc123"}

	t.Run("tls disabled: image entrypoint and no volumes", func(t *testing.T) {
		pod := PodTemplateSpec(&api.PerconaServerMongoDBClusterSync{Name: "mysync"}, nil, TLS{})

		assert.Empty(t, pod.Spec.Containers[0].Command)
		assert.Empty(t, pod.Spec.Containers[0].VolumeMounts)
		assert.Empty(t, pod.Spec.Volumes)
		assert.NotContains(t, pod.Annotations, naming.AnnotationSSLHash)
	})

	t.Run("tls enabled: ssl secret mounted and PEM assembled before entrypoint", func(t *testing.T) {
		pod := PodTemplateSpec(&api.PerconaServerMongoDBClusterSync{Name: "mysync"}, nil, tls)

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
		pod := PodTemplateSpec(cr, nil, tls)

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
		pod := PodTemplateSpec(cr, nil, tls)

		assert.Equal(t, "sre", pod.Annotations["team"])
		assert.Equal(t, "abc123", pod.Annotations[naming.AnnotationSSLHash])
		assert.NotContains(t, userAnnotations, naming.AnnotationSSLHash, "spec annotations must not be mutated")
	})
}
