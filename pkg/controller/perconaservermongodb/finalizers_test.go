package perconaservermongodb

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

func TestCheckFinalizers(t *testing.T) {
	ctx := context.Background()
	crName := "check-finalizers"
	ns := crName + "-ns"

	defaultCR, err := readDefaultCR(crName, ns)
	if err != nil {
		t.Fatal(err)
	}

	obj := append(
		fakePodsForRS(defaultCR, defaultCR.Spec.Replsets[0]),
		fakeStatefulset(defaultCR, defaultCR.Spec.Replsets[0], defaultCR.Spec.Replsets[0].Size, "", ""),
	)

	tests := []struct {
		name string
		cr   *api.PerconaServerMongoDB

		expectedShouldReconcile bool
		expectedFinalizers      []string
	}{
		{
			name: "no-finalizers",
			cr: updateObj(t, defaultCR.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Finalizers = nil
			}),
			expectedShouldReconcile: false,
		},
		{
			name: "delete-pvc pass",
			cr: updateObj(t, defaultCR.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Finalizers = []string{naming.FinalizerDeletePVC}
			}),
			expectedShouldReconcile: false,
			expectedFinalizers:      nil,
		},
		{
			name: "delete pods fails",
			cr: updateObj(t, defaultCR.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Finalizers = []string{naming.FinalizerDeletePSMDBPodsInOrder}
			}),
			expectedFinalizers: []string{naming.FinalizerDeletePSMDBPodsInOrder},
		},
		{
			name: "cr with error state, delete pods fails with delete-pvc",
			cr: updateObj(t, defaultCR.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Finalizers = []string{naming.FinalizerDeletePSMDBPodsInOrder}
				cr.Status.State = api.AppStateError
			}),
			expectedFinalizers: []string{},
		},
		{
			name: "delete pods fails with delete-pvc",
			cr: updateObj(t, defaultCR.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Finalizers = []string{naming.FinalizerDeletePVC, naming.FinalizerDeletePSMDBPodsInOrder}
			}),
			expectedFinalizers: []string{naming.FinalizerDeletePSMDBPodsInOrder, naming.FinalizerDeletePVC},
		},
		{
			name: "cr with error state, delete pods fails with delete-pvc",
			cr: updateObj(t, defaultCR.DeepCopy(), func(cr *api.PerconaServerMongoDB) {
				cr.Finalizers = []string{naming.FinalizerDeletePVC, naming.FinalizerDeletePSMDBPodsInOrder}
				cr.Status.State = api.AppStateError
			}),
			expectedFinalizers: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := buildFakeClient(append(obj, tt.cr)...)

			cr := &api.PerconaServerMongoDB{}
			if err := r.client.Get(ctx, types.NamespacedName{Name: crName, Namespace: ns}, cr); err != nil {
				t.Fatal(err)
			}

			if err := cr.CheckNSetDefaults(ctx, version.PlatformKubernetes); err != nil {
				t.Fatal(err)
			}

			shouldReconcile, err := r.checkFinalizers(ctx, cr)
			if err != nil {
				t.Fatal("unexpected error:", err.Error())
			}
			if shouldReconcile != tt.expectedShouldReconcile {
				t.Fatal("unexpected shouldReconcile:", shouldReconcile)
			}

			if err := r.client.Get(ctx, types.NamespacedName{Name: crName, Namespace: ns}, cr); err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(cr.Finalizers, tt.expectedFinalizers) {
				t.Fatal("unexpected finalizers:", cr.Finalizers, "; expected:", tt.expectedFinalizers)
			}
		})
	}
}

func TestDeleteSecretsIgnoresNotFoundOnDelete(t *testing.T) {
	newCR := func() *api.PerconaServerMongoDB {
		cr := newTestCR()
		cr.Spec.Secrets.Users = cr.Name + "-users"
		return cr
	}
	objsFor := func(cr *api.PerconaServerMongoDB) []client.Object {
		objs := []client.Object{cr}
		for _, name := range []string{
			cr.Spec.Secrets.Users,
			"internal-" + cr.Name,
			"internal-" + cr.Name + "-users",
			cr.Name + "-mongodb-encryption-key",
		} {
			objs = append(objs, &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cr.Namespace},
			})
		}
		return objs
	}

	t.Run("delete returns NotFound", func(t *testing.T) {
		cr := newCR()
		r := buildFakeClient(objsFor(cr)...)
		r.client = interceptorClient(r.client, func(_ context.Context, _ client.WithWatch, obj client.Object, _ ...client.DeleteOption) error {
			return k8serrors.NewNotFound(corev1.Resource("secrets"), obj.GetName())
		})

		require.NoError(t, r.deleteSecrets(t.Context(), cr))
	})

	t.Run("delete fails for another reason", func(t *testing.T) {
		cr := newCR()
		r := buildFakeClient(objsFor(cr)...)
		r.client = interceptorClient(r.client, func(_ context.Context, _ client.WithWatch, _ client.Object, _ ...client.DeleteOption) error {
			return errors.New("boom")
		})

		require.Error(t, r.deleteSecrets(t.Context(), cr))
	})
}
