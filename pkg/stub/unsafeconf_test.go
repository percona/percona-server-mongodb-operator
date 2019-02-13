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
		"pair number": {
			&v1alpha1.ReplsetSpec{
				Size: 4,
			},
			&v1alpha1.ReplsetSpec{
				Size: 5,
			},
		},
		"pair number with arbiter": {
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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			setSafeDefault(test.replset)
			assert.Equal(t, test.replset.Size, test.expected.Size)
			if test.replset.Arbiter.Enabled {
				assert.Equal(t, test.expected.Arbiter.Size, test.replset.Arbiter.Size)
			}
		})
	}
}
