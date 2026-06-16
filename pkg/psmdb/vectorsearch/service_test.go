package vectorsearch

import (
	"testing"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func TestService(t *testing.T) {
	cr := newTestCR()
	rs := newTestRS()

	svc := Service(cr, rs)

	assert.Equal(t, "v1", svc.APIVersion)
	assert.Equal(t, "Service", svc.Kind)
	assert.Equal(t, "psmdb-rs0-search", svc.Name)
	assert.Equal(t, "default", svc.Namespace)

	expectedLabels := map[string]string{
		naming.LabelKubernetesName:      "percona-server-mongodb",
		naming.LabelKubernetesManagedBy: "percona-server-mongodb-operator",
		naming.LabelKubernetesPartOf:    "percona-server-mongodb",
		naming.LabelKubernetesInstance:  "psmdb",
		naming.LabelKubernetesReplset:   "rs0",
		naming.LabelKubernetesComponent: naming.ComponentSearch,
	}
	assert.Equal(t, expectedLabels, svc.Labels)
	assert.Equal(t, expectedLabels, svc.Spec.Selector,
		"selector must match the pod labels written by the StatefulSet so the headless Service resolves to the mongot pod")

	assert.Equal(t, corev1.ClusterIPNone, svc.Spec.ClusterIP,
		"Service must be headless so mongod's gRPC resolver sees the pod IP directly")
	assert.True(t, svc.Spec.PublishNotReadyAddresses,
		"not-ready addresses must be published so mongod can reconnect as soon as the pod has an IP")

	expectedPorts := []corev1.ServicePort{
		{
			Name:       grpcPortName,
			Port:       grpcPort,
			TargetPort: intstr.FromInt32(grpcPort),
			Protocol:   corev1.ProtocolTCP,
		},
		{
			Name:       metricsPortName,
			Port:       metricsPort,
			TargetPort: intstr.FromInt32(metricsPort),
			Protocol:   corev1.ProtocolTCP,
		},
	}
	assert.Equal(t, expectedPorts, svc.Spec.Ports)
}

func TestService_NameDerivesFromClusterAndReplset(t *testing.T) {
	cr := newTestCR()
	cr.Name = "my-cluster"
	cr.Namespace = "my-ns"
	rs := newTestRS()
	rs.Name = "shard-1"

	svc := Service(cr, rs)

	assert.Equal(t, "my-cluster-shard-1-search", svc.Name)
	assert.Equal(t, "my-ns", svc.Namespace)
	assert.Equal(t, "my-cluster", svc.Labels[naming.LabelKubernetesInstance])
	assert.Equal(t, "shard-1", svc.Labels[naming.LabelKubernetesReplset])
}
