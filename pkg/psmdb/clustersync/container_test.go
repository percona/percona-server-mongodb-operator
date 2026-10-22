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
func TestDefaultProbesTargetMetrics(t *testing.T) {
	c := Container(&api.PerconaServerMongoDBClusterSync{})

	for name, probe := range map[string]*corev1.Probe{
		"liveness":  c.LivenessProbe,
		"readiness": c.ReadinessProbe,
	} {
		t.Run(name, func(t *testing.T) {
			require.NotNil(t, probe.Exec)
			cmd := strings.Join(probe.Exec.Command, " ")
			assert.Contains(t, cmd, "http://127.0.0.1:2242/metrics")
			assert.NotContains(t, cmd, "/status", "a standby answers /status with 409 and would never become Ready")
		})
	}
}

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
			c := Container(cr)

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

// TestContainerEnv checks the env the operator owns and the ordering rule
// that makes user overrides work.
//
// PCSM_GROUP_NAME is what scopes leader election. PCSM defaults it to
// "default", so two ClusterSync CRs pointed at the same target would join
// one election and fight over a single lease; deriving it from
// namespace+CR name makes that impossible. User env is appended last on
// purpose: kubelet keeps the last value for a repeated variable, so a user
// can still override any managed variable without the operator having to
// special-case it.
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
			cr := &api.PerconaServerMongoDBClusterSync{
				Name:      crName,
				Namespace: crNS,
				Spec:      tc.spec,
			}
			c := Container(cr)
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
			c := Container(cr)
			assert.Equal(t, tc.want, c.Args)
		})
	}
}
