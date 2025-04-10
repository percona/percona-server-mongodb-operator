package psmdb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/version"
)

func TestExternalService(t *testing.T) {
	tests := map[string]struct {
		cr          *api.PerconaServerMongoDB
		replset     *api.ReplsetSpec
		podName     string
		expectedSvc *corev1.Service
	}{
		"ClusterIP": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version,
				},
			},
			replset: &api.ReplsetSpec{
				Name: "rs0",
				Expose: api.ExposeTogglable{
					Enabled: true,
					Expose: api.Expose{
						ServiceLabels: map[string]string{
							"percona.com/test": "label",
						},
						ServiceAnnotations: map[string]string{
							"percona.com/test": "annotation",
						},
						ExposeType: corev1.ServiceTypeClusterIP,
					},
				},
			},
			podName: "test-cr-rs0-0",
			expectedSvc: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Service",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr-rs0-0",
					Namespace: "test-ns",
					Labels: map[string]string{
						"app.kubernetes.io/component":  "external-service",
						"app.kubernetes.io/instance":   "test-cr",
						"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
						"app.kubernetes.io/name":       "percona-server-mongodb",
						"app.kubernetes.io/part-of":    "percona-server-mongodb",
						"app.kubernetes.io/replset":    "rs0",
						"percona.com/test":             "label",
					},
					Annotations: map[string]string{
						"percona.com/test": "annotation",
					},
				},
				Spec: corev1.ServiceSpec{
					PublishNotReadyAddresses: true,
					Ports: []corev1.ServicePort{
						{
							Name:       "mongodb",
							Port:       27017,
							TargetPort: intstr.FromInt(27017),
						},
					},
					Selector: map[string]string{"statefulset.kubernetes.io/pod-name": "test-cr-rs0-0"},
					Type:     corev1.ServiceTypeClusterIP,
				},
			},
		},
		"NodePort": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version,
				},
			},
			replset: &api.ReplsetSpec{
				Name: "rs0",
				Expose: api.ExposeTogglable{
					Enabled: true,
					Expose: api.Expose{
						ServiceLabels: map[string]string{
							"percona.com/test": "label",
						},
						ServiceAnnotations: map[string]string{
							"percona.com/test": "annotation",
						},
						ExposeType: corev1.ServiceTypeNodePort,
					},
				},
			},
			podName: "test-cr-rs0-0",
			expectedSvc: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Service",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr-rs0-0",
					Namespace: "test-ns",
					Labels: map[string]string{
						"app.kubernetes.io/component":  "external-service",
						"app.kubernetes.io/instance":   "test-cr",
						"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
						"app.kubernetes.io/name":       "percona-server-mongodb",
						"app.kubernetes.io/part-of":    "percona-server-mongodb",
						"app.kubernetes.io/replset":    "rs0",
						"percona.com/test":             "label",
					},
					Annotations: map[string]string{
						"percona.com/test": "annotation",
					},
				},
				Spec: corev1.ServiceSpec{
					PublishNotReadyAddresses: true,
					Ports: []corev1.ServicePort{
						{
							Name:       "mongodb",
							Port:       27017,
							TargetPort: intstr.FromInt(27017),
						},
					},
					Selector:              map[string]string{"statefulset.kubernetes.io/pod-name": "test-cr-rs0-0"},
					Type:                  corev1.ServiceTypeNodePort,
					ExternalTrafficPolicy: corev1.ServiceExternalTrafficPolicyCluster,
				},
			},
		},
		"LoadBalancer": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version,
				},
			},
			replset: &api.ReplsetSpec{
				Name: "rs0",
				Expose: api.ExposeTogglable{
					Enabled: true,
					Expose: api.Expose{
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
				},
			},
			podName: "test-cr-rs0-0",
			expectedSvc: &corev1.Service{
				TypeMeta: metav1.TypeMeta{
					Kind:       "Service",
					APIVersion: "v1",
				},
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr-rs0-0",
					Namespace: "test-ns",
					Labels: map[string]string{
						"app.kubernetes.io/component":  "external-service",
						"app.kubernetes.io/instance":   "test-cr",
						"app.kubernetes.io/managed-by": "percona-server-mongodb-operator",
						"app.kubernetes.io/name":       "percona-server-mongodb",
						"app.kubernetes.io/part-of":    "percona-server-mongodb",
						"app.kubernetes.io/replset":    "rs0",
						"percona.com/test":             "label",
					},
					Annotations: map[string]string{
						"percona.com/test": "annotation",
					},
				},
				Spec: corev1.ServiceSpec{
					PublishNotReadyAddresses: true,
					Ports: []corev1.ServicePort{
						{
							Name:       "mongodb",
							Port:       27017,
							TargetPort: intstr.FromInt(27017),
						},
					},
					Selector:                 map[string]string{"statefulset.kubernetes.io/pod-name": "test-cr-rs0-0"},
					Type:                     corev1.ServiceTypeLoadBalancer,
					ExternalTrafficPolicy:    corev1.ServiceExternalTrafficPolicyLocal,
					LoadBalancerClass:        ptr.To("eks.amazonaws.com/nlb"),
					LoadBalancerSourceRanges: []string{"10.0.0.0/16"},
				},
			},
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			svc := ExternalService(tt.cr, tt.replset, tt.podName)
			assert.Equal(t, tt.expectedSvc, svc)
		})
	}

}
