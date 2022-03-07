package perconaservermongodb

import (
	"context"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *ReconcilePerconaServerMongoDB) checkFinalizers(ctx context.Context, cr *api.PerconaServerMongoDB) (shouldReconcile bool, err error) {
	shouldReconcile = true
	finalizers := []string{}

	for _, f := range cr.GetFinalizers() {
		switch f {
		case "delete-psmdb-pvc":
			err = r.deletePvcFinalizer(ctx, cr)
		case "delete-psmdb-pods-in-order":
			err = r.deletePSMDBPods(ctx, cr)
			if err != nil {
				shouldReconcile = false
			}
		}
		if err != nil {
			log.Error(err, "failed to run finalizer", "finalizer", f)
			finalizers = append(finalizers, f)
		}
	}
	cr.SetFinalizers(finalizers)
	err = r.client.Update(ctx, cr)

	return shouldReconcile, err
}

func (r *ReconcilePerconaServerMongoDB) deletePSMDBPods(ctx context.Context, cr *api.PerconaServerMongoDB) (err error) {
	done := true
	for _, rs := range cr.Spec.Replsets {
		sts, err := r.getRsStatefulset(ctx, cr, rs.Name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get rs statefulset")
		}

		pods := &corev1.PodList{}
		err = r.client.List(ctx,
			pods,
			&client.ListOptions{
				Namespace:     cr.Namespace,
				LabelSelector: labels.SelectorFromSet(sts.Spec.Selector.MatchLabels),
			},
		)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				continue
			}
			return errors.Wrap(err, "get rs statefulset")
		}
		if len(pods.Items) > int(*sts.Spec.Replicas) {
			return errors.New("waiting pods to be deleted")
		}
		if *sts.Spec.Replicas != 1 {
			rs.Size = 1
			done = false
		}
	}
	if !done {
		return errors.New("waiting statefulsets to be resized")
	}
	return nil
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
		log.Info("deleting StatefulSet", "name", sts.Name)
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
		log.Info("deleting PVC", "name", pvc.Name)
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

		log.Info("deleting secret", "name", secret.Name)
		err = r.client.Delete(ctx, secret)
		if err != nil {
			return errors.Wrapf(err, "delete secret %s", secretName)
		}
	}

	return nil
}
