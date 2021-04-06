package perconaservermongodb

import (
	"context"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/pkg/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (r *ReconcilePerconaServerMongoDB) checkFinalizers(cr *api.PerconaServerMongoDB) error {
	var err error = nil
	if cr.ObjectMeta.DeletionTimestamp != nil {
		finalizers := []string{}

		for _, f := range cr.GetFinalizers() {
			switch f {
			case "delete-psmdb-pvc":
				err = r.deletePvcFinalizer(cr)
				if err != nil {
					log.Error(err, "failed to run finalizer", "finalizer", f)
					finalizers = append(finalizers, f)
				}
			}
		}

		cr.SetFinalizers(finalizers)
		err = r.client.Update(context.TODO(), cr)
	}

	return err
}

func (r *ReconcilePerconaServerMongoDB) deletePvcFinalizer(cr *api.PerconaServerMongoDB) error {
	err := r.deleteAllStatefulsets(cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete all StatefulSets")
	}

	err = r.deleteAllPVC(cr)
	if err != nil {
		return errors.Wrap(err, "failed to delete all PVCs")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) matchUID(cr *api.PerconaServerMongoDB, obj metav1.Object) bool {
	if ref := metav1.GetControllerOf(obj); ref != nil {
		if string(cr.GetUID()) == string(ref.UID) {
			return true
		}
	}
	return false
}

func (r *ReconcilePerconaServerMongoDB) deleteAllStatefulsets(cr *api.PerconaServerMongoDB) error {
	stsList, err := r.getAllstatefulsets(cr)
	if err != nil {
		return errors.Wrap(err, "failed to get StatefulSet list")
	}

	for _, sts := range stsList.Items {
		if !r.matchUID(cr, &sts) {
			continue
		}
		log.Info("deleting StatefulSet", "name", sts.Name)
		err := r.client.Delete(context.TODO(), &sts)
		if err != nil {
			return errors.Wrapf(err, "failed to delete StatefulSet %s", sts.Name)
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteAllPVC(cr *api.PerconaServerMongoDB) error {
	pvcList, err := r.getAllPVCs(cr)
	if err != nil {
		return errors.Wrap(err, "failed to get PVC list")
	}

	for _, pvc := range pvcList.Items {
		log.Info("deleting PVC", "name", pvc.Name)
		err := r.client.Delete(context.TODO(), &pvc)
		if err != nil {
			return errors.Wrapf(err, "failed to delete PVC %s", pvc.Name)
		}
	}

	return nil
}
