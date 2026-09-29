package k8s_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordv1 "k8s.io/api/coordination/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake" // nolint

	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
)

func TestAcquireLease(t *testing.T) {
	t.Run("creates a lease", func(t *testing.T) {
		cl := fake.NewClientBuilder().Build()
		ctx := context.Background()

		name := "backup-lock"
		namespace := "test"
		holder := "backup1-a96b4d1c-c0ea-424a-b13c-2e1f351c6e61"

		lease, err := k8s.AcquireLease(ctx, cl, name, namespace, holder, nil)
		require.NoError(t, err)

		freshLease, err := k8s.GetLease(ctx, cl, name, namespace)
		require.NoError(t, err)
		require.NotNil(t, freshLease.Spec.AcquireTime)
		assert.Equal(t, lease.Spec.HolderIdentity, freshLease.Spec.HolderIdentity)
	})

	t.Run("returns already held when holder is active", func(t *testing.T) {
		ctx := context.Background()

		name := "backup-lock"
		namespace := "test"
		holder := "backup1-a96b4d1c-c0ea-424a-b13c-2e1f351c6e61"
		otherHolder := "backup2-9fa90f8c-f857-448f-a2fd-d17fc0237b3c"

		cl := fake.NewClientBuilder().WithRuntimeObjects(lease(name, namespace, holder)).Build()

		_, err := k8s.AcquireLease(ctx, cl, name, namespace, otherHolder, func(context.Context, *coordv1.Lease) (bool, error) {
			return false, nil
		})
		require.ErrorIs(t, err, k8s.ErrLeaseAlreadyHeld)

		freshLease := new(coordv1.Lease)
		require.NoError(t, cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, freshLease))
		assert.Equal(t, new(holder), freshLease.Spec.HolderIdentity)
	})

	t.Run("replaces stale holder", func(t *testing.T) {
		ctx := context.Background()

		name := "backup-lock"
		namespace := "test"
		holder := "backup1-a96b4d1c-c0ea-424a-b13c-2e1f351c6e61"
		otherHolder := "backup2-9fa90f8c-f857-448f-a2fd-d17fc0237b3c"

		cl := fake.NewClientBuilder().WithRuntimeObjects(lease(name, namespace, holder)).Build()

		_, err := k8s.AcquireLease(ctx, cl, name, namespace, otherHolder, func(context.Context, *coordv1.Lease) (bool, error) {
			return true, nil
		})
		require.NoError(t, err)

		freshLease := new(coordv1.Lease)
		require.NoError(t, cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, freshLease))
		assert.Equal(t, new(otherHolder), freshLease.Spec.HolderIdentity)
	})
}

func TestReleaseLease(t *testing.T) {
	ctx := context.Background()

	name := "backup-lock"
	namespace := "test"
	holder := "backup1-a96b4d1c-c0ea-424a-b13c-2e1f351c6e61"

	cl := fake.NewClientBuilder().WithRuntimeObjects(lease(name, namespace, holder)).Build()

	require.NoError(t, k8s.ReleaseLease(ctx, cl, name, namespace, holder))

	freshLease := new(coordv1.Lease)
	err := cl.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, freshLease)
	require.Error(t, err)
	assert.True(t, k8serrors.IsNotFound(err))
}

func lease(name, namespace, holder string) *coordv1.Lease {
	return &coordv1.Lease{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Spec: coordv1.LeaseSpec{
			AcquireTime:    &metav1.MicroTime{Time: time.Now()},
			HolderIdentity: new(holder),
		},
	}
}
