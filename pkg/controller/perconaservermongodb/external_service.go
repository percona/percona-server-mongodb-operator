package perconaservermongodb

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"

	api "github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/psmdb"
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
					return nil, fmt.Errorf("set owner ref for InternalKey %s: %v", service.Name, err)
				}

				err = r.client.Create(context.TODO(), service)
				if err != nil && !errors.IsAlreadyExists(err) {
					return nil, fmt.Errorf("failed to create external service for replset %s: %v", replset.Name, err)
				}
			} else {
				return nil, fmt.Errorf("failed to fetch service for replset %s: %v", replset.Name, err)
			}
		}

		// logrus.Infof("service %s for pod %s of repleset %s has been found", meta.Name, pod.Name, replset.Name)

		// if err := updateExtService(h.client, meta); err != nil {
		// 	return nil, fmt.Errorf("failed to update external service for replset %s: %v", replset.Name, err)
		// }

		services = append(services, *service)
	}

	return services, nil
}

// !!! WTF? why we need this ?!?!
// func updateExtService(cli sdk.Client, svc *corev1.Service) error {
// 	var retries uint64 = 0

// 	for retries <= 5 {
// 		if err := cli.Update(svc); err != nil {
// 			if errors.IsConflict(err) {
// 				retries += 1
// 				time.Sleep(500 * time.Millisecond)
// 				continue
// 			} else {
// 				return fmt.Errorf("failed to update service: %v", err)
// 			}
// 		}
// 		return nil
// 	}
// 	return fmt.Errorf("failed to update service %s, retries limit reached", svc.Name)
// }
