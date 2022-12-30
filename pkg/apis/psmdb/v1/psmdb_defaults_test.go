package v1_test

import (
	"testing"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/version"
	"github.com/stretchr/testify/assert"
	corevs "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestSetSafeDefault(t *testing.T) {
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
				VolumeSpec: vs,
				Size:       4,
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
				VolumeSpec: vs,
				Size:       4,
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
				VolumeSpec: vs,
				Size:       4,
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
				VolumeSpec: vs,
				Size:       4,
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
				VolumeSpec: vs,
				Size:       4,
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
				VolumeSpec: vs,
				Size:       4,
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
				VolumeSpec: vs,
				Size:       4,
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
				VolumeSpec: vs,
				Size:       4,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
	}

	cr := &api.PerconaServerMongoDB{
		ObjectMeta: metav1.ObjectMeta{Name: "psmdb-mock", Namespace: "psmdb"},
		Spec: api.PerconaServerMongoDBSpec{
			Replsets: []*api.ReplsetSpec{{Name: "rs0", Size: 3}, {Name: "rs1", Size: 3}},
			Sharding: api.Sharding{Enabled: true, Mongos: &api.MongosSpec{Size: 3}},
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
