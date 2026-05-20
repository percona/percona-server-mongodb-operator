package perconaservermongodb

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

func TestReconcilePersistentVolumes(t *testing.T) {
	tests := []struct {
		name              string
		requested         string
		configured        string
		actual            string
		resizeInProgress  bool
		volumeScaling     bool
		expectSTSDeleted  bool
		expectErrContains string
		expectCRStorage   string
	}{
		{
			name:             "finishes resize when pvc exceeds requested size",
			requested:        "1200Mi",
			configured:       "1200Mi",
			actual:           "6G",
			resizeInProgress: true,
			expectSTSDeleted: true,
		},
		{
			name:             "deletes statefulset when requested matches actual but template differs",
			requested:        "1200Mi",
			configured:       "6G",
			actual:           "1200Mi",
			expectSTSDeleted: true,
		},
		{
			name:             "deletes statefulset when actual exceeds requested and template is lower",
			requested:        "1200Mi",
			configured:       "1Gi",
			actual:           "6G",
			expectSTSDeleted: true,
		},
		{
			name:       "does nothing when requested configured and actual sizes are aligned",
			requested:  "1200Mi",
			configured: "1200Mi",
			actual:     "1200Mi",
		},
		{
			name:       "does nothing when requested and configured sizes are aligned",
			requested:  "1200Mi",
			configured: "1200Mi",
			actual:     "6G",
		},
		{
			name:              "rejects shrink when configured is higher than requested and actual is higher than requested",
			requested:         "1200Mi",
			configured:        "2Gi",
			actual:            "6G",
			volumeScaling:     true,
			expectErrContains: "requested storage (1200Mi) is less than actual storage (6G)",
			expectCRStorage:   "2Gi",
		},
		{
			name:             "deletes statefulset when pvc already matches increased request but template is stale",
			requested:        "2Gi",
			configured:       "1200Mi",
			actual:           "2Gi",
			expectSTSDeleted: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			requested := resource.MustParse(tt.requested)
			configured := resource.MustParse(tt.configured)
			actual := resource.MustParse(tt.actual)

			cr, err := readDefaultCR("some-cluster", "ns")
			require.NoError(t, err)
			require.NotEmpty(t, cr.Spec.Replsets)

			rs := cr.Spec.Replsets[0]
			rs.Size = 1
			rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests = corev1.ResourceList{
				corev1.ResourceStorage: requested,
			}
			cr.Spec.StorageScaling = &api.StorageScalingSpec{
				EnableVolumeScaling: tt.volumeScaling,
			}

			labels := naming.MongodLabels(cr, rs)
			sts := newStatefulSet(cr.Namespace, naming.MongodStatefulSetName(cr, rs), labels, configured)
			if tt.resizeInProgress {
				sts.Annotations = map[string]string{
					api.AnnotationPVCResizeInProgress: time.Now().Add(-time.Minute).Format(time.RFC3339),
				}
			}

			pod := newPod(cr.Namespace, sts.Name+"-0", labels)
			pvc := newPVC(cr.Namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, actual, actual)

			s := runtime.NewScheme()
			require.NoError(t, clientgoscheme.AddToScheme(s))
			require.NoError(t, apis.AddToScheme(s))

			r := &ReconcilePerconaServerMongoDB{
				client: fake.NewClientBuilder().WithScheme(s).WithRuntimeObjects(cr, sts, pod, pvc).Build(),
				scheme: s,
			}

			err = r.reconcilePVCs(t.Context(), cr, sts, labels, rs.VolumeSpec)
			if tt.expectErrContains != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.expectErrContains)
			} else {
				require.NoError(t, err)
			}

			fetchedSTS := &appsv1.StatefulSet{}
			err = r.client.Get(t.Context(), client.ObjectKeyFromObject(sts), fetchedSTS)
			if tt.expectSTSDeleted {
				assert.Error(t, err)
				assert.True(t, client.IgnoreNotFound(err) == nil)
			} else {
				assert.NoError(t, err)
			}

			fetchedCR := &api.PerconaServerMongoDB{}
			err = r.client.Get(t.Context(), client.ObjectKeyFromObject(cr), fetchedCR)
			require.NoError(t, err)
			if tt.expectCRStorage != "" {
				expected := resource.MustParse(tt.expectCRStorage)
				assert.Equal(t, expected, fetchedCR.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage])
			}
		})
	}
}

func TestReconcilePersistentVolumesExternalAutoscaling(t *testing.T) {
	const (
		namespace      = "test-ns"
		clusterName    = "test-cluster"
		configuredSize = "1Gi"
		requestedSize  = "2Gi"
	)

	scheme := runtime.NewScheme()
	require.NoError(t, clientgoscheme.AddToScheme(scheme))
	require.NoError(t, apis.AddToScheme(scheme))

	cr, err := readDefaultCR(clusterName, namespace)
	require.NoError(t, err)
	require.NotEmpty(t, cr.Spec.Replsets)

	rs := cr.Spec.Replsets[0]
	rs.Size = 1
	rs.VolumeSpec.PersistentVolumeClaim.Resources.Requests = corev1.ResourceList{
		corev1.ResourceStorage: resource.MustParse(requestedSize),
	}

	labels := naming.MongodLabels(cr, rs)
	sts := newStatefulSet(namespace, naming.MongodStatefulSetName(cr, rs), labels, resource.MustParse(configuredSize))
	pod := newPod(namespace, sts.Name+"-0", labels)
	pvc := newPVC(namespace, config.MongodDataVolClaimName+"-"+pod.Name, labels, resource.MustParse(configuredSize), resource.MustParse(configuredSize))

	tests := map[string]struct {
		externalAutoscaling bool
		volumeExpansion     bool
		expectRequestedSize string
		expectPVCSize       string
		expectSTSAnnotation bool
	}{
		"external autoscaling enabled, volume expansion enabled": {
			externalAutoscaling: true,
			volumeExpansion:     true,
			expectRequestedSize: requestedSize,
			expectPVCSize:       configuredSize,
		},
		"external autoscaling enabled, volume expansion disabled": {
			externalAutoscaling: true,
			volumeExpansion:     false,
			expectRequestedSize: requestedSize,
			expectPVCSize:       configuredSize,
		},
		"external autoscaling disabled, volume expansion enabled": {
			externalAutoscaling: false,
			volumeExpansion:     true,
			expectRequestedSize: requestedSize,
			expectPVCSize:       requestedSize,
			expectSTSAnnotation: true,
		},
		"external autoscaling disabled, volume expansion disabled": {
			externalAutoscaling: false,
			volumeExpansion:     false,
			expectRequestedSize: configuredSize,
			expectPVCSize:       configuredSize,
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			ctx := t.Context()

			cr := cr.DeepCopy()
			sts := sts.DeepCopy()
			pvc := pvc.DeepCopy()
			pod := pod.DeepCopy()

			cr.Spec.StorageScaling = &api.StorageScalingSpec{
				EnableExternalAutoscaling: tt.externalAutoscaling,
				EnableVolumeScaling:       tt.volumeExpansion,
			}

			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(cr, sts, pvc, pod).
				Build()

			r := &ReconcilePerconaServerMongoDB{
				client: cl,
				scheme: scheme,
			}

			err := r.reconcilePVCs(ctx, cr, sts, labels, cr.Spec.Replsets[0].VolumeSpec)
			require.NoError(t, err)

			gotSTS := new(appsv1.StatefulSet)
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(sts), gotSTS))
			_, hasResizeAnnotation := gotSTS.Annotations[api.AnnotationPVCResizeInProgress]
			assert.Equal(t, tt.expectSTSAnnotation, hasResizeAnnotation)

			requestedSize := cr.Spec.Replsets[0].VolumeSpec.PersistentVolumeClaim.Resources.Requests[corev1.ResourceStorage]
			assert.Zero(t, requestedSize.Cmp(resource.MustParse(tt.expectRequestedSize)))

			gotPVC := new(corev1.PersistentVolumeClaim)
			require.NoError(t, cl.Get(ctx, client.ObjectKeyFromObject(pvc), gotPVC))
			pvcSize := gotPVC.Spec.Resources.Requests[corev1.ResourceStorage]
			assert.Zero(t, pvcSize.Cmp(resource.MustParse(tt.expectPVCSize)))
		})
	}
}

func newStatefulSet(namespace, name string, labels map[string]string, storage resource.Quantity) *appsv1.StatefulSet {
	return &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{
				{
					ObjectMeta: metav1.ObjectMeta{
						Name: config.MongodDataVolClaimName,
					},
					Spec: corev1.PersistentVolumeClaimSpec{
						Resources: corev1.VolumeResourceRequirements{
							Requests: corev1.ResourceList{
								corev1.ResourceStorage: storage,
							},
						},
					},
				},
			},
		},
	}
}

func newPod(namespace, name string, labels map[string]string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
	}
}

func newPVC(namespace, name string, labels map[string]string, requested, capacity resource.Quantity) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: requested,
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceStorage: capacity,
			},
		},
	}
}
