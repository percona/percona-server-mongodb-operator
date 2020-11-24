package perconaservermongodb

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
)

func (r *ReconcilePerconaServerMongoDB) ensureExternalServices(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, podList *corev1.PodList) ([]corev1.Service, error) {
	services := make([]corev1.Service, 0)

	for _, pod := range podList.Items {
		service := &corev1.Service{}
		if err := r.client.Get(context.TODO(), types.NamespacedName{Name: pod.Name, Namespace: cr.Namespace}, service); err != nil {
			if errors.IsNotFound(err) {
				service = psmdb.ExternalService(cr, replset, pod.Name)
				err = setControllerReference(cr, service, r.scheme)
				if err != nil {
					return nil, fmt.Errorf("set owner ref for Service %s: %v", service.Name, err)
				}

				err = r.client.Create(context.TODO(), service)
				if err != nil && !errors.IsAlreadyExists(err) {
					return nil, fmt.Errorf("failed to create external service for replset %s: %v", replset.Name, err)
				}
			} else {
				return nil, fmt.Errorf("failed to fetch service for replset %s: %v", replset.Name, err)
			}
		}

		services = append(services, *service)
	}

	return services, nil
}

func (r *ReconcilePerconaServerMongoDB) removeOudatedServices(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec,
	podList *corev1.PodList) error {

	if len(podList.Items) == 0 {
		return nil
	}

	podNames := make(map[string]struct{}, len(podList.Items))

	// needed just for labels
	service := psmdb.ExternalService(cr, replset, podList.Items[0].Name)

	for _, pod := range podList.Items {
		podNames[pod.Name] = struct{}{}
	}

	// clear old services
	svcList := &corev1.ServiceList{}
	err := r.client.List(context.TODO(),
		svcList,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(service.Labels),
		},
	)
	if err != nil {
		return fmt.Errorf("get current services: %v", err)
	}

	for _, svc := range svcList.Items {
		if _, ok := podNames[svc.Name]; !ok {
			err := r.client.Delete(context.TODO(), &svc)
			if err != nil {
				return fmt.Errorf("delete service %s: %v", svc.Name, err)
			}
		}
	}

	return nil
}
