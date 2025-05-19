package psmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestMongosService(t *testing.T) {
	tests := map[string]struct {
		expose       api.Expose
		podName      string
		expectedSpec corev1.ServiceSpec
	}{
		"ClusterIP": {
			expose: api.Expose{
				ServiceLabels: map[string]string{
					"percona.com/test": "label",
				},
				ServiceAnnotations: map[string]string{
					"percona.com/test": "annotation",
				},
				ExposeType: corev1.ServiceTypeClusterIP,
			},
			podName: "test-cr-mongos-0",
			expectedSpec: corev1.ServiceSpec{
				PublishNotReadyAddresses: false,
				Ports: []corev1.ServicePort{
					{
						Name:       "mongos",
						Port:       27017,
						TargetPort: intstr.FromInt(27017),
					},
				},
				Selector: map[string]string{
					"app.kubernetes.io/component":  "mongos",
					"app.kubernetes.io/instance":   "test-cr",
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				},
				Type: corev1.ServiceTypeClusterIP,
			},
		},
		"NodePort": {
			expose: api.Expose{
				ServiceLabels: map[string]string{
					"percona.com/test": "label",
				},
				ServiceAnnotations: map[string]string{
					"percona.com/test": "annotation",
				},
				ExposeType: corev1.ServiceTypeNodePort,
			},
			podName: "test-cr-mongos-0",
			expectedSpec: corev1.ServiceSpec{
				PublishNotReadyAddresses: false,
				Ports: []corev1.ServicePort{
					{
						Name:       "mongos",
						Port:       27017,
						TargetPort: intstr.FromInt(27017),
					},
				},
				Selector: map[string]string{
					"app.kubernetes.io/component":  "mongos",
					"app.kubernetes.io/instance":   "test-cr",
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				},
				Type:                  corev1.ServiceTypeNodePort,
				ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyCluster,
			},
		},
		"LoadBalancer": {
			expose: api.Expose{
				ServiceLabels: map[string]string{
					"percona.com/test": "label",
				},
				ServiceAnnotations: map[string]string{
					"percona.com/test": "annotation",
				},
				ExposeType:               corev1.ServiceTypeLoadBalancer,
				LoadBalancerClass:        ptr.To("eks.amazonaws.com/nlb"),
				LoadBalancerSourceRanges: []string{"10.0.0.0/16"},
			},
			podName: "test-cr-mongos-0",
			expectedSpec: corev1.ServiceSpec{
				PublishNotReadyAddresses: false,
				Ports: []corev1.ServicePort{
					{
						Name:       "mongos",
						Port:       27017,
						TargetPort: intstr.FromInt(27017),
					},
				},
				Selector: map[string]string{
					"app.kubernetes.io/component":  "mongos",
					"app.kubernetes.io/instance":   "test-cr",
					"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
					"app.kubernetes.io/name":       "percona-server-mongodb",
					"app.kubernetes.io/part-of":    "percona-server-mongodb",
				},
				Type:                     corev1.ServiceTypeLoadBalancer,
				ExternalTrafficPolicy:    corev1.ServiceExternalTrafficPolicyLocal,
				LoadBalancerClass:        ptr.To("eks.amazonaws.com/nlb"),
				LoadBalancerSourceRanges: []string{"10.0.0.0/16"},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr := &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version(),
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
								Expose: tt.expose,
							},
						},
					},
				},
			}
			spec := MongosServiceSpec(cr, tt.podName)
			assert.Equal(t, tt.expectedSpec, spec)
		})
	}
}
