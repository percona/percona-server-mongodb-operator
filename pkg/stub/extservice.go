package stub

import (
	"fmt"
	sdk "github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	opsSdk "github.com/operator-framework/operator-sdk/pkg/sdk"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

func (h *Handler) ensureExtServices(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) error {
	podsList := podList()
	if err := h.client.List(m.Namespace, podsList, opsSdk.WithListOptions(getLabelSelectorListOpts(m, replset))); err != nil {
		return fmt.Errorf("failed to list pods for replset %s: %v", replset.Name, err)
	}

	for _, pod := range podsList.Items {
		svcMeta := serviceMeta(m.Namespace, pod.Name)
		if err := h.client.Get(svcMeta); err != nil {
			if !errors.IsNotFound(err) {
				return fmt.Errorf("failed to fetch service for replset %s: %v", replset.Name, err)
			}
		}
		svc := extService(m, pod.Name)
		if err := createExtService(h.client, svc); err != nil {
			return fmt.Errorf("failed to create external service for replset %s: %v", replset.Name, err)
		}
	}

	return nil
}

func createExtService(cli sdk.Client, svc *corev1.Service) error {
	if err := cli.Create(svc); err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

func serviceMeta(namespace, podName string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Service",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: namespace,
		},
	}
}

func extService(m *v1alpha1.PerconaServerMongoDB, podName string) *corev1.Service {
	return &corev1.Service{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "v1",
			Kind:       "Service",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: m.Namespace,
		},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{
				{
					Name:       mongodPortName,
					Port:       m.Spec.Mongod.Net.Port,
					TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
				},
			},
			Selector:              map[string]string{"statefulset.kubernetes.io/pod-name": podName},
			ExternalTrafficPolicy: "Local",
			Type:                  "LoadBalancer",
		},
	}
}
