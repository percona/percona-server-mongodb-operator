package stub

import (
	"github.com/stretchr/testify/assert"
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

func Test_arbiterRightsizing(t *testing.T) {
	type args struct {
		replset     *v1alpha1.ReplsetSpec
		arbiterSize int32
	}

	tests := map[string]*args{
		"arbiter is nil": {&v1alpha1.ReplsetSpec{
			Size:    3,
			Arbiter: nil,
		}, 0},

		"arbiter size not set": {&v1alpha1.ReplsetSpec{
			Size: 3,
			Arbiter: &v1alpha1.Arbiter{
				Enabled: true,
				Size:    0,
			},
		}, 1},

		"arbiter size equal replset size": {&v1alpha1.ReplsetSpec{
			Size: 3,
			Arbiter: &v1alpha1.Arbiter{
				Enabled: true,
				Size:    3,
			},
		}, 1},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			arbiterRightsizing(test.replset)
			assert.Equal(t, test.replset.Arbiter.Size, test.arbiterSize)
		})
	}
}
