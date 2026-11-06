package perconaservermongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func journalSpec(size string) *api.StorageSpec {
	return &api.StorageSpec{
		PersistentVolumeClaim: &api.PVCSpec{
			PersistentVolumeClaimSpec: &corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(size),
					},
				},
			},
		},
	}
}

func TestJournalStorage(t *testing.T) {
	t.Parallel()

	const ns = "journal-storage"

	tests := map[string]struct {
		crVersion string
		setup     func(cr *api.PerconaServerMongoDB)
		component string
		replset   string
		wantSize  string
	}{
		"mongod": {
			setup:     func(cr *api.PerconaServerMongoDB) { cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi") },
			component: naming.ComponentMongod,
			replset:   "rs0",
			wantSize:  "5Gi",
		},
		"non-voting uses its own spec": {
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi")
				cr.Spec.Replsets[0].NonVoting.JournalStorage = journalSpec("1Gi")
			},
			component: naming.ComponentNonVoting,
			replset:   "rs0",
			wantSize:  "1Gi",
		},
		"hidden uses its own spec": {
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi")
				cr.Spec.Replsets[0].Hidden.JournalStorage = journalSpec("2Gi")
			},
			component: naming.ComponentHidden,
			replset:   "rs0",
			wantSize:  "2Gi",
		},
		"non-voting does not inherit the replset spec": {
			setup:     func(cr *api.PerconaServerMongoDB) { cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi") },
			component: naming.ComponentNonVoting,
			replset:   "rs0",
		},
		"config server": {
			setup: func(cr *api.PerconaServerMongoDB) {
				cr.Spec.Sharding.ConfigsvrReplSet.JournalStorage = journalSpec("3Gi")
			},
			component: naming.ComponentConfigSrv,
			replset:   "cfg",
			wantSize:  "3Gi",
		},
		"arbiter never gets a journal volume": {
			setup:     func(cr *api.PerconaServerMongoDB) { cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi") },
			component: naming.ComponentArbiter,
			replset:   "rs0",
		},
		"mongos never gets a journal volume": {
			setup:     func(cr *api.PerconaServerMongoDB) { cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi") },
			component: naming.ComponentMongos,
			replset:   "rs0",
		},
		"unknown replset": {
			setup:     func(cr *api.PerconaServerMongoDB) { cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi") },
			component: naming.ComponentMongod,
			replset:   "rs9",
		},
		"not configured": {
			setup:     func(cr *api.PerconaServerMongoDB) {},
			component: naming.ComponentMongod,
			replset:   "rs0",
		},
		"ignored before 1.24.0": {
			crVersion: "1.23.1",
			setup:     func(cr *api.PerconaServerMongoDB) { cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi") },
			component: naming.ComponentMongod,
			replset:   "rs0",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)
			if tt.crVersion != "" {
				cr.Spec.CRVersion = tt.crVersion
			}
			tt.setup(cr)

			sts := &appsv1.StatefulSet{
				Labels: map[string]string{
					naming.LabelKubernetesComponent: tt.component,
					naming.LabelKubernetesReplset:   tt.replset,
				},
			}

			got := journalStorage(cr, sts)
			if tt.wantSize == "" {
				assert.Nil(t, got)
				return
			}

			require.NotNil(t, got)
			size := got.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.wantSize, size.String())
		})
	}
}

func TestValidateJournalStorage(t *testing.T) {
	t.Parallel()

	const ns = "journal-storage"

	tests := map[string]struct {
		claims      []string
		stsMissing  bool
		pvcs        []string
		wantJournal bool
		wantErr     error
	}{
		"journal stays off": {
			claims: []string{config.MongodDataVolClaimName},
		},
		"journal stays on": {
			claims:      []string{config.MongodDataVolClaimName, config.MongodJournalVolClaimName},
			wantJournal: true,
		},
		"enabling on a running statefulset is allowed": {
			claims:      []string{config.MongodDataVolClaimName},
			wantJournal: true,
		},
		"disabling on a running statefulset": {
			claims:  []string{config.MongodDataVolClaimName, config.MongodJournalVolClaimName},
			wantErr: errJournalStorageDisable,
		},
		"new statefulset": {
			stsMissing:  true,
			wantJournal: true,
		},
		"disabling while statefulset is recreated": {
			stsMissing: true,
			pvcs:       []string{"mongod-journal-some-cluster-rs0-0"},
			wantErr:    errJournalStorageDisable,
		},
		"journal PVCs of another statefulset": {
			stsMissing: true,
			pvcs:       []string{"mongod-journal-some-cluster-rs0-nv-0"},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sts := logStorageSts("some-cluster-rs0", ns, naming.ComponentMongod, tt.claims...)

			objs := []client.Object{}
			if !tt.stsMissing {
				objs = append(objs, sts)
			}
			for _, name := range tt.pvcs {
				objs = append(objs, &corev1.PersistentVolumeClaim{Name: name, Namespace: ns})
			}
			r := buildFakeClient(objs...)

			err := r.validateJournalStorage(t.Context(), sts, tt.wantJournal)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestReconcileJournalPVCs(t *testing.T) {
	t.Parallel()

	const ns = "journal-storage"

	tests := map[string]struct {
		requested    string
		autoscaling  bool
		pvcSize      string
		wantErr      error
		wantSize     string
		wantSpecSize string
	}{
		"grows the journal PVCs": {
			requested:    "5Gi",
			autoscaling:  true,
			pvcSize:      "3Gi",
			wantSize:     "5Gi",
			wantSpecSize: "5Gi",
		},
		"rejects a shrink and reverts the request": {
			requested:    "1Gi",
			pvcSize:      "3Gi",
			wantErr:      errAuxiliaryShrink,
			wantSize:     "3Gi",
			wantSpecSize: "3Gi",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)

			cr.Spec.Replsets[0].JournalStorage = journalSpec(tt.requested)
			if tt.autoscaling {
				cr.Spec.StorageScaling = &api.StorageScalingSpec{EnableVolumeScaling: true}
			}

			ls := naming.MongodLabels(cr, cr.Spec.Replsets[0])
			sts := logStorageSts("some-cluster-rs0", ns, naming.ComponentMongod,
				config.MongodDataVolClaimName, config.MongodJournalVolClaimName)
			sts.Labels = ls

			pod := newPod(ns, sts.Name+"-0", ls)
			size := resource.MustParse(tt.pvcSize)
			journalPVC := newPVC(ns, config.MongodJournalVolClaimName+"-"+pod.Name, ls, size, size)
			dataPVC := newPVC(ns, config.MongodDataVolClaimName+"-"+pod.Name, ls, size, size)

			r := buildFakeClient(cr, sts, pod, journalPVC, dataPVC)

			err = r.reconcileAuxiliaryPVCs(t.Context(), cr, sts, ls, config.MongodJournalVolClaimName, journalStorage(cr, sts))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			updated := new(corev1.PersistentVolumeClaim)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(journalPVC), updated))
			got := updated.Spec.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.wantSize, got.String(), "journal PVC request")

			updatedData := new(corev1.PersistentVolumeClaim)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(dataPVC), updatedData))
			gotData := updatedData.Spec.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.pvcSize, gotData.String(), "data PVC must not be touched by the journal claim")

			updatedCR := new(api.PerconaServerMongoDB)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), updatedCR))
			gotSpec := updatedCR.Spec.Replsets[0].JournalStorage.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.wantSpecSize, gotSpec.String(), "journal storage request in the CR")
		})
	}
}
