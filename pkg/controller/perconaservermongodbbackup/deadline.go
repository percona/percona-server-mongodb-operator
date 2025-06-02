package perconaservermongodbbackup

import (
	"context"
	"time"

	"github.com/pkg/errors"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
)

func checkStartingDeadline(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, cr *psmdbv1.PerconaServerMongoDBBackup) error {
	log := logf.FromContext(ctx)

	if cr.Status.State != psmdbv1.BackupStateNew {
		return nil
	}

	var deadlineSeconds *int64
	if cr.Spec.StartingDeadlineSeconds != nil {
		deadlineSeconds = cr.Spec.StartingDeadlineSeconds
	} else if cluster.Spec.Backup.StartingDeadlineSeconds != nil {
		deadlineSeconds = cluster.Spec.Backup.StartingDeadlineSeconds
	}

	if deadlineSeconds == nil {
		return nil
	}

	since := time.Since(cr.CreationTimestamp.Time).Seconds()
	if since < float64(*deadlineSeconds) {
		return nil
	}

	log.Info("Backup didn't start in startingDeadlineSeconds, failing the backup",
		"startingDeadlineSeconds", *deadlineSeconds,
		"passedSeconds", since)

	return errors.New("starting deadline seconds exceeded")
}
