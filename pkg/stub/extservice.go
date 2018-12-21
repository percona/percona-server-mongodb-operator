package stub

import (
	"fmt"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/mongod"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/sdk"
	"github.com/Percona-Lab/percona-server-mongodb-operator/internal/util"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	opsSdk "github.com/operator-framework/operator-sdk/pkg/sdk"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sync/atomic"
	"time"
)

func (h *Handler) ensureExtServices(m *v1alpha1.PerconaServerMongoDB, replset *v1alpha1.ReplsetSpec) ([]corev1.Service, error) {
	podsList := util.PodList()
	if err := h.client.List(m.Namespace, podsList, opsSdk.WithListOptions(util.GetLabelSelectorListOpts(m, replset))); err != nil {
		return nil, fmt.Errorf("failed to list pods for replset %s: %v", replset.Name, err)
	}

	services := make([]corev1.Service, 0)
	for _, pod := range podsList.Items {
		svcMeta := serviceMeta(m.Namespace, pod.Name)
		if err := h.client.Get(svcMeta); err != nil {
			if !errors.IsNotFound(err) {
				return nil, fmt.Errorf("failed to fetch service for replset %s: %v", replset.Name, err)
			}
		}
		svc := extService(m, pod.Name)
		if err := createExtService(h.client, svc); err != nil {
			return nil, fmt.Errorf("failed to create external service for replset %s: %v", replset.Name, err)
		}

		if err := updateExtService(h.client, svc); err != nil {
			return nil, fmt.Errorf("failed to update external service for replset %s: %v", replset.Name, err)
		}
		services = append(services, *svc)
	}

	return services, nil
}

func createExtService(cli sdk.Client, svc *corev1.Service) error {
	if err := cli.Create(svc); err != nil {
		if !errors.IsAlreadyExists(err) {
			return err
		}
	}
	return nil
}

func updateExtService(cli sdk.Client, svc *corev1.Service) error {
	var retries uint64 = 0

	for atomic.LoadUint64(&retries) <= 5 {
		if err := cli.Update(svc); err != nil {
			if errors.IsConflict(err) {
				time.Sleep(500 * time.Millisecond)
				atomic.AddUint64(&retries, 1)
				continue
			}
		}
		return nil
	}
	return fmt.Errorf("failed to update service %s, retries limit reached", svc.Name)
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
	svc := &corev1.Service{
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
					Name:       mongod.MongodPortName,
					Port:       m.Spec.Mongod.Net.Port,
					TargetPort: intstr.FromInt(int(m.Spec.Mongod.Net.Port)),
				},
			},
			Selector: map[string]string{"statefulset.kubernetes.io/pod-name": podName},
		},
	}
	switch m.Spec.Expose.ExposeType {
	case corev1.ServiceTypeNodePort:
		svc.Spec.Type = corev1.ServiceTypeNodePort
		svc.Spec.ExternalTrafficPolicy = "Local"
	case corev1.ServiceTypeLoadBalancer:
		svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		svc.Spec.ExternalTrafficPolicy = "Local"
	default:
		svc.Spec.Type = corev1.ServiceTypeClusterIP
	}
	util.AddOwnerRefToObject(svc, util.AsOwner(m))
	return svc
}
