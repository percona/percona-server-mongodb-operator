package backup

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
)

func BackupFromTask(cr *api.PerconaServerMongoDB, task *api.BackupTaskSpec) (*api.PerconaServerMongoDBBackup, error) {
	shortClusterName := cr.Name
	if len(shortClusterName) > 16 {
		shortClusterName = shortClusterName[:16]
	}
	backupType := defs.LogicalBackup
	if len(task.Type) > 0 {
		backupType = task.Type
	}
	finalizers := []string{naming.FinalizerDeleteBackup}
	if r := task.GetRetention(cr); !r.DeleteFromStorage {
		finalizers = []string{}
	}
	backupCr := &api.PerconaServerMongoDBBackup{
		APIVersion:   api.SchemeGroupVersion.String(),
		Kind:         "PerconaServerMongoDBBackup",
		Finalizers:   finalizers,
		GenerateName: "cron-" + shortClusterName + "-" + time.Now().Format("20060102150405") + "-",
		Labels:       naming.ScheduledBackupLabels(cr, task),
		Spec: api.PerconaServerMongoDBBackupSpec{
			Type:                backupType,
			ClusterName:         cr.Name,
			StorageName:         task.StorageName,
			Compression:         task.CompressionType,
			CompressionLevel:    task.CompressionLevel,
			VolumeSnapshotClass: task.VolumeSnapshotClass,
		},
	}
	if err := backupCr.CheckFields(); err != nil {
		return nil, err
	}
	return backupCr, nil
}
