package perconaservermongodb

import (
	"context"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/v2/bson"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/membergroup"
)

var errWaitingTermination = errors.New("waiting pods to be deleted")

func (r *ReconcilePerconaServerMongoDB) checkFinalizers(ctx context.Context, cr *api.PerconaServerMongoDB) (shouldReconcile bool, err error) {
	log := logf.FromContext(ctx)

	shouldReconcile = false
	orderedFinalizers := GetOrderedFinalizers(cr)
	var finalizers []string

	for i, f := range orderedFinalizers {
		switch f {
		case "delete-psmdb-pvc":
			log.Info("The value delete-psmdb-pvc is deprecated and will be deleted in 1.20.0. Use percona.com/delete-psmdb-pvc instead")
			fallthrough
		case naming.FinalizerDeletePVC:
			err = r.deletePvcFinalizer(ctx, cr)
		case "delete-psmdb-pods-in-order":
			log.Info("The value delete-psmdb-pods-in-order is deprecated and will be deleted in 1.20.0. Use percona.com/delete-psmdb-pods-in-order instead")
			fallthrough
		case naming.FinalizerDeletePSMDBPodsInOrder:
			err = r.deletePSMDBPods(ctx, cr)
			if err == nil {
				shouldReconcile = true
			}
		case naming.FinalizerDeletePITR:
			err = r.deleteAllPITRChunks(ctx, cr)
		default:
			finalizers = append(finalizers, f)
			continue
		}
		if err != nil {
			if cr.Status.State == api.AppStateError {
				/*
					If a finalizer returns an error, the operator will continue the reconciliation process.
					However, if the cluster is already in an error state, it is likely that this state will
					persist in each subsequent reconcile, causing the cluster deletion process to get stuck.

					The operator should attempt to execute finalizers, but when the cluster is in an error state,
					any errors from finalizer functions should be ignored to allow the deletion process to continue.

					Do not move this check elsewhere. Finalizers should not put the cluster into an error state.
					If a finalizer function does cause the cluster to enter an error state, its logic should be changed.
				*/
				continue
			}
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
	if err := r.client.Update(ctx, cr); err != nil {
		return false, errors.Wrap(err, "update")
	}

	return shouldReconcile, nil
}

func (r *ReconcilePerconaServerMongoDB) deleteAllPITRChunks(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	pbmc, err := r.newPBM(ctx, r.client, cr)
	if err != nil {
		return errors.Wrap(err, "new pbm")
	}
	defer pbmc.Close(ctx)

	if err := pbmc.DeletePITRChunks(ctx, bson.Timestamp{
		T: uint32(time.Now().Unix()),
	}); err != nil {
		return errors.Wrap(err, "delete pitr chunks")
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deletePSMDBPods(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if cr.Spec.Sharding.Enabled {
		cr.Spec.Sharding.Mongos.Size = 0

		list, err := r.getMongosPods(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "get mongos pods")
		}
		if len(list.Items) != 0 {
			return errWaitingTermination
		}
	}

	if cr.IsSearchEnabled() {
		cr.Spec.Search.Size = 0
		for _, rs := range cr.Spec.Replsets {
			if rs.Search != nil {
				rs.Search.Size = new(int32(0))
			}
		}
	}

	rsDeleted := true
	for _, rs := range cr.Spec.Replsets {
		if err := r.deleteReplset(ctx, cr, rs); err != nil {
			rsDeleted = false
			switch err {
			case errWaitingTermination:
				log.Info("deleting rs pods", "rs", rs.Name, "status", err.Error())
				continue
			default:
				log.Error(err, "failed to delete rs pods", "rs", rs.Name)
				return err
			}
		}
	}
	if !rsDeleted {
		return errWaitingTermination
	}

	if cr.Spec.Sharding.Enabled && cr.Spec.Sharding.ConfigsvrReplSet != nil {
		if err := r.deleteReplset(ctx, cr, cr.Spec.Sharding.ConfigsvrReplSet); err != nil {
			return err
		}
	}
	return nil
}

// deleteReplset shrinks a replica set one step per pass, leaving the member
// holding every acknowledged write until last.
//
// It deletes nothing itself. Each pass writes smaller counts onto the working
// copy of the spec and reports errWaitingTermination; the StatefulSet
// reconciliation later in the same pass pushes those counts, and the
// StatefulSet controller removes the pods. nil means the replica set is gone.
//
// shutdownTarget owns the ordering, and is shared with reconcilePause so that
// pausing and deleting shut a replica set down the same way.
func (r *ReconcilePerconaServerMongoDB) deleteReplset(ctx context.Context, cr *api.PerconaServerMongoDB, rs *api.ReplsetSpec) error {
	set, err := membergroup.Resolve(cr, rs)
	if err != nil {
		return errors.Wrapf(err, "resolve member groups for replset %s", rs.Name)
	}

	target, done, err := r.shutdownTarget(ctx, cr, rs, set)
	if err != nil {
		return errors.Wrapf(err, "compute shutdown target for replset %s", rs.Name)
	}

	r.applyShutdownTarget(rs, target)

	if done {
		return nil
	}

	return errWaitingTermination
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

func GetOrderedFinalizers(cr *api.PerconaServerMongoDB) []string {
	order := []string{naming.FinalizerDeletePITR, naming.FinalizerDeletePSMDBPodsInOrder, naming.FinalizerDeletePVC}

	if cr.CompareVersion("1.17.0") < 0 {
		order = []string{"delete-psmdb-pods-in-order", "delete-psmdb-pvc"}
	}

	finalizers := make([]string, len(cr.GetFinalizers()))
	copy(finalizers, cr.GetFinalizers())
	orderedFinalizers := make([]string, 0, len(finalizers))

	for _, v := range order {
		for i := 0; i < len(finalizers); {
			if v == finalizers[i] {
				orderedFinalizers = append(orderedFinalizers, v)
				finalizers = append(finalizers[:i], finalizers[i+1:]...)
				continue
			}
			i++
		}
	}

	orderedFinalizers = append(orderedFinalizers, finalizers...)
	return orderedFinalizers
}
