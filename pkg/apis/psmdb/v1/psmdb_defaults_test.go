package v1_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corevs "k8s.io/api/core/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestSetSafeDefaultPre116(t *testing.T) {
	t.Parallel()

	type args struct {
		replset  *api.ReplsetSpec
		expected *api.ReplsetSpec
	}

	vs := &api.VolumeSpec{
		EmptyDir: &corevs.EmptyDirVolumeSource{
			Medium: corevs.StorageMediumDefault,
		},
	}
	tests := map[string]args{
		"even number": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       4,
			},
			&api.ReplsetSpec{
				Size: 5,
			},
		},
		"even number2": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
			},
			&api.ReplsetSpec{
				Size: 3,
			},
		},
		"0 w/o arbiter ": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       0,
			},
			&api.ReplsetSpec{
				Size: 3,
			},
		},
		"0 with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       0,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"1 w/o arbiter ": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       1,
			},
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       3,
			},
		},
		"1 with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       1,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"odd with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"odd with two arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"odd with three arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even4 with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with two arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with three arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&api.ReplsetSpec{
				Size: 4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
	}

	cr := &api.PerconaServerMongoDB{
		Name: "psmdb-mock", Namespace: "psmdb",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.15.0",
			Replsets:  []*api.ReplsetSpec{{Name: "rs0", Size: 3}, {Name: "rs1", Size: 3}},
			Sharding:  api.Sharding{Enabled: true, Mongos: &api.MongosSpec{Size: 3}},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			// TODO: separate testing different platforms, this will not test OpenShift properly
			for _, platform := range []version.Platform{version.PlatformKubernetes, version.PlatformOpenshift} {
				err := test.replset.SetDefaults(platform, cr, logf.Log.WithName("TestSetSafeDefault"))
				if err != nil {
					t.Fatal(err)
				}
				assert.Equal(t, test.expected.Size, test.replset.Size)
				if test.replset.Arbiter.Enabled {
					assert.Equal(t, test.expected.Arbiter.Size, test.replset.Arbiter.Size)
				}
			}
		})
	}
}

func TestSetSafeDefault(t *testing.T) {
	t.Parallel()

	type args struct {
		replset     *api.ReplsetSpec
		expectedErr string
	}

	vs := &api.VolumeSpec{
		EmptyDir: &corevs.EmptyDirVolumeSource{
			Medium: corevs.StorageMediumDefault,
		},
	}
	tests := map[string]args{
		"even number": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       4,
			},
			"check safe defaults: replset size must be odd. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even number2": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
			},
			"check safe defaults: replset size must be odd. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"0 w/o arbiter ": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       0,
			},
			"check safe defaults: replset size must be at least 3. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"0 with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       0,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"1 w/o arbiter ": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       1,
			},
			"check safe defaults: replset size must be at least 3. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"1 with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       1,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"odd with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"odd with two arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"odd with three arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even4 with arbiter": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even with two arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even with three arbiters": {
			&api.ReplsetSpec{
				VolumeSpec: vs,
				Size:       2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
	}

	cr := &api.PerconaServerMongoDB{
		Name: "psmdb-mock", Namespace: "psmdb",
		Spec: api.PerconaServerMongoDBSpec{
			CRVersion: "1.16.0",
			Replsets:  []*api.ReplsetSpec{{Name: "rs0", Size: 3}, {Name: "rs1", Size: 3}},
			Sharding:  api.Sharding{Enabled: true, Mongos: &api.MongosSpec{Size: 3}},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			for _, platform := range []version.Platform{version.PlatformKubernetes, version.PlatformOpenshift} {
				err := test.replset.SetDefaults(platform, cr, logf.Log.WithName("TestSetSafeDefault"))
				if err == nil {
					t.Fatalf("expected error: %v, got nil", test.expectedErr)
				}

				assert.EqualError(t, err, test.expectedErr)
			}
		})
	}
}

func TestStartupProbeDefaults(t *testing.T) {
	startupCommand := []string{"/opt/percona/mongodb-healthcheck", "k8s", "startup", "--component", "mongod"}
	tlsStartupCommand := append(slices.Clone(startupCommand),
		"--ssl", "--sslInsecure",
		"--sslCAFile", "/etc/mongodb-ssl/ca.crt",
		"--sslPEMKeyFile", "/tmp/tls.pem")

	tests := map[string]struct {
		modify          func(cr *api.PerconaServerMongoDB)
		wantNil         bool
		wantRS          int32
		wantNonVoting   int32
		wantHidden      int32
		wantRSPeriod    int32
		wantCfgFailures int32
		wantCommand     []string
		wantExec        []string
	}{
		"not set before 1.24.0": {
			modify:  func(cr *api.PerconaServerMongoDB) { cr.Spec.CRVersion = "1.23.0" },
			wantNil: true,
		},
		"defaults to 24h": {
			modify:          func(cr *api.PerconaServerMongoDB) {},
			wantRS:          8640,
			wantNonVoting:   8640,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
		},
		"replset override is inherited by nonvoting and hidden": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].StartupProbe = &corevs.Probe{FailureThreshold: 100}
			},
			wantRS:          100,
			wantNonVoting:   100,
			wantHidden:      100,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
		},
		"nonvoting override doesn't change replset": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].NonVoting.StartupProbe = &corevs.Probe{FailureThreshold: 50, PeriodSeconds: 30}
			},
			wantRS:          8640,
			wantNonVoting:   50,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
		},
		"no TLS flags when TLS is disabled": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.TLS = &api.TLSSpec{Mode: api.TLSModeDisabled}
				cr.Spec.Unsafe.TLS = true
			},
			wantRS:          8640,
			wantNonVoting:   8640,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
			wantCommand:     startupCommand,
		},
		"user handler is kept": {
			modify: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].StartupProbe = &corevs.Probe{
					Exec: &corevs.ExecAction{Command: []string{"/bin/true"}},
				}
			},
			wantRS:          8640,
			wantNonVoting:   8640,
			wantHidden:      8640,
			wantRSPeriod:    10,
			wantCfgFailures: 8640,
			wantExec:        []string{"/bin/true"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr, err := readDefaultCR()
			require.NoError(t, err)
			cr.Spec.Replsets[0].NonVoting.Enabled = true
			cr.Spec.Replsets[0].Hidden.Enabled = true
			tt.modify(cr)
			require.NoError(t, cr.CheckNSetDefaults(t.Context(), version.PlatformKubernetes))

			rs := cr.Spec.Replsets[0]
			cfg := cr.Spec.Sharding.ConfigsvrReplSet
			if tt.wantNil {
				require.Nil(t, rs.StartupProbe)
				require.Nil(t, rs.NonVoting.StartupProbe)
				require.Nil(t, rs.Hidden.StartupProbe)
				require.Nil(t, cfg.StartupProbe)
				return
			}

			require.Equal(t, tt.wantRS, rs.StartupProbe.FailureThreshold)
			require.Equal(t, tt.wantRSPeriod, rs.StartupProbe.PeriodSeconds)
			require.Equal(t, tt.wantNonVoting, rs.NonVoting.StartupProbe.FailureThreshold)
			require.Equal(t, tt.wantHidden, rs.Hidden.StartupProbe.FailureThreshold)
			require.Equal(t, tt.wantCfgFailures, cfg.StartupProbe.FailureThreshold)
			wantCommand := tlsStartupCommand
			if tt.wantCommand != nil {
				wantCommand = tt.wantCommand
			}
			for _, p := range []*corevs.Probe{rs.StartupProbe, rs.NonVoting.StartupProbe, rs.Hidden.StartupProbe} {
				if tt.wantExec != nil {
					require.Equal(t, tt.wantExec, p.Exec.Command)
					continue
				}
				require.Equal(t, wantCommand, p.Exec.Command)
			}
			require.Equal(t, wantCommand, cfg.StartupProbe.Exec.Command)
		})
	}
}

func readDefaultCR() (*api.PerconaServerMongoDB, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "..", "deploy", "cr.yaml"))
	if err != nil {
		return nil, err
	}

	cr := new(api.PerconaServerMongoDB)
	if err := yaml.Unmarshal(data, cr); err != nil {
		return nil, err
	}
	return cr, nil
}
