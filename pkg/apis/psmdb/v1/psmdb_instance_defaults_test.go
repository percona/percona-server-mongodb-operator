package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// testCR is the cluster an instance is defaulted against. crVersion and the
// TLS mode are parameters because the probe defaulting InstanceSpec.SetDefaults
// delegates to reads both.
func testCR(crVersion string, tlsMode TLSMode) *PerconaServerMongoDB {
	return &PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "cluster1", Namespace: "psmdb"},
		Spec: PerconaServerMongoDBSpec{
			CRVersion: crVersion,
			TLS:       &TLSSpec{Mode: tlsMode},
		},
	}
}

const currentCRVersion = "1.24.0"

// defaultedReplset is a replica set carrying a distinctive, non-default value
// for every field an instance can inherit, so an inherited value is
// distinguishable from a freshly defaulted one.
//
// The probes are written out rather than produced by the defaulting helpers:
// what these tests need is a value defaulting would never choose, so that
// inheritance is provable.
func defaultedReplset(t *testing.T) *ReplsetSpec {
	t.Helper()

	rs := &ReplsetSpec{
		Name: "rs0",
		MultiAZ: MultiAZ{
			ServiceAccountName: "rs-sa",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("500m")},
			},
			PodDisruptionBudget: &PodDisruptionBudgetSpec{
				MinAvailable: new(intstr.FromInt(2)),
			},
			TerminationGracePeriodSeconds: new(int64(120)),
			Annotations:                   map[string]string{"rs": "yes"},
		},
		Env:                      []corev1.EnvVar{{Name: "FROM_RS", Value: "1"}},
		EnvFrom:                  []corev1.EnvFromSource{{Prefix: "rs_"}},
		PodSecurityContext:       &corev1.PodSecurityContext{RunAsUser: new(int64(1001))},
		ContainerSecurityContext: &corev1.SecurityContext{RunAsNonRoot: new(true)},
	}

	// 99 and 98 are values no defaulting path produces, so seeing one on an
	// instance proves inheritance rather than a coincidental default.
	rs.LivenessProbe = &LivenessProbeExtended{
		Probe: corev1.Probe{
			PeriodSeconds: 99,
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{Command: []string{"/rs/liveness"}},
			},
		},
		StartupDelaySeconds: 7200,
	}
	rs.ReadinessProbe = &corev1.Probe{
		PeriodSeconds: 98,
		ProbeHandler: corev1.ProbeHandler{
			Exec: &corev1.ExecAction{Command: []string{"/rs/readiness"}},
		},
	}

	return rs
}

func TestInstanceSetDefaultsInherits(t *testing.T) {
	cr := testCR(currentCRVersion, TLSModeDisabled)
	rs := defaultedReplset(t)

	inst := &InstanceSpec{Name: "hot", Replicas: 3, VolumeSpec: testVol("1Gi")}
	require.NoError(t, inst.SetDefaults(cr, rs))

	assert.Equal(t, "rs-sa", inst.ServiceAccountName)
	assert.Equal(t, rs.Env, inst.Env)
	assert.Equal(t, rs.EnvFrom, inst.EnvFrom)
	assert.Equal(t, rs.PodSecurityContext, inst.PodSecurityContext)
	assert.Equal(t, rs.ContainerSecurityContext, inst.ContainerSecurityContext)
	assert.Equal(t, int32(99), inst.LivenessProbe.PeriodSeconds,
		"the replica set's probe is inherited whole, not re-defaulted")
	assert.Equal(t, int32(98), inst.ReadinessProbe.PeriodSeconds)

	t.Run("inherited values are copies", func(t *testing.T) {
		// The instance is reconciled into its own StatefulSet. Sharing backing
		// memory with the replica set would let one group's rollout mutate
		// another's desired spec.
		inst.LivenessProbe.PeriodSeconds = 1
		inst.ReadinessProbe.PeriodSeconds = 2
		inst.PodSecurityContext.RunAsUser = new(int64(2002))
		inst.Env[0].Value = "mutated"

		assert.Equal(t, int32(99), rs.LivenessProbe.PeriodSeconds)
		assert.Equal(t, int32(98), rs.ReadinessProbe.PeriodSeconds)
		assert.Equal(t, int64(1001), *rs.PodSecurityContext.RunAsUser)
		assert.Equal(t, "1", rs.Env[0].Value)
	})
}

func TestInstanceSetDefaultsKeepsDeclaredValues(t *testing.T) {
	cr := testCR(currentCRVersion, TLSModeDisabled)
	rs := defaultedReplset(t)

	inst := &InstanceSpec{
		Name: "hot", Replicas: 3, VolumeSpec: testVol("1Gi"),
		MultiAZ: MultiAZ{
			ServiceAccountName: "hot-sa",
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceCPU: resource.MustParse("4")},
			},
			PodDisruptionBudget:           &PodDisruptionBudgetSpec{MaxUnavailable: new(intstr.FromInt(3))},
			TerminationGracePeriodSeconds: new(int64(90)),
		},
		Env:                      []corev1.EnvVar{{Name: "FROM_INSTANCE", Value: "1"}},
		EnvFrom:                  []corev1.EnvFromSource{{Prefix: "inst_"}},
		PodSecurityContext:       &corev1.PodSecurityContext{RunAsUser: new(int64(2002))},
		ContainerSecurityContext: &corev1.SecurityContext{RunAsNonRoot: new(false)},
		LivenessProbe:            &LivenessProbeExtended{Probe: corev1.Probe{TimeoutSeconds: 42}},
		ReadinessProbe:           &corev1.Probe{TimeoutSeconds: 41},
	}
	require.NoError(t, inst.SetDefaults(cr, rs))

	assert.Equal(t, "hot-sa", inst.ServiceAccountName)
	assert.Equal(t, "4", inst.Resources.Requests.Cpu().String())
	assert.Equal(t, 3, inst.PodDisruptionBudget.MaxUnavailable.IntValue())
	assert.Equal(t, int64(90), *inst.TerminationGracePeriodSeconds)
	assert.Equal(t, "FROM_INSTANCE", inst.Env[0].Name)
	assert.Len(t, inst.Env, 1, "the replica set's env is not appended")
	assert.Equal(t, "inst_", inst.EnvFrom[0].Prefix)
	assert.Equal(t, int64(2002), *inst.PodSecurityContext.RunAsUser)
	assert.False(t, *inst.ContainerSecurityContext.RunAsNonRoot)

	// A declared probe goes through the defaulting helper rather than being
	// inherited, so the user's value survives and the rest is filled in.
	assert.Equal(t, int32(42), inst.LivenessProbe.TimeoutSeconds)
	assert.Equal(t, int32(60), inst.LivenessProbe.InitialDelaySeconds,
		"the untouched fields of a declared probe are defaulted, not left at zero")
	require.NotNil(t, inst.LivenessProbe.Exec)

	assert.Equal(t, int32(41), inst.ReadinessProbe.TimeoutSeconds)
	assert.Equal(t, int32(10), inst.ReadinessProbe.InitialDelaySeconds)
	require.NotNil(t, inst.ReadinessProbe.Exec)
}
func TestInstanceSetDefaultsNormalizesVolumeSpec(t *testing.T) {
	cr := testCR(currentCRVersion, TLSModeDisabled)
	rs := defaultedReplset(t)

	t.Run("access modes are filled in", func(t *testing.T) {
		inst := &InstanceSpec{Name: "hot", Replicas: 1, VolumeSpec: testVol("1Gi")}
		require.NoError(t, inst.SetDefaults(cr, rs))

		assert.Equal(t,
			[]corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			inst.VolumeSpec.PersistentVolumeClaim.AccessModes)
	})

	t.Run("an arbiter needs no volume", func(t *testing.T) {
		inst := &InstanceSpec{
			Name: "arb", Replicas: 1,
			RSConfig: &MemberConfigSpec{ArbiterOnly: new(true), Votes: new(int32(1))},
		}
		require.NoError(t, inst.SetDefaults(cr, rs))
		assert.Nil(t, inst.VolumeSpec)
	})

	t.Run("a volume without a storage request is rejected", func(t *testing.T) {
		inst := &InstanceSpec{
			Name: "hot", Replicas: 1,
			VolumeSpec: &VolumeSpec{PersistentVolumeClaim: PVCSpec{
				PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{},
			}},
		}
		err := inst.SetDefaults(cr, rs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "spec.replsets[rs0].instances[hot].volumeSpec")
		assert.Contains(t, err.Error(), "volume.resources.storage can't be empty")
	})
}

func TestInstanceSetDefaultsValidatesName(t *testing.T) {
	cr := testCR(currentCRVersion, TLSModeDisabled)
	rs := defaultedReplset(t)

	tests := map[string]struct {
		name    string
		wantErr string
	}{
		"a lowercase label is fine":   {name: "hot"},
		"digits and dashes are fine":  {name: "hot-2"},
		"uppercase is rejected":       {name: "Hot", wantErr: "instances[Hot].name"},
		"a trailing dash is rejected": {name: "hot-", wantErr: "instances[hot-].name"},
		"underscores are rejected":    {name: "hot_1", wantErr: "instances[hot_1].name"},
		"an empty name is rejected":   {name: "", wantErr: "instances[].name"},
		"nonVoting is exempt": {
			name: ReservedGroupNonVoting,
		},
		"mongod is exempt":  {name: ReservedGroupMongod},
		"hidden is exempt":  {name: ReservedGroupHidden},
		"arbiter is exempt": {name: ReservedGroupArbiter},
	}

	for tn, tt := range tests {
		t.Run(tn, func(t *testing.T) {
			inst := &InstanceSpec{Name: tt.name, Replicas: 1, VolumeSpec: testVol("1Gi")}
			err := inst.SetDefaults(cr, rs)

			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestInstanceSetDefaultsRejectsShortGracePeriod(t *testing.T) {
	cr := testCR(currentCRVersion, TLSModeDisabled)
	rs := defaultedReplset(t)

	inst := &InstanceSpec{
		Name: "hot", Replicas: 1, VolumeSpec: testVol("1Gi"),
		MultiAZ: MultiAZ{TerminationGracePeriodSeconds: new(int64(5))},
	}

	err := inst.SetDefaults(cr, rs)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "spec.replsets[rs0].instances[hot]")
	assert.Contains(t, err.Error(), "terminationGracePeriodSeconds must be at least 30 seconds")

	t.Run("unsafeFlags waives it", func(t *testing.T) {
		cr := testCR(currentCRVersion, TLSModeDisabled)
		cr.Spec.Unsafe.TerminationGracePeriod = true

		inst := &InstanceSpec{
			Name: "hot", Replicas: 1, VolumeSpec: testVol("1Gi"),
			MultiAZ: MultiAZ{TerminationGracePeriodSeconds: new(int64(5))},
		}
		require.NoError(t, inst.SetDefaults(cr, rs))
		assert.Equal(t, int64(5), *inst.TerminationGracePeriodSeconds)
	})
}

func testVol(size string) *VolumeSpec {
	return &VolumeSpec{PersistentVolumeClaim: PVCSpec{
		PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse(size)},
			},
		},
	}}
}
