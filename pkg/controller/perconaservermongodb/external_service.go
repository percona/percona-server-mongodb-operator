package perconaservermongodb

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/pkg/errors"
)

func (r *ReconcilePerconaServerMongoDB) ensureExternalServices(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, podList *corev1.PodList) ([]corev1.Service, error) {
	services := make([]corev1.Service, 0)

	for _, pod := range podList.Items {
		service := psmdb.ExternalService(cr, replset, pod.Name)
		err := setControllerReference(cr, service, r.scheme)
		if err != nil {
			return nil, errors.Wrap(err, "set owner ref for Service "+service.Name)
		}

		err = r.createOrUpdate(service)
		if err != nil {
			return nil, errors.Wrap(err, "failed to create external service for replset "+replset.Name)
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
