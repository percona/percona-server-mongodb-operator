package k8s

import (
	"context"
	"time"

	"github.com/pkg/errors"
	coordv1 "k8s.io/api/coordination/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
)

var (
	ErrNotTheHolder     = errors.New("not the holder")
	ErrLeaseAlreadyHeld = errors.New("lease held by another holder")
)

type IsHolderStaleFunc func(ctx context.Context, lease *coordv1.Lease) (bool, error)

func GetLease(ctx context.Context, c client.Client, name, namespace string) (*coordv1.Lease, error) {
	lease := new(coordv1.Lease)
	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, lease); err != nil {
		return nil, err
	}
	return lease, nil
}

func AcquireLease(ctx context.Context, c client.Client, name, namespace, holder string, checkStale ...IsHolderStaleFunc) (*coordv1.Lease, error) {
	lease := &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	var staleCheck IsHolderStaleFunc
	if len(checkStale) > 0 {
		staleCheck = checkStale[0]
	}

	_, err := controllerutil.CreateOrUpdate(ctx, c, lease, func() error {
		if lease.Spec.HolderIdentity != nil {
			if *lease.Spec.HolderIdentity == holder {
				return nil
			}
			if staleCheck == nil {
				return ErrLeaseAlreadyHeld
			}
			stale, err := staleCheck(ctx, lease)
			if err != nil {
				return errors.Wrap(err, "check if lease holder is stale")
			}
			if !stale {
				return ErrLeaseAlreadyHeld
			}
		}

		lease.Spec.HolderIdentity = &holder
		lease.Spec.AcquireTime = &metav1.MicroTime{Time: time.Now()}
		return nil
	})
	return lease, err
}

func IsLeaseActive(ctx context.Context, c client.Client, name, namespace string) (bool, error) {
	lease := new(coordv1.Lease)

	if err := c.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, lease); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "get lease")
	}

	return lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "", nil
}

func ReleaseLease(ctx context.Context, c client.Client, name, namespace, holder string) error {
	lease, err := GetLease(ctx, c, name, namespace)
	if k8serrors.IsNotFound(err) {
		return nil
	} else if err != nil {
		return errors.Wrap(err, "get lease")
	}

	// HolderIdentity can be nil, we allow deleting the lease if that's the case
	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != holder {
		return ErrNotTheHolder
	}

	if err := c.Delete(ctx, lease, &client.DeleteOptions{
		Preconditions: &metav1.Preconditions{
			UID:             &lease.UID,
			ResourceVersion: &lease.ResourceVersion,
		},
	}); err != nil {
		if k8serrors.IsNotFound(err) || k8serrors.IsConflict(err) {
			return nil
		}
		return errors.Wrap(err, "delete lease")
	}

	return nil
}
