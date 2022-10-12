package perconaservermongodb

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
	"github.com/percona/percona-server-mongodb-operator/pkg/controller"
	"github.com/percona/percona-server-mongodb-operator/version"
	//+kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

var (
	cfg       *rest.Config
	k8sClient client.Client
	testEnv   *envtest.Environment
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"PerconaServerMongoDB Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func() {
	logf.SetLogger(zap.New(zap.WriteTo(GinkgoWriter), zap.UseDevMode(true)))

	By("bootstrapping test environment")
	t := true
	testEnv = &envtest.Environment{
		CRDDirectoryPaths:        []string{filepath.Join("..", "..", "..", "config", "crd", "bases")},
		ErrorIfCRDPathMissing:    true,
		UseExistingCluster:       &t,
		AttachControlPlaneOutput: true,
	}

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

	err = controller.AddToManager(mgr)
	Expect(err).NotTo(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = mgr.Start(context.TODO())
		Expect(err).ToNot(HaveOccurred(), "failed to run manager")
	}()
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	err := testEnv.Stop()
	Expect(err).NotTo(HaveOccurred())
})

var _ = Describe("PerconaServerMongoDB controller", func() {
	It("Should create PerconaServerMongoDB", func() {
		cr := &psmdbv1.PerconaServerMongoDB{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "psmdb.percona.com/v1",
				Kind:       "PerconaServerMongoDB",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-cr",
				Namespace: "default",
			},
			Spec: psmdbv1.PerconaServerMongoDBSpec{
				CRVersion: version.Version,
			},
		}

		Expect(k8sClient.Create(context.TODO(), cr)).Should(Succeed())
	})
	It("Should create replset", func() {
		sts := &appsv1.StatefulSet{}
		nn := types.NamespacedName{Namespace: "default", Name: "test-cr-rs0"}

		Eventually(func() bool {
			return k8sClient.Get(context.TODO(), nn, sts) == nil
		}, time.Second*10, time.Millisecond*250).Should(BeTrue())
	})
})
