package backup

import (
	"strconv"
	"testing"

	"github.com/Percona-Lab/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"

	"github.com/stretchr/testify/assert"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewBackupCronJobContainerArgs(t *testing.T) {
	// file mode
	t.Run("file", func(t *testing.T) {
		c := &Controller{
			psmdb: &v1alpha1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: t.Name(),
				},
			},
		}
		args := c.newBackupCronJobContainerArgs(&v1alpha1.BackupTaskSpec{
			Name:            "test",
			DestinationType: v1alpha1.BackupDestinationFile,
		})
		assert.Equal(t, []string{
			"run",
			"backup",
			"--description=" + t.Name() + "-test",
			"--destination-type=file",
		}, args)
	})

	// s3 mode
	t.Run("s3", func(t *testing.T) {
		c := &Controller{
			psmdb: &v1alpha1.PerconaServerMongoDB{
				ObjectMeta: metav1.ObjectMeta{
					Name: t.Name(),
				},
			},
		}
		args := c.newBackupCronJobContainerArgs(&v1alpha1.BackupTaskSpec{
			Name:            "test",
			DestinationType: v1alpha1.BackupDestinationS3,
		})
		assert.Equal(t, []string{
			"run",
			"backup",
			"--description=" + t.Name() + "-test",
			"--destination-type=aws",
		}, args)
	})
}

func TestNewBackupCronJobContainerEnv(t *testing.T) {
	c := &Controller{
		psmdb: &v1alpha1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      t.Name(),
				Namespace: "test",
			},
		},
	}
	env := c.newBackupCronJobContainerEnv()
	assert.Len(t, env, 1)
	assert.Equal(t, env[0].Name, "PBMCTL_SERVER_ADDRESS")
	assert.Equal(t, env[0].Value, t.Name()+"-backup-coordinator.test.svc.cluster.local:"+strconv.Itoa(int(coordinatorAPIPort)))
}
