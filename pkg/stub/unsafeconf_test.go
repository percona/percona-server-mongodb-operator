package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/stretchr/testify/assert"
)

func Test_setSafeDefault(t *testing.T) {
	type args struct {
		replset  *v1alpha1.ReplsetSpec
		expected *v1alpha1.ReplsetSpec
	}

	tests := map[string]args{
		"even number": {
			&v1alpha1.ReplsetSpec{
				Size: 4,
			},
			&v1alpha1.ReplsetSpec{
				Size: 5,
			},
		},
		"even number2": {
			&v1alpha1.ReplsetSpec{
				Size: 2,
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
			},
		},
		"0 w/o arbiter ": {
			&v1alpha1.ReplsetSpec{
				Size: 0,
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
			},
		},
		"0 with arbiter": {
			&v1alpha1.ReplsetSpec{
				Size: 0,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"1 w/o arbiter ": {
			&v1alpha1.ReplsetSpec{
				Size: 1,
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
			},
		},
		"1 with arbiter": {
			&v1alpha1.ReplsetSpec{
				Size: 1,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"odd with arbiter": {
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"odd with two arbiters": {
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"odd with three arbiters": {
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: false,
					Size:    0,
				},
			},
		},
		"even with arbiter": {
			&v1alpha1.ReplsetSpec{
				Size: 2,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 2,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even4 with arbiter": {
			&v1alpha1.ReplsetSpec{
				Size: 4,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 4,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with two arbiters": {
			&v1alpha1.ReplsetSpec{
				Size: 2,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    2,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 2,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
		"even with three arbiters": {
			&v1alpha1.ReplsetSpec{
				Size: 2,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    3,
				},
			},
			&v1alpha1.ReplsetSpec{
				Size: 2,
				Arbiter: &v1alpha1.Arbiter{
					Enabled: true,
					Size:    1,
				},
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			setSafeDefault(test.replset)
			assert.Equal(t, test.replset.Size, test.expected.Size)
			if test.replset.Arbiter != nil && test.replset.Arbiter.Enabled {
				assert.Equal(t, test.expected.Arbiter.Size, test.replset.Arbiter.Size)
			}
		})
	}
}
