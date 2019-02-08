package stub

import (
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
)

func Test_setSafeDefault(t *testing.T) {
	type args struct {
		replset  *v1alpha1.ReplsetSpec
		expected *v1alpha1.ReplsetSpec
	}

	tests := map[string]args{
		"Bigger": {
			&v1alpha1.ReplsetSpec{
				Size: 4,
			},
			&v1alpha1.ReplsetSpec{
				Size: 3,
			},
		},
		"Bigger with Arbiter": {
			&v1alpha1.ReplsetSpec{
				Size: 4,
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
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			setSafeDefault(test.replset)
		})
	}
}
