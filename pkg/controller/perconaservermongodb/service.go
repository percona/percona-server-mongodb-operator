package perconaservermongodb

import (
	"context"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/mcs"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

func (r *ReconcilePerconaServerMongoDB) reconcileServices(ctx context.Context, cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) error {
	if err := r.reconcileReplsetServices(ctx, cr, repls); err != nil {
		return errors.Wrap(err, "reconcile replset services")
	}

	if err := r.reconcileMongosSvc(ctx, cr); err != nil {
		return errors.Wrap(err, "reconcile mongos services")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileReplsetServices(ctx context.Context, cr *api.PerconaServerMongoDB, repls []*api.ReplsetSpec) error {
	for _, rs := range repls {
		// Create headless service
		service := psmdb.Service(cr, rs)
		if err := setControllerReference(cr, service, r.scheme); err != nil {
			return errors.Wrapf(err, "set owner ref for service %s", service.Name)
		}
		if err := r.createOrUpdateSvc(ctx, cr, service, true); err != nil {
			return errors.Wrapf(err, "create or update service for replset %s", rs.Name)
		}
		if err := r.removeOutdatedServices(ctx, cr, rs); err != nil {
			return errors.Wrapf(err, "failed to remove old services of replset %s", rs.Name)
		}
		if !rs.Expose.Enabled {
			continue
		}
		// Create exposed services
		pods, err := psmdb.GetRSPods(ctx, r.client, cr, rs.Name)
		if err != nil {
			return errors.Wrapf(err, "get pods list for replset %s", rs.Name)
		}
		if err := r.ensureExternalServices(ctx, cr, rs, &pods); err != nil {
			return errors.Wrap(err, "ensure external services")
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileMongosSvc(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Sharding.Enabled {
		return nil
	}

	if cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
		for i := 0; i < int(cr.Spec.Sharding.Mongos.Size); i++ {
			err := r.createOrUpdateMongosSvc(ctx, cr, naming.MongosPerPodServiceName(cr, i))
			if err != nil {
				return errors.Wrap(err, "create or update mongos service")
			}
		}
	} else {
		err := r.createOrUpdateMongosSvc(ctx, cr, naming.MongosServiceName(cr))
		if err != nil {
			return errors.Wrap(err, "create or update mongos service")
		}
	}

	err := r.removeOutdatedMongosSvc(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "remove outdated mongos services")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) ensureExternalServices(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec, podList *corev1.PodList) error {
	for _, pod := range podList.Items {
		service := psmdb.ExternalService(cr, replset, pod.Name)
		err := setControllerReference(cr, service, r.scheme)
		if err != nil {
			return errors.Wrapf(err, "set owner ref for Service %s", service.Name)
		}

		err = r.createOrUpdateSvc(ctx, cr, service, replset.Expose.SaveOldMeta())
		if err != nil {
			return errors.Wrapf(err, "failed to create external service for replset %s", replset.Name)
		}

		if cr.Spec.MultiCluster.Enabled {
			err = r.exportService(ctx, cr, service)
			if err != nil {
				return errors.Wrapf(err, "failed to export service %s", service.Name)
			}
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) exportService(ctx context.Context, cr *api.PerconaServerMongoDB, svc *corev1.Service) error {
	ls := naming.ClusterLabels(cr)
	if !cr.Spec.MultiCluster.Enabled {
		return nil
	}
	se := mcs.ServiceExport(cr.Namespace, svc.Name, ls)
	if err := setControllerReference(cr, se, r.scheme); err != nil {
		return errors.Wrapf(err, "set owner ref for serviceexport %s", se.Name)
	}
	if err := r.createOrUpdate(ctx, se); err != nil {
		return errors.Wrapf(err, "create or update ServiceExport %s", se.Name)
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) exportServices(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.MCSEnabled() {
		return nil
	}

	ls := naming.ClusterLabels(cr)

	seList := mcs.ServiceExportList()
	err := r.client.List(
		ctx,
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
			err = r.client.Delete(ctx, &se)
			if err != nil {
				return errors.Wrapf(err, "delete service export %s", se.Name)
			}
		}
		return nil
	}

	svcList := &corev1.ServiceList{}
	err = r.client.List(
		ctx,
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
		if err := r.exportService(ctx, cr, &svc); err != nil {
			return errors.Wrapf(err, "export service %s", svc.Name)
		}
		svcNames[svc.Name] = struct{}{}
	}

	for _, se := range seList.Items {
		if _, ok := svcNames[se.Name]; !ok {
			if err := r.client.Delete(ctx, &se); err != nil && !k8serrors.IsNotFound(err) {
				return errors.Wrap(err, "delete service export")
			}
		}
	}
	return nil
}

// expectedExternalServiceNames returns the per-pod service names the replica
// set's groups require. A per-pod service is named after its pod, so this is
// exactly the union of every group's desired pod names.
func (r *ReconcilePerconaServerMongoDB) expectedExternalServiceNames(
	cr *api.PerconaServerMongoDB,
	rs *api.ReplsetSpec,
	set *membergroup.Set,
) map[string]struct{} {
	names := make(map[string]struct{}, set.GetTotalMemberCount())
	if !rs.Expose.Enabled {
		return names
	}
	for _, group := range set.GetAll() {
		for _, pod := range group.DesiredPodNames(cr, rs) {
			names[pod] = struct{}{}
		}
	}
	return names
}

func (r *ReconcilePerconaServerMongoDB) removeOutdatedServices(ctx context.Context, cr *api.PerconaServerMongoDB, replset *api.ReplsetSpec) error {
	if cr.Spec.Pause {
		return nil
	}

	set, err := membergroup.Resolve(cr, replset)
	if err != nil {
		return errors.Wrapf(err, "resolve member groups for replset %s", replset.Name)
	}

	svcNames := r.expectedExternalServiceNames(cr, replset, set)

	svcList := &corev1.ServiceList{}
	if err := r.client.List(ctx, svcList, &client.ListOptions{
		Namespace:     cr.Namespace,
		LabelSelector: labels.SelectorFromSet(psmdb.ExternalService(cr, replset, cr.GetName()+"-"+replset.Name).GetLabels()),
	}); err != nil {
		return errors.Wrap(err, "get current services")
	}

	for i := range svcList.Items {
		svc := &svcList.Items[i]
		if _, ok := svcNames[svc.Name]; ok {
			continue
		}

		// A per-pod service is named after its pod. While that pod still
		// exists — a scale-down victim, or a member of a retiring group — it
		// needs its service for connectivity and for the removal reconfig.
		pod := new(corev1.Pod)
		err := r.client.Get(ctx, types.NamespacedName{Name: svc.Name, Namespace: cr.Namespace}, pod)
		if err == nil {
			logf.FromContext(ctx).V(1).Info(
				"keeping service of a still-running member", "service", svc.Name)
			continue
		}
		if !k8serrors.IsNotFound(err) {
			return errors.Wrapf(err, "get pod %s", svc.Name)
		}

		if err := r.client.Delete(ctx, svc); err != nil && !k8serrors.IsNotFound(err) {
			return errors.Wrapf(err, "delete service %s", svc.Name)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) removeOutdatedMongosSvc(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if cr.Spec.Pause && cr.Spec.Sharding.Enabled {
		return nil
	}

	svcNames := make(map[string]struct{}, cr.Spec.Sharding.Mongos.Size)
	if cr.Spec.Sharding.Mongos.Expose.ServicePerPod {
		for i := 0; i < int(cr.Spec.Sharding.Mongos.Size); i++ {
			svcNames[naming.MongosPerPodServiceName(cr, i)] = struct{}{}
		}
	} else {
		svcNames[naming.MongosServiceName(cr)] = struct{}{}
	}

	svcList, err := psmdb.GetMongosServices(ctx, r.client, cr)
	if err != nil {
		return errors.Wrap(err, "failed to list mongos services")
	}

	for _, service := range svcList.Items {
		if _, ok := svcNames[service.Name]; !ok {
			err = r.client.Delete(ctx, &service)
			if err != nil {
				return errors.Wrapf(err, "failed to delete service %s", service.Name)
			}
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateMongosSvc(ctx context.Context, cr *api.PerconaServerMongoDB, name string) error {
	svc := psmdb.MongosService(cr, name)
	err := setControllerReference(cr, &svc, r.scheme)
	if err != nil {
		return errors.Wrapf(err, "set owner ref for service %s", svc.Name)
	}

	svc.Spec = psmdb.MongosServiceSpec(cr, name)

	err = r.createOrUpdateSvc(ctx, cr, &svc, cr.Spec.Sharding.Mongos.Expose.SaveOldMeta())
	if err != nil {
		return errors.Wrap(err, "create or update mongos service")
	}
	return nil
}
