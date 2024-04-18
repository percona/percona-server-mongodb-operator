package perconaservermongodb

import (
	"context"
	"sort"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

var (
	errWaitingTermination  = errors.New("waiting pods to be deleted")
	errWaitingFirstPrimary = errors.New("waiting first pod to become primary")
)

func (r *ReconcilePerconaServerMongoDB) checkFinalizers(ctx context.Context, cr *api.PerconaServerMongoDB) (shouldReconcile bool, err error) {
	log := logf.FromContext(ctx)

	shouldReconcile = false
	orderedFinalizers := cr.GetOrderedFinalizers()
	var finalizers []string

	for i, f := range orderedFinalizers {
		switch f {
		case api.FinalizerDeletePVC:
			err = r.deletePvcFinalizer(ctx, cr)
		case api.FinalizerDeletePSMDBPodsInOrder:
			err = r.deletePSMDBPods(ctx, cr)
			if err == nil {
				shouldReconcile = true
			}
		default:
			finalizers = append(finalizers, f)
			continue
		}
		if err != nil {
			switch err {
			case errWaitingTermination:
			default:
				log.Error(err, "failed to run finalizer", "finalizer", f)
			}
			finalizers = append(finalizers, orderedFinalizers[i:]...)
			break
		}
	}

	cr.SetFinalizers(finalizers)
	err = r.client.Update(ctx, cr)

	return shouldReconcile, err
}

func (r *ReconcilePerconaServerMongoDB) deletePSMDBPods(ctx context.Context, cr *api.PerconaServerMongoDB) (err error) {
	log := logf.FromContext(ctx)

	if cr.Spec.Sharding.Enabled {
		cr.Spec.Sharding.Mongos.Size = 0

		sts := new(appsv1.StatefulSet)
		err := r.client.Get(ctx, cr.MongosNamespacedName(), sts)
		if client.IgnoreNotFound(err) != nil {
			return errors.Wrap(err, "failed to get mongos statefulset")
		}
		if sts.Spec.Replicas != nil && *sts.Spec.Replicas > 0 {
			err = r.disableBalancer(ctx, cr)
			if err != nil {
				return errors.Wrap(err, "failed to disable balancer")
			}
		}
		list, err := r.getMongosPods(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "get mongos pods")
		}
		if len(list.Items) != 0 {
			return errWaitingTermination
		}
	}

	replsetsDeleted := true
	for _, rs := range cr.Spec.Replsets {
		if err := r.deleteRSPods(ctx, cr, rs); err != nil {
			if err != nil {
				switch err {
				case errWaitingTermination, errWaitingFirstPrimary:
					log.Info(err.Error(), "rs", rs.Name)
				default:
					log.Error(err, "failed to delete rs pods", "rs", rs.Name)
				}
				replsetsDeleted = false
				continue
			}
		}
	}
	if !replsetsDeleted {
		return errWaitingTermination
	}

	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		if err := r.deleteRSPods(ctx, cr, cr.Spec.Sharding.ConfigsvrReplSet); err != nil {
			return err
		}
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteRSPods(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) error {
	sts, err := r.getRsStatefulset(ctx, cr, rs.Name)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "get rs statefulset")
	}

	pods := &corev1.PodList{}
	err = r.client.List(ctx,
		pods,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(sts.Spec.Template.Labels),
		},
	)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return nil
		}
		return errors.Wrap(err, "get rs statefulset")
	}

	if len(pods.Items) == 0 {
		return nil
	}

	// `k8sclient.List` returns unsorted list of pods
	// We should sort pods to be sure that the first pod is the primary
	sort.Slice(pods.Items, func(i, j int) bool {
		return pods.Items[i].Name < pods.Items[j].Name
	})

	switch *sts.Spec.Replicas {
	case 0:
		rs.Size = 0
		if len(pods.Items) == 0 {
			return nil
		}
		return errWaitingTermination
	case 1:
		rs.Size = 1
		// If there is one pod left, we should be sure that it's the primary
		if len(pods.Items) != 1 {
			return errWaitingTermination
		}

		isPrimary, err := r.isPodPrimary(ctx, cr, pods.Items[0], rs)
		if err != nil {
			return errors.Wrap(err, "is pod primary")
		}
		if !isPrimary {
			return errWaitingFirstPrimary
		}

		// If true, we should resize the replset to 0
		rs.Size = 0
		return errWaitingTermination
	default:
		// If statefulset size is bigger then 1 we should set the first pod as primary.
		// After that we can continue the resize of statefulset.
		rs.Size = *sts.Spec.Replicas
		isPrimary, err := r.isPodPrimary(ctx, cr, pods.Items[0], rs)
		if err != nil {
			return errors.Wrap(err, "is pod primary")
		}
		if !isPrimary {
			if len(pods.Items) != int(*sts.Spec.Replicas) {
				return errWaitingTermination
			}
			err = r.setPrimary(ctx, cr, rs, pods.Items[0])
			if err != nil {
				return errors.Wrap(err, "set primary")
			}
			return errWaitingFirstPrimary
		}

		rs.Size = 1
		return errWaitingTermination
	}
}

func (r *ReconcilePerconaServerMongoDB) deletePvcFinalizer(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	err := r.deleteAllStatefulsets(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete all StatefulSets")
	}

	err = r.deleteAllPVC(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete all PVCs")
	}

	err = r.deleteSecrets(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete secrets")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteAllStatefulsets(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	stsList, err := r.getAllstatefulsets(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to get StatefulSet list")
	}

	for _, sts := range stsList.Items {
		logf.FromContext(ctx).Info("deleting StatefulSet", "name", sts.Name)
		err := r.client.Delete(ctx, &sts)
		if err != nil {
			return errors.Wrapf(err, "failed to delete StatefulSet %s", sts.Name)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteAllPVC(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	pvcList, err := r.getAllPVCs(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "failed to get PVC list")
	}

	for _, pvc := range pvcList.Items {
		logf.FromContext(ctx).Info("deleting PVC", "name", pvc.Name)
		err := r.client.Delete(ctx, &pvc)
		if err != nil {
			return errors.Wrapf(err, "failed to delete PVC %s", pvc.Name)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteSecrets(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	secrets := []string{
		cr.Spec.Secrets.Users,
		"internal-" + cr.Name,
		"internal-" + cr.Name + "-users",
		cr.Name + "-mongodb-encryption-key",
	}

	for _, secretName := range secrets {
		secret := &corev1.Secret{}
		err := r.client.Get(ctx, types.NamespacedName{
			Namespace: cr.Namespace,
			Name:      secretName,
		}, secret)

		if err != nil && !k8serrors.IsNotFound(err) {
			return errors.Wrap(err, "get secret")
		}

		if k8serrors.IsNotFound(err) {
			continue
		}

		logf.FromContext(ctx).Info("deleting secret", "name", secret.Name)
		err = r.client.Delete(ctx, secret)
		if err != nil {
			return errors.Wrapf(err, "delete secret %s", secretName)
		}
	}

	return nil
}
