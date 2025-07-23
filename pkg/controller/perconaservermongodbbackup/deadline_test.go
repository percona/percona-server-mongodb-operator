package perconaservermongodbbackup

import (
	"context"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	_ "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/yaml"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

var _ = Describe("Starting deadline", func() {
	It("should be optional", func() {
		cluster, err := readDefaultCR("cluster1", "test")
		Expect(err).ToNot(HaveOccurred())

		bcp, err := readDefaultBackup("backup1", "test")
		Expect(err).ToNot(HaveOccurred())

		cluster.Spec.Backup.StartingDeadlineSeconds = nil

		bcp.Spec.StartingDeadlineSeconds = nil
		bcp.Status.State = psmdbv1.BackupStateNew

		err = checkStartingDeadline(context.Background(), cluster, bcp)
		Expect(err).ToNot(HaveOccurred())
	})

	It("should use universal value if defined", func() {
		cluster, err := readDefaultCR("cluster1", "test")
		Expect(err).ToNot(HaveOccurred())

		bcp, err := readDefaultBackup("backup1", "test")
		Expect(err).ToNot(HaveOccurred())

		cluster.Spec.Backup.StartingDeadlineSeconds = ptr.To(int64(60))

		bcp.Status.State = psmdbv1.BackupStateNew
		bcp.CreationTimestamp = metav1.NewTime(time.Now().Add(-2 * time.Minute))

		err = checkStartingDeadline(context.Background(), cluster, bcp)
		Expect(err).To(HaveOccurred())
	})

	It("should use particular value if defined", func() {
		cluster, err := readDefaultCR("cluster1", "test")
		Expect(err).ToNot(HaveOccurred())

		bcp, err := readDefaultBackup("backup1", "test")
		Expect(err).ToNot(HaveOccurred())

		cluster.Spec.Backup.StartingDeadlineSeconds = ptr.To(int64(600))

		bcp.Status.State = psmdbv1.BackupStateNew
		bcp.CreationTimestamp = metav1.NewTime(time.Now().Add(-2 * time.Minute))
		bcp.Spec.StartingDeadlineSeconds = ptr.To(int64(60))

		err = checkStartingDeadline(context.Background(), cluster, bcp)
		Expect(err).To(HaveOccurred())
	})

	It("should not return an error", func() {
		cluster, err := readDefaultCR("cluster1", "test")
		Expect(err).ToNot(HaveOccurred())

		bcp, err := readDefaultBackup("backup1", "test")
		Expect(err).ToNot(HaveOccurred())

		cluster.Spec.Backup.StartingDeadlineSeconds = ptr.To(int64(600))

		bcp.Status.State = psmdbv1.BackupStateNew
		bcp.CreationTimestamp = metav1.NewTime(time.Now().Add(-2 * time.Minute))
		bcp.Spec.StartingDeadlineSeconds = ptr.To(int64(300))

		err = checkStartingDeadline(context.Background(), cluster, bcp)
		Expect(err).ToNot(HaveOccurred())
	})
})

func readDefaultCR(name, namespace string) (*psmdbv1.PerconaServerMongoDB, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "cr.yaml"))
	if err != nil {
		return nil, err
	}

	cr := &psmdbv1.PerconaServerMongoDB{}

	if err := yaml.Unmarshal(data, cr); err != nil {
		return nil, err
	}

	cr.Name = name
	cr.Namespace = namespace
	cr.Spec.InitImage = "perconalab/percona-server-mongodb-operator:main"
	return cr, nil
}

func readDefaultBackup(name, namespace string) (*psmdbv1.PerconaServerMongoDBBackup, error) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "deploy", "backup", "backup.yaml"))
	if err != nil {
		return nil, err
	}

	cr := &psmdbv1.PerconaServerMongoDBBackup{}

	if err := yaml.Unmarshal(data, cr); err != nil {
		return cr, err
	}

	cr.Name = name
	cr.Namespace = namespace

	return cr, nil
}
