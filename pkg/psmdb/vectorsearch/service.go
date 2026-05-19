package vectorsearch

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

// Service returns the headless ClusterIP Service that fronts the
// mongot pod for the given replset (or shard). The Service name
// equals StatefulSetName(cr, rs) — `<cluster>-<rs>-search` — so
// mongod's `mongotHost` setParameter resolves to the pod IP directly,
// preserving long-lived gRPC streams across kube-proxy.
//
// Headless (ClusterIP=None) matches the pattern used for the mongod
// replset Service and is forward-compatible with the future size>1
// L7 LB path: an L7 gateway can pick up the per-pod endpoints without
// a separate per-pod Service.
func Service(cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) *corev1.Service {
	ls := naming.SearchLabels(cr, rs)

	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.SearchServiceName(cr, rs),
			Namespace: cr.Namespace,
			Labels:    ls,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: corev1.ClusterIPNone,
			Selector:  ls,
			Ports: []corev1.ServicePort{
				{
					Name:       GRPCPortName,
					Port:       GRPCPort,
					TargetPort: intstr.FromInt32(GRPCPort),
					Protocol:   corev1.ProtocolTCP,
				},
				{
					Name:       MetricsPortName,
					Port:       MetricsPort,
					TargetPort: intstr.FromInt32(MetricsPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
			// gRPC streams are long-lived and reconnect on pod
			// restart; publishing not-ready addresses lets mongod's
			// resolver pick up the pod IP as soon as it has one,
			// shortening the catch-up window after a restart.
			PublishNotReadyAddresses: true,
		},
	}
}
