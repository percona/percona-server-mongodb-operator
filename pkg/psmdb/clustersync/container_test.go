package clustersync

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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
			wantLiveness:  livenessProbe(),
			wantReadiness: readinessProbe(),
		},
		"newer crVersion keeps the defaults": {
			cluster:       targetCluster("1.25.0"),
			wantLiveness:  livenessProbe(),
			wantReadiness: readinessProbe(),
		},
		"older crVersion gets no probes": {
			cluster: targetCluster("1.23.0"),
		},
		"nil cluster gets no probes": {},
		"liveness override applies, readiness still default": {
			cluster:       targetCluster("1.24.0"),
			spec:          api.PerconaServerMongoDBClusterSyncSpec{LivenessProbe: customLiveness},
			wantLiveness:  customLiveness,
			wantReadiness: readinessProbe(),
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
			c := Container(syncCR(tc.spec), tc.cluster)

			assert.Equal(t, tc.wantLiveness, c.LivenessProbe)
			assert.Equal(t, tc.wantReadiness, c.ReadinessProbe)
		})
	}

	// A STANDBY answers /status with HTTP 409, so probing it would hold
	// every standby NotReady and leave the group with no eligible
	// successor. /metrics is served by ACTIVE and STANDBY alike.
	t.Run("default probes target /metrics, never /status", func(t *testing.T) {
		c := Container(syncCR(api.PerconaServerMongoDBClusterSyncSpec{}), targetCluster("1.24.0"))

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

		c := Container(cr, targetCluster("1.24.0"))
		before := c.LivenessProbe.PeriodSeconds
		cr.Spec.LivenessProbe.PeriodSeconds = 99

		assert.Equal(t, before, c.LivenessProbe.PeriodSeconds,
			"container probe must not alias spec pointer")
	})
}

func TestContainerEnv(t *testing.T) {
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
		"user override of a managed variable wins by coming last": {
			spec: api.PerconaServerMongoDBClusterSyncSpec{
				Env: []corev1.EnvVar{{Name: "PCSM_GROUP_NAME", Value: "custom-group"}},
			},
			want: append(append([]corev1.EnvVar{}, managed...),
				corev1.EnvVar{Name: "PCSM_GROUP_NAME", Value: "custom-group"},
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
			c := Container(syncCR(tc.spec), targetCluster("1.24.0"))
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
			c := Container(syncCR(api.PerconaServerMongoDBClusterSyncSpec{PCSMConfig: tc.pcsm}), targetCluster("1.24.0"))
			assert.Equal(t, tc.want, c.Args)
		})
	}
}
