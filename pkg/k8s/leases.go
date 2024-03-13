package k8s

import (
	"context"
	"time"

	"github.com/pkg/errors"
	coordinationv1 "k8s.io/api/coordination/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func GetLease(ctx context.Context, k8sclient client.Client, namespace, leaseName, holder string) (bool, error) {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
		},
	}

	if err := k8sclient.Get(ctx, client.ObjectKeyFromObject(lease), lease); err != nil {
		if k8serrors.IsNotFound(err) {
			return true, CreateLease(ctx, k8sclient, namespace, leaseName, holder)
		}
		return false, errors.Wrap(err, "get lease")
	}

	return lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == holder, nil
}

func CreateLease(ctx context.Context, k8sclient client.Client, namespace, leaseName, holder string) error {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
		},
		Spec: coordinationv1.LeaseSpec{
			HolderIdentity: &holder,
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
		},
	}

	if err := k8sclient.Create(ctx, lease); err != nil {
		return errors.Wrap(err, "create lease")
	}

	return nil
}

func DeleteLease(ctx context.Context, k8sclient client.Client, namespace, leaseName, holder string) error {
	lease := &coordinationv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      leaseName,
			Namespace: namespace,
		},
	}

	if err := k8sclient.Delete(ctx, lease); err != nil {
		return errors.Wrap(err, "delete lease")
	}

	return nil
}
