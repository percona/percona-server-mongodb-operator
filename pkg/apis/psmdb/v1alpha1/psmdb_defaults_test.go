package v1alpha1_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

func TestSetSafeDefault(t *testing.T) {
	type args struct {
		replset  *api.ReplsetSpec
		expected *api.ReplsetSpec
	}

	tests := map[string]args{
		"even number": {
			&api.ReplsetSpec{
				Size: 4,
			},
			&api.ReplsetSpec{
				Size: 5,
			},
		},
		"even number2": {
			&api.ReplsetSpec{
				Size: 2,
			},
			&api.ReplsetSpec{
				Size: 3,
			},
		},
		"0 w/o arbiter ": {
			&api.ReplsetSpec{
				Size: 0,
			},
			&api.ReplsetSpec{
				Size: 3,
			},
		},
		"0 with arbiter": {
			&api.ReplsetSpec{
				Size: 0,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"1 w/o arbiter ": {
			&api.ReplsetSpec{
				Size: 1,
			},
			&api.ReplsetSpec{
				Size: 3,
			},
		},
		"1 with arbiter": {
			&api.ReplsetSpec{
				Size: 1,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"odd with arbiter": {
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"odd with two arbiters": {
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"odd with three arbiters": {
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&api.ReplsetSpec{
				Size: 3,
				Arbiter: api.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"even with arbiter": {
			&api.ReplsetSpec{
				Size: 2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&api.ReplsetSpec{
				Size: 2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even4 with arbiter": {
			&api.ReplsetSpec{
				Size: 4,
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
				Size: 2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&api.ReplsetSpec{
				Size: 2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with three arbiters": {
			&api.ReplsetSpec{
				Size: 2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&api.ReplsetSpec{
				Size: 2,
				Arbiter: api.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			test.replset.SetDefauts(false, logf.Log.WithName("TestSetSafeDefault"))
			assert.Equal(t, test.replset.Size, test.expected.Size)
			if test.replset.Arbiter.Enabled {
				assert.Equal(t, test.expected.Arbiter.Size, test.replset.Arbiter.Size)
			}
		})
	}
}
