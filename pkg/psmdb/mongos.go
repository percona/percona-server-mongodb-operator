package psmdb

import (
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Service returns a core/v1 API Service
func MongosService(m *api.PerconaServerMongoDB) *corev1.Service {
	ls := map[string]string{
		"app.kubernetes.io/component": "mongos",
	}

	svc := corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:        m.Name + "-" + "mongos",
			Namespace:   m.Namespace,
			Annotations: m.Spec.Mongos.ServiceAnnotations,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       mongosPortName,
					Port:       m.Spec.Mongos.Port,
					TargetPort: intstr.FromInt(int(m.Spec.Mongos.Port)),
				},
			},
			Selector:                 ls,
			LoadBalancerSourceRanges: m.Spec.Mongos.LoadBalancerSourceRanges,
		},
	}

	if !m.Spec.Mongos.Expose.Enabled {
		svc.Spec.ClusterIP = "None"
	} else {
		switch m.Spec.Mongos.Expose.ExposeType {
		case corev1.ServiceTypeNodePort:
			svc.Spec.Type = corev1.ServiceTypeNodePort
			svc.Spec.ExternalTrafficPolicy = "Local"
		case corev1.ServiceTypeLoadBalancer:
			svc.Spec.Type = corev1.ServiceTypeLoadBalancer
			svc.Spec.ExternalTrafficPolicy = "Cluster"
		default:
			svc.Spec.Type = corev1.ServiceTypeClusterIP
		}
	}

	return &svc
}
