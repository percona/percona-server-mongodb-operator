package perconaservermongodbbackup

import (
	"context"
	"time"

	"github.com/pkg/errors"
	coordinationv1 "k8s.io/api/coordination/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

const leaseName = "percona-server-mongodb-backup"

func (r *ReconcilePerconaServerMongoDBBackup) getLeaseForBackup(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup) (bool, error) {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: cr.Namespace,
		},
	}

	if err := r.client.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		if k8serrors.IsNotFound(err) {
			return true, r.createLeaseForBackup(ctx, cr)
		}
		return false, errors.Wrap(err, "get lease")
	}

	return lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == cr.Name, nil
}

func (r *ReconcilePerconaServerMongoDBBackup) createLeaseForBackup(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup) error {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: cr.Namespace,
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &cr.Name,
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
		},
	}

	if err := r.client.Create(ctx, lease); err != nil {
		return errors.Wrap(err, "create lease")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDBBackup) deleteLeaseForBackup(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup) error {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: cr.Namespace,
		},
	}

	if err := r.client.Delete(ctx, lease); err != nil {
		return errors.Wrap(err, "delete lease")
	}

	return nil
}
