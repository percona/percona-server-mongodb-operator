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
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func logStorage(size string) *api.LogStorageSpec {
	return &api.LogStorageSpec{
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
		crVersion     string
		storage       *api.LogStorageSpec
		component     string
		claims        []string
		stsMissing    bool
		wantRecreated bool
	}{
		"log storage added": {
			storage:       logStorage("2Gi"),
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
			storage:       logStorage("2Gi"),
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
			storage:       logStorage("2Gi"),
			component:     naming.ComponentArbiter,
			claims:        nil,
			wantRecreated: false,
		},
		"hidden gets the claim": {
			storage:       logStorage("2Gi"),
			component:     naming.ComponentHidden,
			claims:        []string{config.MongodDataVolClaimName},
			wantRecreated: true,
		},
		"ignored on 1.23.1": {
			crVersion:     "1.23.1",
			storage:       logStorage("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			wantRecreated: false,
		},
		"statefulset does not exist yet": {
			storage:       logStorage("2Gi"),
			component:     naming.ComponentMongod,
			claims:        []string{config.MongodDataVolClaimName},
			stsMissing:    true,
			wantRecreated: false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)

			if tt.crVersion != "" {
				cr.Spec.CRVersion = tt.crVersion
			}
			cr.Spec.LogCollector.Storage = tt.storage

			sts := logStorageSts("some-cluster-rs0", ns, tt.component, tt.claims...)
			nn := types.NamespacedName{Name: sts.Name, Namespace: ns}

			objs := []client.Object{cr}
			if !tt.stsMissing {
				objs = append(objs, sts)
			}
			r := buildFakeClient(objs...)

			before := new(appsv1.StatefulSet)
			if !tt.stsMissing {
				require.NoError(t, r.client.Get(t.Context(), nn, before))
			}

			recreated, err := r.recreateStsForLogStorageIfNeeded(t.Context(), cr, sts)
			require.NoError(t, err)
			assert.Equal(t, tt.wantRecreated, recreated)

			after := new(appsv1.StatefulSet)
			err = r.client.Get(t.Context(), nn, after)
			if tt.stsMissing {
				assert.True(t, k8serrors.IsNotFound(err), "statefulset must not be created here")
				return
			}

			require.NoError(t, err, "statefulset must exist after the call")
			assert.Equal(t, tt.wantRecreated, before.ResourceVersion != after.ResourceVersion, "statefulset recreation")
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
		wantErrMsg   string
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
			wantErrMsg:   "shrinking log volumes is not supported",
			wantLogSize:  "3Gi",
			wantSpecSize: "3Gi",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cr, err := readDefaultCR("some-cluster", ns)
			require.NoError(t, err)

			cr.Spec.LogCollector.Storage = logStorage(tt.requested)
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

			err = r.reconcileLogPVCs(t.Context(), cr, sts, ls)
			if tt.wantErrMsg != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErrMsg)
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
			gotSpec := updatedCR.LogStorage().Resources.Requests[corev1.ResourceStorage]
			assert.Equal(t, tt.wantSpecSize, gotSpec.String(), "log storage request in the CR")
		})
	}
}

func TestPodsMissingClaimVolumes(t *testing.T) {
	t.Parallel()

	podWith := func(volumes ...string) corev1.Pod {
		pod := corev1.Pod{}
		for _, name := range volumes {
			pod.Spec.Volumes = append(pod.Spec.Volumes, corev1.Volume{Name: name})
		}
		return pod
	}

	tests := map[string]struct {
		claims []string
		pods   []corev1.Pod
		want   bool
	}{
		"every pod has every claim volume": {
			claims: []string{config.MongodDataVolClaimName, config.MongodLogVolClaimName},
			pods: []corev1.Pod{
				podWith(config.MongodDataVolClaimName, config.MongodLogVolClaimName),
				podWith(config.MongodDataVolClaimName, config.MongodLogVolClaimName),
			},
			want: false,
		},
		"one pod predates the log claim": {
			claims: []string{config.MongodDataVolClaimName, config.MongodLogVolClaimName},
			pods: []corev1.Pod{
				podWith(config.MongodDataVolClaimName, config.MongodLogVolClaimName),
				podWith(config.MongodDataVolClaimName),
			},
			want: true,
		},
		"log claim removed from the statefulset": {
			claims: []string{config.MongodDataVolClaimName},
			pods: []corev1.Pod{
				podWith(config.MongodDataVolClaimName, config.MongodLogVolClaimName),
			},
			want: false,
		},
		"statefulset has no claim templates": {
			pods: []corev1.Pod{podWith(config.MongodDataVolClaimName)},
			want: false,
		},
		"no pods": {
			claims: []string{config.MongodDataVolClaimName},
			want:   false,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sts := logStorageSts("some-cluster-rs0", "log-storage", naming.ComponentMongod, tt.claims...)
			assert.Equal(t, tt.want, podsMissingClaimVolumes(sts, tt.pods))
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
			cr.Spec.LogCollector.Storage = logStorage("3Gi")

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

			require.NoError(t, r.reconcileLogPVCs(t.Context(), cr, stale, ls))

			err = r.client.Get(t.Context(), client.ObjectKeyFromObject(sts), new(appsv1.StatefulSet))
			if tt.wantStsExists {
				require.NoError(t, err, "statefulset must survive another claim's resize")
			} else {
				assert.True(t, k8serrors.IsNotFound(err), "statefulset is deleted when the log resize completes")
			}
		})
	}
}
