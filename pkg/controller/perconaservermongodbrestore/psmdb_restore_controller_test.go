package perconaservermongodbrestore

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

var _ = Describe("PerconaServerMongoDB", Ordered, func() {
	ctx := context.Background()

	const ns = "psmdb-restore"
	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ns,
			Namespace: ns,
		},
	}

	BeforeAll(func() {
		By("Creating the Namespace to perform the tests")
		err := k8sClient.Create(ctx, namespace)
		Expect(err).To(Not(HaveOccurred()))
	})

	AfterAll(func() {
		By("Deleting the Namespace to perform the tests")
		_ = k8sClient.Delete(ctx, namespace)
	})

	Context("Check '.spec.pitr.type' validation rule", func() {
		cr := readDefaultRestore(GinkgoT(), "type-validation", ns)
		It("Should not create restore", func() {
			cr.Spec.PITR = &psmdbv1.PITRestoreSpec{
				Type: "latest",
				Date: &psmdbv1.PITRestoreDate{Time: metav1.Now()},
			}

			err := k8sClient.Create(ctx, cr)
			Expect(err).NotTo(BeNil())
			Expect(err.Error()).To(Equal(`PerconaServerMongoDBRestore.psmdb.percona.com "type-validation" is invalid: spec.pitr: Invalid value: "object": Date should not be used when 'latest' type is used`))
		})
		It("Should create restore", func() {
			cr.Spec.PITR = &psmdbv1.PITRestoreSpec{
				Type: "date",
				Date: &psmdbv1.PITRestoreDate{
					Time: metav1.Now(),
				},
			}
			Expect(k8sClient.Create(ctx, cr)).Should(Succeed())
		})
	})
	Context("Check '.spec.pitr.date' validation rule", func() {
		cr := readDefaultRestore(GinkgoT(), "date-validation", ns)
		var obj *unstructured.Unstructured
		It("Should not create restore", func() {
			cr.Spec.PITR = &psmdbv1.PITRestoreSpec{
				Type: "date",
				Date: &psmdbv1.PITRestoreDate{
					Time: metav1.Now(),
				},
			}

			m, err := runtime.DefaultUnstructuredConverter.ToUnstructured(cr)
			Expect(err).To(Succeed())

			obj = &unstructured.Unstructured{Object: m}
			Expect(unstructured.SetNestedField(obj.Object, "2020-01-01", "spec", "pitr", "date")).To(Succeed())

			err = k8sClient.Create(ctx, obj)
			Expect(err.Error()).To(Equal(`PerconaServerMongoDBRestore.psmdb.percona.com "date-validation" is invalid: spec.pitr: Invalid value: "object": Time should be in format YYYY-MM-DD HH:MM:SS with valid ranges (MM: 01-12, DD: 01-31, HH: 00-23, MM/SS: 00-59)`))

			obj = &unstructured.Unstructured{Object: m}
			Expect(unstructured.SetNestedField(obj.Object, "1992-13-32 25:63:64", "spec", "pitr", "date")).To(Succeed())

			err = k8sClient.Create(ctx, obj)
			Expect(err.Error()).To(Equal(`PerconaServerMongoDBRestore.psmdb.percona.com "date-validation" is invalid: spec.pitr: Invalid value: "object": Time should be in format YYYY-MM-DD HH:MM:SS with valid ranges (MM: 01-12, DD: 01-31, HH: 00-23, MM/SS: 00-59)`))
		})
		It("Should create restore", func() {
			Expect(unstructured.SetNestedField(obj.Object, "2020-01-01 10:10:10", "spec", "pitr", "date")).To(Succeed())

			Expect(k8sClient.Create(ctx, obj)).To(Succeed())
		})
	})
})
