package stub

import (
	"github.com/stretchr/testify/assert"
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

func Test_setExposeDefaulf(t *testing.T) {
	type args struct {
		replset  *v1alpha1.ReplsetSpec
		expected *v1alpha1.ReplsetSpec
	}
	tests := map[string]*args{
		"Expose Nil": {
			&v1alpha1.ReplsetSpec{
				Expose: nil,
			},
			&v1alpha1.ReplsetSpec{
				Expose: &v1alpha1.Expose{
					Enabled: false,
				},
			},
		},
		"Expose not nil but ExposeType is nil": {
			&v1alpha1.ReplsetSpec{
				Expose: &v1alpha1.Expose{
					Enabled:    true,
					ExposeType: "",
				},
			},
			&v1alpha1.ReplsetSpec{
				Expose: &v1alpha1.Expose{
					Enabled:    true,
					ExposeType: corev1.ServiceTypeClusterIP,
				},
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			setExposeDefaults(test.replset)
			assert.Equal(t, test.replset, test.expected)
		})
	}
}
