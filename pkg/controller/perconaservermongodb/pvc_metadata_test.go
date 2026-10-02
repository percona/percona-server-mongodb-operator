package perconaservermongodb

import (
	"context"
	"maps"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/config"
)

var _ = Describe("PersistentVolumeClaim metadata reconciliation", func() {
	ctx := context.Background()

	It("updates labels and annotations on existing PVCs", func() {
		const namespaceName = "pvc-metadata-update"

		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, namespace)
		})

		cr, err := readDefaultCR("pvc-metadata", namespaceName)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Spec.Replsets).NotTo(BeEmpty())

		replset := cr.Spec.Replsets[0]
		replset.Size = 1
		replset.VolumeSpec.PersistentVolumeClaim.Labels = map[string]string{
			"custom.percona.com/volume-label": "desired",
		}
		replset.VolumeSpec.PersistentVolumeClaim.Annotations = map[string]string{
			"custom.percona.com/volume-annotation": "desired",
		}

		selectorLabels := naming.MongodLabels(cr, replset)
		sts := newStatefulSet(namespaceName, naming.MongodStatefulSetName(cr, replset), selectorLabels, resource.MustParse("1Gi"))
		pvc := existingPVC(namespaceName, config.MongodDataVolClaimName+"-"+sts.Name+"-0", selectorLabels)
		pvc.Labels["custom.percona.com/existing-label"] = "kept"
		pvc.Annotations = map[string]string{
			"custom.percona.com/existing-annotation": "kept",
		}

		Expect(k8sClient.Create(ctx, sts)).To(Succeed())
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())

		Expect(reconciler().reconcilePVCs(ctx, cr, sts, selectorLabels, replset.VolumeSpec)).To(Succeed())

		updated := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pvc), updated)).To(Succeed())
		Expect(updated.Labels).To(HaveKeyWithValue("custom.percona.com/existing-label", "kept"))
		Expect(updated.Labels).To(HaveKeyWithValue("custom.percona.com/volume-label", "desired"))
		Expect(updated.Labels).To(HaveKeyWithValue(naming.LabelKubernetesInstance, cr.Name))
		Expect(updated.Annotations).To(HaveKeyWithValue("custom.percona.com/existing-annotation", "kept"))
		Expect(updated.Annotations).To(HaveKeyWithValue("custom.percona.com/volume-annotation", "desired"))
	})

	It("adds annotations to existing PVCs without annotations", func() {
		const namespaceName = "pvc-metadata-add-annotations"

		namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespaceName}}
		Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
		DeferCleanup(func() {
			_ = k8sClient.Delete(ctx, namespace)
		})

		cr, err := readDefaultCR("pvc-metadata-annotations", namespaceName)
		Expect(err).NotTo(HaveOccurred())
		Expect(cr.Spec.Replsets).NotTo(BeEmpty())

		replset := cr.Spec.Replsets[0]
		replset.Size = 1
		replset.VolumeSpec.PersistentVolumeClaim.Annotations = map[string]string{
			"custom.percona.com/volume-annotation": "desired",
		}

		selectorLabels := naming.MongodLabels(cr, replset)
		sts := newStatefulSet(namespaceName, naming.MongodStatefulSetName(cr, replset), selectorLabels, resource.MustParse("1Gi"))
		pvc := existingPVC(namespaceName, config.MongodDataVolClaimName+"-"+sts.Name+"-0", selectorLabels)

		Expect(k8sClient.Create(ctx, sts)).To(Succeed())
		Expect(k8sClient.Create(ctx, pvc)).To(Succeed())

		Expect(reconciler().reconcilePVCs(ctx, cr, sts, selectorLabels, replset.VolumeSpec)).To(Succeed())

		updated := &corev1.PersistentVolumeClaim{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pvc), updated)).To(Succeed())
		Expect(updated.Annotations).To(HaveKeyWithValue("custom.percona.com/volume-annotation", "desired"))
	})
})

func existingPVC(namespace, name string, labels map[string]string) *corev1.PersistentVolumeClaim {
	pvcLabels := make(map[string]string, len(labels))
	maps.Copy(pvcLabels, labels)

	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    pvcLabels,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: resource.MustParse("1Gi"),
				},
			},
		},
	}
}
