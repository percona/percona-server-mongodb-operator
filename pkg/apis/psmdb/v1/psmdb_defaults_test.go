package v1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corevs "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestSetSafeDefaultPre116(t *testing.T) {
	type args struct {
		replset  *ReplsetSpec
		expected *ReplsetSpec
	}

	vs := &VolumeSpec{
		EmptyDir: &corevs.EmptyDirVolumeSource{
			Medium: corevs.StorageMediumDefault,
		},
	}
	tests := map[string]args{
		"even number": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(4)),
			},
			&ReplsetSpec{
				Size: new(int32(5)),
			},
		},
		"even number2": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
			},
			&ReplsetSpec{
				Size: new(int32(3)),
			},
		},
		"0 w/o arbiter ": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(0)),
			},
			&ReplsetSpec{
				Size: new(int32(3)),
			},
		},
		"0 with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(0)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"1 w/o arbiter ": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(1)),
			},
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(3)),
			},
		},
		"1 with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(1)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"odd with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(3)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"odd with two arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(3)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"odd with three arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(3)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even4 with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with two arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with three arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&ReplsetSpec{
				Size: new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
	}

	cr := &PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: PerconaServerMongoDBSpec{
			CRVersion: "1.15.0",
			Replsets:  []*ReplsetSpec{{Name: "rs0", Size: new(int32(3))}, {Name: "rs1", Size: new(int32(3))}},
			Sharding:  Sharding{Enabled: true, Mongos: &MongosSpec{Size: 3}},
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
	type args struct {
		replset     *ReplsetSpec
		expectedErr string
	}

	vs := &VolumeSpec{
		EmptyDir: &corevs.EmptyDirVolumeSource{
			Medium: corevs.StorageMediumDefault,
		},
	}
	tests := map[string]args{
		"even number": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(4)),
			},
			"check safe defaults: replset size must be odd. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even number2": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
			},
			"check safe defaults: replset size must be odd. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"0 w/o arbiter ": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(0)),
			},
			"check safe defaults: replset size must be at least 3. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"0 with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(0)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"1 w/o arbiter ": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(1)),
			},
			"check safe defaults: replset size must be at least 3. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"1 with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(1)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"odd with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(3)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"odd with two arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(3)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"odd with three arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(3)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			"check safe defaults: replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even4 with arbiter": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(4)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even with two arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"even with three arbiters": {
			&ReplsetSpec{
				VolumeSpec: vs,
				Size:       new(int32(2)),
				Arbiter: Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			"check safe defaults: arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
	}

	cr := &PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: PerconaServerMongoDBSpec{
			CRVersion: "1.16.0",
			Replsets:  []*ReplsetSpec{{Name: "rs0", Size: new(int32(3))}, {Name: "rs1", Size: new(int32(3))}},
			Sharding:  Sharding{Enabled: true, Mongos: &MongosSpec{Size: 3}},
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

func TestCheckSafeDefaults(t *testing.T) {
	tlsModeConf := MongoConfiguration(`net:
  tls:
    mode: requireTLS`)

	tests := map[string]struct {
		rs          *ReplsetSpec
		unsafe      UnsafeFlags
		expectedErr string
	}{
		// legacy topology, no arbiter
		"odd size": {
			rs: &ReplsetSpec{Size: new(int32(3))},
		},
		"even size": {
			rs:          &ReplsetSpec{Size: new(int32(4))},
			expectedErr: "replset size must be odd. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"size 1": {
			rs:          &ReplsetSpec{Size: new(int32(1))},
			expectedErr: "replset size must be at least 3. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"size 0": {
			rs:          &ReplsetSpec{Size: new(int32(0))},
			expectedErr: "replset size must be at least 3. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},

		// legacy topology, arbiter enabled
		"even size with arbiter": {
			rs: &ReplsetSpec{Size: new(int32(4)), Arbiter: Arbiter{Enabled: true, Size: 1}},
		},
		"odd size with arbiter": {
			rs:          &ReplsetSpec{Size: new(int32(5)), Arbiter: Arbiter{Enabled: true, Size: 1}},
			expectedErr: "arbiter must disabled due to odd replset size. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"size below minimum with arbiter": {
			rs:          &ReplsetSpec{Size: new(int32(2)), Arbiter: Arbiter{Enabled: true, Size: 1}},
			expectedErr: "replset size must be at least 4 with arbiter. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"more than one arbiter": {
			rs:          &ReplsetSpec{Size: new(int32(4)), Arbiter: Arbiter{Enabled: true, Size: 2}},
			expectedErr: "arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"arbiter enabled with size 0": {
			rs:          &ReplsetSpec{Size: new(int32(4)), Arbiter: Arbiter{Enabled: true, Size: 0}},
			expectedErr: "arbiter size must be 1. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},

		// unsafeFlags.replsetSize
		"unsafe replset size skips size check": {
			rs:     &ReplsetSpec{Size: new(int32(0))},
			unsafe: UnsafeFlags{ReplsetSize: true},
		},
		"unsafe replset size skips arbiter checks": {
			rs:     &ReplsetSpec{Size: new(int32(2)), Arbiter: Arbiter{Enabled: true, Size: 3}},
			unsafe: UnsafeFlags{ReplsetSize: true},
		},

		// tls mode
		"tls mode in configuration": {
			rs:          &ReplsetSpec{Size: new(int32(3)), Configuration: tlsModeConf},
			expectedErr: "tlsMode must be set using spec.tls.mode",
		},
		"tls mode is checked even with unsafe replset size": {
			rs:          &ReplsetSpec{Size: new(int32(0)), Configuration: tlsModeConf},
			unsafe:      UnsafeFlags{ReplsetSize: true},
			expectedErr: "tlsMode must be set using spec.tls.mode",
		},
		"size check runs before tls mode check": {
			rs:          &ReplsetSpec{Size: new(int32(4)), Configuration: tlsModeConf},
			expectedErr: "replset size must be odd. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},
		"invalid tls configuration": {
			rs: &ReplsetSpec{Size: new(int32(3)), Configuration: `net:
  tls: requireTLS`},
			expectedErr: "get tls mode: tls configuration is invalid",
		},
		"configuration without tls mode": {
			rs: &ReplsetSpec{Size: new(int32(3)), Configuration: `net:
  port: 27017`},
		},

		// instances[] topology: checkSafeInstanceDefaults takes over
		"instance mode ignores replset size": {
			rs: &ReplsetSpec{
				Size:      new(int32(4)),
				Instances: []InstanceSpec{{Name: ReservedGroupMongod, Replicas: 3}},
			},
		},
		"instance mode rejects even voter count": {
			rs: &ReplsetSpec{
				Instances: []InstanceSpec{{Name: ReservedGroupMongod, Replicas: 4, RSConfig: &MemberConfigSpec{}}},
			},
			expectedErr: "the number of voting members must be odd, got 4. Set spec.unsafeFlags.replsetSize to true to disable this check",
		},

		"instance mode with more than 7 voters (single instance)": {
			rs: &ReplsetSpec{
				Instances: []InstanceSpec{{Name: ReservedGroupMongod, Replicas: 9, RSConfig: &MemberConfigSpec{}}},
			},
			expectedErr: "a replica set supports at most 7 voting members, got 9",
		},
		"instance mode with more than 7 voters (two instances)": {
			rs: &ReplsetSpec{
				Instances: []InstanceSpec{
					{Name: "inst1", Replicas: 3, RSConfig: &MemberConfigSpec{}},
					{Name: "inst2", Replicas: 5, RSConfig: &MemberConfigSpec{}},
				},
			},
			expectedErr: "a replica set supports at most 7 voting members, got 8",
		},
		"instance mode with unsafe replset size skips voter parity": {
			rs: &ReplsetSpec{
				Instances: []InstanceSpec{{Name: ReservedGroupMongod, Replicas: 4, RSConfig: &MemberConfigSpec{}}},
			},
			unsafe: UnsafeFlags{ReplsetSize: true},
		},
		"instance mode checks tls mode": {
			rs: &ReplsetSpec{
				Configuration: tlsModeConf,
				Instances:     []InstanceSpec{{Name: ReservedGroupMongod, Replicas: 3}},
			},
			expectedErr: "tlsMode must be set using spec.tls.mode",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			err := tt.rs.checkSafeDefaults(tt.unsafe)
			if tt.expectedErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tt.expectedErr)
		})
	}
}
