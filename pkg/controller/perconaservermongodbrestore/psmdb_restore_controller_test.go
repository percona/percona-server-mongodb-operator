package perconaservermongodbrestore

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmVersion "github.com/percona/percona-backup-mongodb/pbm/version"

	fakeClientcmd "github.com/percona/percona-server-mongodb-operator/clientcmd/fake"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func TestReconcileValidationError(t *testing.T) {
	t.Parallel()

	ns := "reconcile-validation"
	clusterName := ns + "-cr"
	backupName := ns + "-backup"
	restoreName := ns + "-restore"
	secretName := ns + "-secret"
	storageName := ns + "-stg"

	secret := &corev1.Secret{
		Name:      secretName,
		Namespace: ns,
	}

	cluster := readDefaultCluster(t, clusterName, ns)
	cluster.Spec.Backup.Storages = map[string]psmdbv1.BackupStorageSpec{
		storageName: {
			Type: psmdbv1.BackupStorageS3,
			S3: psmdbv1.BackupStorageS3Spec{
				Bucket:            "some-bucket",
				Prefix:            "some-prefix",
				Region:            "some-region",
				EndpointURL:       "some-endpoint",
				CredentialsSecret: secretName,
			},
		},
	}
	cluster.Status.BackupVersion = pbmVersion.Current().Version

	bcp := &psmdbv1.PerconaServerMongoDBBackup{
		Name:      backupName,
		Namespace: ns,
		Spec: psmdbv1.PerconaServerMongoDBBackupSpec{
			Type:        defs.LogicalBackup,
			ClusterName: clusterName,
			StorageName: storageName,
		},
		Status: psmdbv1.PerconaServerMongoDBBackupStatus{
			State: psmdbv1.BackupStateReady,
			Type:  defs.LogicalBackup,
			LastWriteAt: &metav1.Time{
				Time: mustParseTime(time.RFC3339, "2026-11-12T10:27:02Z"),
			},
		},
	}

	restore := &psmdbv1.PerconaServerMongoDBRestore{
		Name:      restoreName,
		Namespace: ns,
		Spec: psmdbv1.PerconaServerMongoDBRestoreSpec{
			BackupName:  backupName,
			ClusterName: clusterName,
			PITR: &psmdbv1.PITRestoreSpec{
				Type: psmdbv1.PITRestoreTypeDate,
				Date: &psmdbv1.PITRestoreDate{
					Time: metav1.Time{
						Time: mustParseTime(time.RFC3339, "2020-01-01T00:00:00Z"),
					},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/version" {
			http.NotFound(w, req)
			return
		}
		_, _ = w.Write([]byte(`{"gitVersion":"v1.34.0"}`))
	}))
	t.Cleanup(srv.Close)

	restClient, err := rest.RESTClientFor(&rest.Config{
		Host:                 srv.URL,
		GroupVersion:         &metav1.SchemeGroupVersion,
		NegotiatedSerializer: scheme.Codecs.WithoutConversion(),
	})
	require.NoError(t, err)

	r := fakeReconciler(secret, restore, cluster, bcp)
	r.clientcmd = &fakeClientcmd.Client{
		RESTFunc: func() rest.Interface { return restClient },
	}

	_, err = r.Reconcile(t.Context(), reconcile.Request{NamespacedName: client.ObjectKeyFromObject(restore)})
	require.EqualError(t, err, "failed to validate restore: backup's last write is later than target time")

	got := new(psmdbv1.PerconaServerMongoDBRestore)
	require.NoError(t, r.client.Get(t.Context(), client.ObjectKeyFromObject(restore), got))
	assert.Equal(t, psmdbv1.RestoreStateError, got.Status.State)
	assert.Equal(t, "backup's last write is later than target time", got.Status.Error)
}

var _ = Describe("PerconaServerMongoDBRestore CEL validations", Ordered, func() {
	ctx := context.Background()

	const ns = "psmdb-restore"
	namespace := &corev1.Namespace{
		Name:      ns,
		Namespace: ns,
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

	Context("Selective restore validations", func() {
		cr := readDefaultRestore(GinkgoT(), "ns-validation", ns)

		It("should not create restore when only nsFrom is set", func() {
			cr.Spec.Selective = &psmdbv1.SelectiveRestoreOpts{
				NamespaceFrom: "ns.col",
			}

			err := k8sClient.Create(ctx, cr)
			Expect(err.Error()).To(ContainSubstring("nsFrom and nsTo need to be set together"))
		})

		It("should not create restore when only nsTo is set", func() {
			cr.Spec.Selective = &psmdbv1.SelectiveRestoreOpts{
				NamespaceTo: "ns.col",
			}

			err := k8sClient.Create(ctx, cr)
			Expect(err.Error()).To(ContainSubstring("nsFrom and nsTo need to be set together"))
		})

		It("should not create restore when nsFrom and nsTo is equal", func() {
			cr.Spec.Selective = &psmdbv1.SelectiveRestoreOpts{
				NamespaceFrom: "ns.col",
				NamespaceTo:   "ns.col",
			}

			err := k8sClient.Create(ctx, cr)
			Expect(err.Error()).To(ContainSubstring("nsFrom and nsTo can't be the same"))
		})

		It("should create restore without nsFrom and nsTo", func() {
			cr := readDefaultRestore(GinkgoT(), "ns-validation-empty", ns)
			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		})

		It("should create restore with nsFrom and nsTo", func() {
			cr := readDefaultRestore(GinkgoT(), "ns-validation-set", ns)
			cr.Spec.Selective = &psmdbv1.SelectiveRestoreOpts{
				NamespaceFrom: "ns1.col",
				NamespaceTo:   "ns2.col",
			}

			Expect(k8sClient.Create(ctx, cr)).To(Succeed())
		})
	})
})
