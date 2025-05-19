package logcollector

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestContainers(t *testing.T) {
	tests := map[string]struct {
		logCollector           *api.LogCollectorSpec
		expectedContainerNames []string
	}{
		"nil logcollector": {},
		"logcollector disabled": {
			logCollector: &api.LogCollectorSpec{
				Enabled: false,
			},
		},
		"logcollector enabled": {
			logCollector: &api.LogCollectorSpec{
				Enabled:         true,
				Image:           "test-image",
				ImagePullPolicy: corev1.PullIfNotPresent,
			},
			expectedContainerNames: []string{"logs", "logrotate"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "my-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					LogCollector: tt.logCollector,
				},
			}

			containers, err := Containers(cr)
			assert.NoError(t, err)

			var gotNames []string
			for _, c := range containers {
				gotNames = append(gotNames, c.Name)
			}
			assert.Equal(t, tt.expectedContainerNames, gotNames)
		})
	}
}
