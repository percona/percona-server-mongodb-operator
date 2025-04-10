package psmdb

import (
	"context"
	"testing"

	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/version"
)

func TestMongosHost(t *testing.T) {
	ctx := context.Background()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mongos-0",
			Namespace: "default",
		},
	}

	tests := map[string]struct {
		init          func(cl client.Client)
		cr            *api.PerconaServerMongoDB
		expectedHost  string
		expectedError error
	}{
		"service not found": {
			init: func(cl client.Client) {},
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					ClusterServiceDNSSuffix: "svc.cluster.local",
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
								ServicePerPod: false,
							},
						},
					},
				},
			},
			expectedHost: "",
		},
		"clusterip service type": {
			init: func(cl client.Client) {
				svc := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster-mongos",
						Namespace: "default",
					},
					Spec: corev1.ServiceSpec{
						Type: corev1.ServiceTypeClusterIP,
						Ports: []corev1.ServicePort{
							{
								Name: "mongos",
								Port: 27018,
							},
							{
								Name: "random",
								Port: 12345,
							},
						},
					},
				}
				assert.NoError(t, cl.Create(ctx, svc))
			},
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					ClusterServiceDNSSuffix: "svc.cluster.local",
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
								ServicePerPod: false,
							},
						},
					},
				},
			},
			expectedHost: "test-cluster-mongos.default.svc.cluster.local:27018",
		},
		"err: clusterip service type and port not found": {
			init: func(cl client.Client) {
				svc := &corev1.Service{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-cluster-mongos",
						Namespace: "default",
					},
					Spec: corev1.ServiceSpec{
						Type: corev1.ServiceTypeClusterIP,
						Ports: []corev1.ServicePort{
							{
								Name: "random",
								Port: 12345,
							},
						},
					},
				}
				assert.NoError(t, cl.Create(ctx, svc))
			},
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster",
					Namespace: "default",
				},
				Spec: api.PerconaServerMongoDBSpec{
					ClusterServiceDNSSuffix: "svc.cluster.local",
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
								ServicePerPod: false,
							},
						},
					},
				},
			},
			expectedError: errors.New("mongos port not found in service"),
		},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			s := scheme.Scheme
			cl := fake.NewClientBuilder().WithScheme(s).Build()
			assert.NotNil(t, cl)
			tt.init(cl)

			host, err := MongosHost(ctx, cl, tt.cr, pod, false)
			if tt.expectedError != nil {
				assert.Empty(t, host)
				assert.EqualError(t, err, tt.expectedError.Error())
				return
			}
			assert.Equal(t, tt.expectedHost, host)
			assert.NoError(t, err)
		})
	}
}

func TestMongosService(t *testing.T) {
	tests := map[string]struct {
		cr           *api.PerconaServerMongoDB
		podName      string
		expectedSpec corev1.ServiceSpec
	}{
		"ClusterIP": {
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version,
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
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
					},
				},
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
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version,
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
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
					},
				},
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
			cr: &api.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cr",
					Namespace: "test-ns",
				},
				Spec: api.PerconaServerMongoDBSpec{
					CRVersion: version.Version,
					Sharding: api.Sharding{
						Mongos: &api.MongosSpec{
							Expose: api.MongosExpose{
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
					},
				},
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
			spec := MongosServiceSpec(tt.cr, tt.podName)
			assert.Equal(t, tt.expectedSpec, spec)
		})
	}

}
