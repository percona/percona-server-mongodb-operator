package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	gomock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/rand"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/percona/percona-server-mongodb-operator/pkg/apis"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/mongo/mocks"
	"github.com/percona/percona-server-mongodb-operator/version"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
	namespace string
)

func TestReconcile(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t, "PerconaServerMongoDB Controller Suite", []Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:     []string{filepath.Join("..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing: true,
	}

	namespace = "psmdb-envtest-" + rand.String(4)
	err := os.Setenv("WATCH_NAMESPACE", namespace)
	Expect(err).NotTo(HaveOccurred())

	cfg, err := testEnv.Start()
	Expect(err).NotTo(HaveOccurred())
	Expect(cfg).NotTo(BeNil())

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())
	Expect(k8sClient).NotTo(BeNil())

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{Scheme: scheme.Scheme})
	Expect(err).NotTo(HaveOccurred())

	err = apis.AddToScheme(mgr.GetScheme())
	Expect(err).NotTo(HaveOccurred())

	err = AddToManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	err = k8sClient.Create(context.TODO(), &ns)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(context.TODO())
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")

	ns := corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: namespace,
		},
	}
	err := k8sClient.Delete(context.TODO(), &ns)
	Expect(err).NotTo(HaveOccurred())

	err = testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("PerconaServerMongoDB controller", func() {
	ctrl := gomock.NewController(GinkgoT())
	mock_client := mock_mongo.NewMockClient(ctrl)

	It("Should create PerconaServerMongoDB", func() {
		cr := &psmdbv1.PerconaServerMongoDB{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "psmdb.percona.com/v1",
				Kind:       "PerconaServerMongoDB",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cr",
				Namespace: namespace,
			},
			Spec: psmdbv1.PerconaServerMongoDBSpec{
				CRVersion: version.Version,
				Image:     "percona/percona-server-mongodb:5.0.11-10",
				Sharding:  psmdbv1.Sharding{Enabled: false},
				Secrets: &psmdbv1.SecretsSpec{
					Users: "test-cr-users",
				},
				Replsets: []*psmdbv1.ReplsetSpec{
					{
						Name: "rs0",
						Size: 3,
						VolumeSpec: &psmdbv1.VolumeSpec{
							PersistentVolumeClaim: &corev1.PersistentVolumeClaimSpec{
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										"storage": resource.MustParse("1G"),
									},
								},
							},
						},
					},
				},
			},
		}

		Expect(k8sClient.Create(context.TODO(), cr)).Should(Succeed())
	})
	It("Should create replset", func() {
		sts := &appsv1.StatefulSet{}
		nn := types.NamespacedName{Namespace: namespace, Name: "test-cr-rs0"}

		Eventually(func() bool {
			return k8sClient.Get(context.TODO(), nn, sts) == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())
	})
	It("Should create mongod service", func() {
		svc := &corev1.Service{}
		nn := types.NamespacedName{Namespace: namespace, Name: "test-cr-rs0"}

		Eventually(func() bool {
			return k8sClient.Get(context.TODO(), nn, svc) == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())
	})
	It("Should create users secret", func() {
		secret := &corev1.Secret{}
		nn := types.NamespacedName{Namespace: namespace, Name: "test-cr-users"}

		Eventually(func() bool {
			return k8sClient.Get(context.TODO(), nn, secret) == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())
	})
	It("Should create internal users secret", func() {
		secret := &corev1.Secret{}
		nn := types.NamespacedName{Namespace: namespace, Name: "internal-test-cr-users"}

		Eventually(func() bool {
			return k8sClient.Get(context.TODO(), nn, secret) == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())
	})
	It("Should create TLS secret", func() {
		secret := &corev1.Secret{}
		nn := types.NamespacedName{Namespace: namespace, Name: "test-cr-ssl"}

		Eventually(func() bool {
			return k8sClient.Get(context.TODO(), nn, secret) == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())
	})
	It("Should create internal TLS secret", func() {
		secret := &corev1.Secret{}
		nn := types.NamespacedName{Namespace: namespace, Name: "test-cr-ssl-internal"}

		Eventually(func() bool {
			return k8sClient.Get(context.TODO(), nn, secret) == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())
	})
	It("Should reconcile pods", func() {
		Eventually(func() bool {
			podList := &corev1.PodList{}
			err := k8sClient.List(context.TODO(), podList, &client.ListOptions{
				Namespace:     namespace,
				LabelSelector: labels.SelectorFromSet(map[string]string{"app.kubernetes.io/instance": "test-cr"})},
			)
			Expect(err).NotTo(HaveOccurred())
			return len(podList.Items) == 3
		}, time.Second*120, time.Millisecond*250).Should(BeTrue())
	})
	It("Should create PVCs", func() {
		pvcList := &corev1.PersistentVolumeClaimList{}

		err := k8sClient.List(context.TODO(), pvcList, &client.ListOptions{
			Namespace:     namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{"app.kubernetes.io/instance": "test-cr"})},
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(len(pvcList.Items)).To(Equal(3))
	})
	It("Should initialize the replset", func() {
		Eventually(func() bool {
			cr := &psmdbv1.PerconaServerMongoDB{}

			nn := types.NamespacedName{Namespace: namespace, Name: "test-cr"}
			err := k8sClient.Get(context.TODO(), nn, cr)
			Expect(err).NotTo(HaveOccurred())

			return cr.Status.Replsets["rs0"].Initialized
		}, time.Second*30, time.Second*1).Should(BeTrue())

		mock_client.EXPECT().GetRole(gomock.Any(), gomock.Any()).MinTimes(1)
		mock_client.EXPECT().CreateRole(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).MinTimes(1)
	})
})
