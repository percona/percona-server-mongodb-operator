package perconaservermongodb

import (
	"context"
	"slices"
	"testing"

	"k8s.io/apimachinery/pkg/types"

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
