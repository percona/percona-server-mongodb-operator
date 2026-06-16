package perconaservermongodb

import (
	"context"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	psmdbconfig "github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/vectorsearch"
)

// reconcileSearch reconciles the mongot ConfigMap, Service, and
// StatefulSet for a single replset (or shard). If the cluster has
// search disabled the function deletes any previously created objects
// and returns.
func (r *ReconcilePerconaServerMongoDB) reconcileSearch(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) error {
	if !cr.IsSearchEnabled() {
		return r.deleteSearch(ctx, cr, rs)
	}

	cm, err := vectorsearch.ConfigMap(cr, rs)
	if err != nil {
		return errors.Wrapf(err, "build mongot ConfigMap for %s", rs.Name)
	}
	if err := r.createOrUpdateConfigMap(ctx, cr, cm); err != nil {
		return errors.Wrapf(err, "reconcile mongot ConfigMap %s", cm.Name)
	}
	configHash, err := psmdbconfig.HashConfigMap(cm)
	if err != nil {
		return errors.Wrapf(err, "hash mongot ConfigMap %s", cm.Name)
	}

	svc := vectorsearch.Service(cr, rs)
	if err := setControllerReference(cr, svc, r.scheme); err != nil {
		return errors.Wrapf(err, "set owner ref for Service %s", svc.Name)
	}
	if err := r.createOrUpdateSvc(ctx, cr, svc, false); err != nil {
		return errors.Wrapf(err, "reconcile mongot Service %s", svc.Name)
	}

	sslAnnotations, err := r.sslAnnotation(ctx, cr.DeepCopy() /* sslAnnotation mutates status conditions */)
	if err != nil {
		return errors.Wrap(err, "get ssl annotations")
	}

	sts := vectorsearch.StatefulSet(cr, rs, r.initImage, configHash, sslAnnotations)

	s := new(appsv1.StatefulSet)
	if err := r.client.Get(ctx, client.ObjectKeyFromObject(sts), s); err == nil {
		if _, ok := s.Annotations[api.AnnotationRestoreInProgress]; ok {
			return nil
		}
	}

	if err := setControllerReference(cr, sts, r.scheme); err != nil {
		return errors.Wrapf(err, "set owner ref for StatefulSet %s", sts.Name)
	}
	if err := r.createOrUpdate(ctx, sts); err != nil {
		return errors.Wrapf(err, "reconcile mongot StatefulSet %s", sts.Name)
	}

	return nil
}

// deleteSearch removes any mongot StatefulSet, Service, and ConfigMap
// previously created for this replset/shard. Order: StatefulSet first
// (so the pod stops referencing the Service and ConfigMap), then
// Service, then ConfigMap. NotFound is treated as success — the
// caller is reconciling toward "absent".
func (r *ReconcilePerconaServerMongoDB) deleteSearch(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) error {
	ns := cr.Namespace
	sts := &appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: naming.SearchStatefulSetName(cr, rs), Namespace: ns}}
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: naming.SearchServiceName(cr, rs), Namespace: ns}}
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: naming.SearchConfigMapName(cr, rs), Namespace: ns}}

	if err := r.client.Delete(ctx, sts); err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrapf(err, "delete StatefulSet %s", sts.Name)
	}
	if err := r.client.Delete(ctx, svc); err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrapf(err, "delete Service %s", svc.Name)
	}
	if err := r.client.Delete(ctx, cm); err != nil && !k8serrors.IsNotFound(err) {
		return errors.Wrapf(err, "delete ConfigMap %s", cm.Name)
	}

	return nil
}

// searchStatus returns the SearchStatus for one replset/shard's mongot
// group. It reads the <cluster>-<rs>-search StatefulSet and the pods
// behind it; the state machine mirrors rsStatus: AppStateInit while
// not all replicas are ready, AppStateReady once every replica's
// containers are ready, AppStateStopping / AppStatePaused when the
// cluster is paused. NotFound on the StatefulSet (creation in flight)
// also returns AppStateInit.
func (r *ReconcilePerconaServerMongoDB) searchStatus(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) (api.SearchStatus, error) {
	sts := &appsv1.StatefulSet{}
	name := naming.SearchStatefulSetName(cr, rs)
	if err := r.client.Get(ctx, types.NamespacedName{Name: name, Namespace: cr.Namespace}, sts); err != nil {
		if k8serrors.IsNotFound(err) {
			return api.SearchStatus{Status: api.AppStateInit}, nil
		}
		return api.SearchStatus{}, errors.Wrapf(err, "get statefulset %s", name)
	}

	pods := &corev1.PodList{}
	if err := r.client.List(ctx, pods, &client.ListOptions{
		Namespace:     cr.Namespace,
		LabelSelector: labels.SelectorFromSet(naming.SearchLabels(cr, rs)),
	}); err != nil {
		return api.SearchStatus{}, errors.Wrapf(err, "list pods for %s", name)
	}

	status := api.SearchStatus{
		Size:   *sts.Spec.Replicas,
		Status: api.AppStateInit,
	}

	for _, pod := range pods.Items {
		if !pod.DeletionTimestamp.IsZero() {
			continue
		}
		if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
			continue
		}

		for _, cntr := range pod.Status.ContainerStatuses {
			if cntr.State.Waiting != nil && cntr.State.Waiting.Message != "" {
				status.Message += cntr.Name + ": " + cntr.State.Waiting.Message + "; "
			}
		}

		for _, cond := range pod.Status.Conditions {
			switch cond.Type {
			case corev1.ContainersReady:
				if cond.Status == corev1.ConditionTrue {
					status.Ready++
				}
			case corev1.PodScheduled:
				if cond.Reason == corev1.PodReasonUnschedulable &&
					cond.LastTransitionTime.Time.Before(time.Now().Add(-1*time.Minute)) {
					status.Status = api.AppStateError
					status.Message = cond.Message
				}
			}
		}
	}

	if status.Ready > status.Size {
		status.Ready = status.Size
	}

	switch {
	case cr.Spec.Pause && status.Ready > 0:
		status.Status = api.AppStateStopping
	case cr.Spec.Pause:
		status.Status = api.AppStatePaused
	case status.Ready > 0 && status.Size == status.Ready:
		status.Status = api.AppStateReady
	}

	return status, nil
}
