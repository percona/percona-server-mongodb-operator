package perconaservermongodb

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func TestDeleteOrphanPVCs(t *testing.T) {
	t.Parallel()

	const ns = "orphan-pvcs"

	cr, err := readDefaultCR("some-cluster", ns)
	require.NoError(t, err)
	cr.Finalizers = []string{naming.FinalizerDeletePVC}

	ls := naming.MongodLabels(cr, nil)
	size := resource.MustParse("1Gi")

	kept := []string{
		config.MongodDataVolClaimName + "-some-cluster-rs0-0",
		config.MongodLogVolClaimName + "-some-cluster-rs0-0",
		config.MongodJournalVolClaimName + "-some-cluster-rs0-0",
		"unrelated-some-cluster-rs0-1",
	}
	deleted := []string{
		config.MongodDataVolClaimName + "-some-cluster-rs0-1",
		config.MongodLogVolClaimName + "-some-cluster-rs0-1",
		config.MongodJournalVolClaimName + "-some-cluster-rs0-1",
	}

	objs := []client.Object{cr, newPod(ns, "some-cluster-rs0-0", ls)}
	for _, name := range append(append([]string{}, kept...), deleted...) {
		objs = append(objs, newPVC(ns, name, ls, size, size))
	}
	r := buildFakeClient(objs...)

	require.NoError(t, r.deleteOrphanPVCs(t.Context(), cr))

	for _, name := range kept {
		err := r.client.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: name}, new(corev1.PersistentVolumeClaim))
		assert.NoError(t, err, "PVC %s must be kept", name)
	}
	for _, name := range deleted {
		err := r.client.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: name}, new(corev1.PersistentVolumeClaim))
		assert.True(t, k8serrors.IsNotFound(err), "PVC %s must be deleted", name)
	}
}

func TestRecreateStsForStorageDeleteErrors(t *testing.T) {
	t.Parallel()

	const ns = "log-storage"

	gr := schema.GroupResource{Group: "apps", Resource: "statefulsets"}
	errInternal := errors.New("etcd unavailable")

	tests := map[string]struct {
		deleteErr     error
		wantRecreated bool
		wantErr       error
	}{
		"statefulset replaced under us": {
			deleteErr: k8serrors.NewConflict(gr, "some-cluster-rs0", errors.New("uid precondition")),
		},
		"statefulset already gone": {
			deleteErr: k8serrors.NewNotFound(gr, "some-cluster-rs0"),
		},
		"delete fails": {
			deleteErr: errInternal,
			wantErr:   errInternal,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)
			cr.Spec.LogCollector.Storage = logStorage("2Gi")

			sts := logStorageSts("some-cluster-rs0", ns, naming.ComponentMongod, config.MongodDataVolClaimName)
			r := buildFakeClient(cr, sts.DeepCopy())
			r.client = interceptor.NewClient(r.client.(client.WithWatch), interceptor.Funcs{
				Delete: func(context.Context, client.WithWatch, client.Object, ...client.DeleteOption) error {
					return tt.deleteErr
				},
			})

			recreated, err := r.recreateStsForStorageIfNeeded(t.Context(), cr, sts.DeepCopy())
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantRecreated, recreated)
		})
	}
}

func TestReconcileAuxiliaryPVCsSkips(t *testing.T) {
	t.Parallel()

	const ns = "aux-pvcs"

	tests := map[string]struct {
		component string
		noSpec    bool
		setup     func(cr *api.PerconaServerMongoDB, sts *appsv1.StatefulSet)
		wantSize  string
	}{
		"resizes when nothing blocks it": {
			component: naming.ComponentMongod,
			wantSize:  "5Gi",
		},
		"no storage spec": {
			component: naming.ComponentMongod,
			noSpec:    true,
			wantSize:  "3Gi",
		},
		"arbiter": {
			component: naming.ComponentArbiter,
			wantSize:  "3Gi",
		},
		"data shrink in progress": {
			component: naming.ComponentMongod,
			setup: func(cr *api.PerconaServerMongoDB, sts *appsv1.StatefulSet) {
				cr.SetPVCShrinkState(sts.Name, api.PVCShrinkState{})
			},
			wantSize: "3Gi",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)
			cr.Spec.LogCollector.Storage = logStorage("5Gi")
			cr.Spec.StorageScaling = &api.StorageScalingSpec{EnableVolumeScaling: true}

			ls := naming.MongodLabels(cr, cr.Spec.Replsets[0])
			ls[naming.LabelKubernetesComponent] = tt.component
			sts := logStorageSts("some-cluster-rs0", ns, tt.component,
				config.MongodDataVolClaimName, config.MongodLogVolClaimName)
			sts.Labels = ls
			if tt.setup != nil {
				tt.setup(cr, sts)
			}

			pod := newPod(ns, sts.Name+"-0", ls)
			size := resource.MustParse("3Gi")
			logPVC := newPVC(ns, config.MongodLogVolClaimName+"-"+pod.Name, ls, size, size)

			r := buildFakeClient(cr, sts, pod, logPVC)

			pvcSpec := cr.LogStorage()
			if tt.noSpec {
				pvcSpec = nil
			}
			require.NoError(t, r.reconcileAuxiliaryPVCs(t.Context(), cr, sts, ls, config.MongodLogVolClaimName, pvcSpec))

			got := new(corev1.PersistentVolumeClaim)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(logPVC), got))
			gotSize := got.Spec.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.wantSize, gotSize.String())
		})
	}
}

func TestRevertJournalVolumeTemplate(t *testing.T) {
	t.Parallel()

	const ns = "journal-revert"

	journalSize := func(cr *api.PerconaServerMongoDB, component string) resource.Quantity {
		rs := cr.Spec.Replsets[0]
		storage := rs.JournalStorage
		switch component {
		case naming.ComponentNonVoting:
			storage = rs.NonVoting.JournalStorage
		case naming.ComponentHidden:
			storage = rs.Hidden.JournalStorage
		}
		return storage.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
	}

	tests := map[string]struct {
		component string
		noJournal bool
		wantErr   error
	}{
		"mongod":    {component: naming.ComponentMongod},
		"nonvoting": {component: naming.ComponentNonVoting},
		"hidden":    {component: naming.ComponentHidden},
		"journal storage removed": {
			component: naming.ComponentMongod,
			noJournal: true,
			wantErr:   errMissingStorageSpec,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)
			if !tt.noJournal {
				cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi")
				cr.Spec.Replsets[0].NonVoting.JournalStorage = journalSpec("5Gi")
				cr.Spec.Replsets[0].Hidden.JournalStorage = journalSpec("5Gi")
			}

			sts := logStorageSts("some-cluster-rs0", ns, tt.component, config.MongodDataVolClaimName, config.MongodJournalVolClaimName)
			r := buildFakeClient(cr)

			err = r.revertVolumeTemplate(t.Context(), cr, sts, config.MongodJournalVolClaimName, resource.MustParse("3Gi"))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)

			updated := new(api.PerconaServerMongoDB)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), updated))

			want := resource.MustParse("3Gi")
			assert.True(t, want.Equal(journalSize(updated, tt.component)), "reverted component journal")
			for _, other := range []string{naming.ComponentMongod, naming.ComponentNonVoting, naming.ComponentHidden} {
				if other == tt.component {
					continue
				}
				untouched := resource.MustParse("5Gi")
				assert.True(t, untouched.Equal(journalSize(updated, other)), "%s journal must not change", other)
			}
		})
	}
}
