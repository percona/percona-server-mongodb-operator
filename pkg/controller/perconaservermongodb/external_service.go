package perconaservermongodb

import (
	"context"
	"strconv"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
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

func (r *ReconcilePerconaServerMongoDB) exportServices(cr *api.PerconaServerMongoDB) error {
	ls := clusterLabels(cr)

	seList := mcs.ServiceExportList()
	err := r.client.List(context.TODO(),
		seList,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(ls),
		},
	)
	if err != nil {
		return errors.Wrap(err, "get service export list")
	}
	if !cr.Spec.MultiCluster.Enabled {
		for _, se := range seList.Items {
			err = r.client.Delete(context.TODO(), &se)
			if err != nil {
				return errors.Wrap(err, "delete service export "+se.Name)
			}
		}
		return nil
	}

	svcList := &corev1.ServiceList{}
	err = r.client.List(context.TODO(),
		svcList,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(ls),
		},
	)
	if err != nil {
		return errors.Wrap(err, "get service list")
	}

	svcNames := make(map[string]struct{}, len(svcList.Items))
	for _, svc := range svcList.Items {
		se := mcs.ServiceExport(cr, svc.Name, ls)
		err = setControllerReference(cr, se, r.scheme)
		if err != nil {
			return errors.Wrap(err, "set owner ref for serviceexport "+se.Name)
		}
		if err := r.createOrUpdate(se); err != nil {
			return errors.Wrapf(err, "create or update ServiceExport %s", se.Name)
		}
		svcNames[svc.Name] = struct{}{}
	}

	for _, se := range seList.Items {
		if _, ok := svcNames[se.Name]; !ok {
			if err := r.client.Delete(context.TODO(), &se); err != nil {
				return errors.Wrap(err, "delete service export")
			}
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) removeOutdatedServices(cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	if cr.Spec.Pause {
		return nil
	}

	// needed just for labels
	service := psmdb.ExternalService(cr, replset, cr.Name+"-"+replset.Name)

	svcNames := make(map[string]struct{}, replset.Size)
	for i := 0; i < int(replset.Size); i++ {
		svcNames[service.Name+"-"+strconv.Itoa(i)] = struct{}{}
	}

	if replset.NonVoting.Enabled {
		for i := 0; i < int(replset.NonVoting.Size); i++ {
			svcNames[service.Name+"-nv-"+strconv.Itoa(i)] = struct{}{}
		}
	}

	if replset.Arbiter.Enabled {
		for i := 0; i < int(replset.Arbiter.Size); i++ {
			svcNames[service.Name+"-arbiter-"+strconv.Itoa(i)] = struct{}{}
		}
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
		return errors.Wrap(err, "get current services")
	}

	for _, svc := range svcList.Items {
		if _, ok := svcNames[svc.Name]; !ok {
			if err := r.client.Delete(context.TODO(), &svc); err != nil {
				return errors.Wrapf(err, "delete service %s", svc.Name)
			}
		}
	}

	return nil
}
