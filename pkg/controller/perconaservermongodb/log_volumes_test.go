package perconaservermongodb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func logStorageSpec(size string) *api.StorageSpec {
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

func logStorageSts(name, ns, component string, claims ...string) *appsv1.StatefulSet {
	sts := &appsv1.StatefulSet{
		Name:      name,
		Namespace: ns,
		Labels: map[string]string{
			naming.LabelKubernetesComponent: component,
			naming.LabelKubernetesReplset:   "rs0",
		},
	}

	for _, claim := range claims {
		sts.Spec.VolumeClaimTemplates = append(sts.Spec.VolumeClaimTemplates, corev1.PersistentVolumeClaim{
			Name: claim,
			Spec: corev1.PersistentVolumeClaimSpec{
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("3Gi"),
					},
				},
			},
		})
	}

	return sts
}

func TestRecreateStsForLogStorageIfNeeded(t *testing.T) {
	t.Parallel()

	const ns = "log-storage"

	tests := map[string]struct {
		crVersion      string
		storage        *api.StorageSpec
		hiddenStorage  *api.StorageSpec
		journalStorage bool
		component      string
		claims         []string
		stsMissing     bool
		terminating    bool
		wantRecreated  bool
	}{
		"journal storage added": {
			journalStorage: true,
			component:      naming.ComponentMongod,
			claims:         []string{config.MongodDataVolClaimName},
			wantRecreated:  true,
		},
		"journal storage unchanged": {
			journalStorage: true,
			component:      naming.ComponentMongod,
			claims:         []string{config.MongodDataVolClaimName, config.MongodJournalVolClaimName},
		},
		"log storage added": {
			storage:       logStorageSpec("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			wantRecreated: true,
		},
		"log storage removed": {
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName, config.MongodLogVolClaimName},
			wantRecreated: true,
		},
		"log storage unchanged": {
			storage:       logStorageSpec("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName, config.MongodLogVolClaimName},
			wantRecreated: false,
		},
		"log storage not configured": {
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			wantRecreated: false,
		},
		"arbiter is skipped": {
			storage:       logStorageSpec("2Gi"),
			component:     naming.ComponentArbiter,
			claims:        nil,
			wantRecreated: false,
		},
		"hidden gets the claim": {
			storage:       logStorageSpec("2Gi"),
			component:     naming.ComponentHidden,
			claims:        []string{config.MongodDataVolClaimName},
			wantRecreated: true,
		},
		"hidden storage leaves mongod alone": {
			hiddenStorage: logStorageSpec("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			wantRecreated: false,
		},
		"ignored on 1.23.1": {
			crVersion:     "1.23.1",
			storage:       logStorageSpec("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			wantRecreated: false,
		},
		"statefulset does not exist yet": {
			storage:       logStorageSpec("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			stsMissing:    true,
			wantRecreated: false,
		},
		"waits for the old statefulset to be deleted": {
			storage:       logStorageSpec("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			terminating:   true,
			wantRecreated: true,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)

			if tt.crVersion != "" {
				cr.Spec.CRVersion = tt.crVersion
			}
			rs := cr.Spec.Replsets[0]
			rs.Hidden.LogStorage = tt.hiddenStorage
			if tt.component == naming.ComponentHidden {
				rs.Hidden.LogStorage = tt.storage
			} else {
				rs.LogStorage = tt.storage
			}
			if tt.journalStorage {
				cr.Spec.Replsets[0].JournalStorage = journalSpec("5Gi")
			}

			sts := logStorageSts("some-cluster-rs0", ns, tt.component, tt.claims...)
			if tt.terminating {
				sts.Finalizers = []string{metav1.FinalizerOrphanDependents}
				sts.DeletionTimestamp = &metav1.Time{Time: time.Now()}
			}
			nn := types.NamespacedName{Name: sts.Name, Namespace: ns}

			objs := []client.Object{cr}
			if !tt.stsMissing {
				objs = append(objs, sts.DeepCopy())
			}
			r := buildFakeClient(objs...)

			before := new(appsv1.StatefulSet)
			if !tt.stsMissing {
				require.NoError(t, r.client.Get(t.Context(), nn, before))
			}

			desired := sts.DeepCopy()
			desired.ResourceVersion = "999"
			recreated, err := r.recreateStsForStorageIfNeeded(t.Context(), cr, desired)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRecreated, recreated)

			after := new(appsv1.StatefulSet)
			err = r.client.Get(t.Context(), nn, after)
			switch {
			case tt.stsMissing:
				assert.True(t, k8serrors.IsNotFound(err), "statefulset must not be created here")
			case tt.terminating:
				require.NoError(t, err)
				assert.Equal(t, before.ResourceVersion, after.ResourceVersion, "terminating statefulset must be left alone")
			case tt.wantRecreated:
				assert.True(t, k8serrors.IsNotFound(err), "statefulset must be deleted and created on the next reconcile")
			default:
				require.NoError(t, err)
				assert.Equal(t, before.ResourceVersion, after.ResourceVersion, "statefulset must not be touched")
			}
		})
	}
}

func TestReconcileLogPVCs(t *testing.T) {
	t.Parallel()

	const ns = "log-storage"

	tests := map[string]struct {
		requested    string
		autoscaling  bool
		pvcSize      string
		wantErr      error
		wantLogSize  string
		wantSpecSize string
	}{
		"grows the log PVCs": {
			requested:    "5Gi",
			autoscaling:  true,
			pvcSize:      "3Gi",
			wantLogSize:  "5Gi",
			wantSpecSize: "5Gi",
		},
		"rejects a shrink and reverts the request": {
			requested:    "1Gi",
			pvcSize:      "3Gi",
			wantErr:      errAuxiliaryShrink,
			wantLogSize:  "3Gi",
			wantSpecSize: "3Gi",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)

			cr.Spec.Replsets[0].LogStorage = logStorageSpec(tt.requested)
			if tt.autoscaling {
				cr.Spec.StorageScaling = &api.StorageScalingSpec{EnableVolumeScaling: true}
			}

			ls := naming.MongodLabels(cr, cr.Spec.Replsets[0])
			sts := logStorageSts("some-cluster-rs0", ns, naming.ComponentMongod,
				config.MongodDataVolClaimName, config.MongodLogVolClaimName)
			sts.Labels = ls

			pod := newPod(ns, sts.Name+"-0", ls)
			size := resource.MustParse(tt.pvcSize)
			logPVC := newPVC(ns, config.MongodLogVolClaimName+"-"+pod.Name, ls, size, size)
			dataPVC := newPVC(ns, config.MongodDataVolClaimName+"-"+pod.Name, ls, size, size)

			r := buildFakeClient(cr, sts, pod, logPVC, dataPVC)

			err = r.reconcileAuxiliaryPVCs(t.Context(), cr, sts, ls, config.MongodLogVolClaimName, logStorage(cr, sts))
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			} else {
				require.NoError(t, err)
			}

			updatedLog := new(corev1.PersistentVolumeClaim)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(logPVC), updatedLog))
			gotLog := updatedLog.Spec.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.wantLogSize, gotLog.String(), "log PVC request")

			updatedData := new(corev1.PersistentVolumeClaim)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(dataPVC), updatedData))
			gotData := updatedData.Spec.Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.pvcSize, gotData.String(), "data PVC must not be touched by the log claim")

			updatedCR := new(api.PerconaServerMongoDB)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), updatedCR))
			gotSpec := updatedCR.LogStorage(updatedCR.Spec.Replsets[0].LogStorage).Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.wantSpecSize, gotSpec.String(), "log storage request in the CR")
		})
	}
}

func TestReconcileLogPVCsDuringDataResize(t *testing.T) {
	t.Parallel()

	const ns = "log-storage"

	tests := map[string]struct {
		resizingClaim string
		wantStsExists bool
	}{
		"data claim resize must not be adopted by the log claim": {
			resizingClaim: config.MongodDataVolClaimName,
			wantStsExists: true,
		},
		"legacy annotation without a claim belongs to the data claim": {
			wantStsExists: true,
		},
		"log claim owns its own resize": {
			resizingClaim: config.MongodLogVolClaimName,
			wantStsExists: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)
			cr.Spec.Replsets[0].LogStorage = logStorageSpec("3Gi")

			ls := naming.MongodLabels(cr, cr.Spec.Replsets[0])
			sts := logStorageSts("some-cluster-rs0", ns, naming.ComponentMongod,
				config.MongodDataVolClaimName, config.MongodLogVolClaimName)
			sts.Labels = ls
			sts.Annotations = map[string]string{
				api.AnnotationPVCResizeInProgress: time.Now().Format(time.RFC3339),
			}
			if tt.resizingClaim != "" {
				sts.Annotations[api.AnnotationPVCResizeClaim] = tt.resizingClaim
			}

			pod := newPod(ns, sts.Name+"-0", ls)
			size := resource.MustParse("3Gi")
			logPVC := newPVC(ns, config.MongodLogVolClaimName+"-"+pod.Name, ls, size, size)
			dataPVC := newPVC(ns, config.MongodDataVolClaimName+"-"+pod.Name, ls, size, size)

			r := buildFakeClient(cr, sts, pod, logPVC, dataPVC)

			// reconcilePVCs holds the statefulset it read before the data resize
			// annotated it, so the guard must not depend on the in-memory copy.
			stale := sts.DeepCopy()
			stale.Annotations = nil

			require.NoError(t, r.reconcileAuxiliaryPVCs(t.Context(), cr, stale, ls, config.MongodLogVolClaimName, logStorage(cr, stale)))

			err = r.client.Get(t.Context(), client.ObjectKeyFromObject(sts), new(appsv1.StatefulSet))
			if tt.wantStsExists {
				require.NoError(t, err, "statefulset must survive another claim's resize")
			} else {
				assert.True(t, k8serrors.IsNotFound(err), "statefulset is deleted when the log resize completes")
			}
		})
	}
}

func TestRevertLogVolumeTemplatePerComponent(t *testing.T) {
	t.Parallel()

	const ns = "log-storage"

	tests := map[string]struct {
		component     string
		wantMongod    string
		wantNonVoting string
		wantHidden    string
	}{
		"mongod": {
			component:     naming.ComponentMongod,
			wantMongod:    "1Gi",
			wantNonVoting: "3Gi",
			wantHidden:    "3Gi",
		},
		"hidden": {
			component:     naming.ComponentHidden,
			wantMongod:    "3Gi",
			wantNonVoting: "3Gi",
			wantHidden:    "1Gi",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)

			rs := cr.Spec.Replsets[0]
			rs.LogStorage = logStorageSpec("3Gi")
			rs.NonVoting.LogStorage = logStorageSpec("3Gi")
			rs.Hidden.LogStorage = logStorageSpec("3Gi")

			sts := logStorageSts("some-cluster-rs0", ns, tt.component, config.MongodDataVolClaimName, config.MongodLogVolClaimName)
			r := buildFakeClient(cr)

			require.NoError(t, r.revertVolumeTemplate(t.Context(), cr, sts, config.MongodLogVolClaimName, resource.MustParse("1Gi")))

			got := new(api.PerconaServerMongoDB)
			require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), got))

			size := func(s *api.StorageSpec) string {
				q := s.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
				return q.String()
			}
			gotRS := got.Spec.Replsets[0]
			assert.Equal(t, tt.wantMongod, size(gotRS.LogStorage), "mongod")
			assert.Equal(t, tt.wantNonVoting, size(gotRS.NonVoting.LogStorage), "nonvoting")
			assert.Equal(t, tt.wantHidden, size(gotRS.Hidden.LogStorage), "hidden")
		})
	}
}

func TestComponentStorage(t *testing.T) {
	t.Parallel()

	journal := map[string]*api.StorageSpec{}
	logs := map[string]*api.StorageSpec{}
	for _, c := range []string{"rs0", "rs0-nv", "rs0-hidden", "cfg"} {
		journal[c] = journalSpec("1Gi")
		logs[c] = logStorageSpec("1Gi")
	}

	cr := &api.PerconaServerMongoDB{
		Spec: api.PerconaServerMongoDBSpec{
			Replsets: []*api.ReplsetSpec{{
				Name:           "rs0",
				JournalStorage: journal["rs0"],
				LogStorage:     logs["rs0"],
				NonVoting:      api.NonVotingSpec{JournalStorage: journal["rs0-nv"], LogStorage: logs["rs0-nv"]},
				Hidden:         api.HiddenSpec{JournalStorage: journal["rs0-hidden"], LogStorage: logs["rs0-hidden"]},
			}},
			Sharding: api.Sharding{
				ConfigsvrReplSet: &api.ReplsetSpec{Name: "cfg", JournalStorage: journal["cfg"], LogStorage: logs["cfg"]},
			},
		},
	}

	tests := map[string]struct {
		component string
		replset   string
		want      string
	}{
		"mongod":           {component: naming.ComponentMongod, replset: "rs0", want: "rs0"},
		"nonvoting":        {component: naming.ComponentNonVoting, replset: "rs0", want: "rs0-nv"},
		"hidden":           {component: naming.ComponentHidden, replset: "rs0", want: "rs0-hidden"},
		"configsvr":        {component: naming.ComponentConfigSrv, replset: "cfg", want: "cfg"},
		"arbiter":          {component: naming.ComponentArbiter, replset: "rs0"},
		"mongos":           {component: naming.ComponentMongos},
		"unknown replset":  {component: naming.ComponentMongod, replset: "rs9"},
		"missing replsets": {component: naming.ComponentHidden},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sts := &appsv1.StatefulSet{Labels: map[string]string{
				naming.LabelKubernetesComponent: tt.component,
				naming.LabelKubernetesReplset:   tt.replset,
			}}

			gotJournal, gotLogs := componentStorage(cr, sts)
			assert.Same(t, journal[tt.want], gotJournal, "journal")
			assert.Same(t, logs[tt.want], gotLogs, "logs")
		})
	}
}
