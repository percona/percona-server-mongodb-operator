package perconaservermongodb

import (
	"container/heap"
	"context"
	"fmt"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"reflect"
	"strconv"

	"go.mongodb.org/mongo-driver/mongo"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/pkg/errors"
)

func (r *ReconcilePerconaServerMongoDB) reconcileBackupTasks(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	ctasks := make(map[string]api.BackupTaskSpec)
	ls := backup.NewBackupCronJobLabels(cr.Name, cr.Spec.Backup.Labels)

	for _, task := range cr.Spec.Backup.Tasks {
		cjob, err := backup.BackupCronJob(&task, cr.Name, cr.Namespace, cr.Spec.Backup, cr.Spec.ImagePullSecrets)
		if err != nil {
			return errors.Wrap(err, "can't create job")
		}
		ls = cjob.ObjectMeta.Labels
		if task.Enabled {
			ctasks[cjob.Name] = task

			err := setControllerReference(cr, &cjob, r.scheme)
			if err != nil {
				return errors.Wrap(err, "set owner reference for backup task "+cjob.Name)
			}

			err = r.createOrUpdate(ctx, &cjob)
			if err != nil {
				return errors.Wrap(err, "create or update backup job")
			}
		}
	}

	// Remove old/unused tasks
	tasksList := &batchv1beta1.CronJobList{}
	err := r.client.List(ctx,
		tasksList,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(ls),
		},
	)
	if err != nil {
		return fmt.Errorf("get backup list: %v", err)
	}

	for _, t := range tasksList.Items {
		if spec, ok := ctasks[t.Name]; ok {
			if spec.Keep > 0 {
				oldjobs, err := r.oldScheduledBackups(ctx, cr, t.Name, spec.Keep)
				if err != nil {
					return fmt.Errorf("remove old backups: %v", err)
				}

				for _, todel := range oldjobs {
					err = r.client.Delete(ctx, &todel)
					if err != nil {
						return fmt.Errorf("failed to delete backup object: %v", err)
					}
				}
			}
		} else {
			err := r.client.Delete(ctx, &t)
			if err != nil && !k8sErrors.IsNotFound(err) {
				return fmt.Errorf("delete backup task %s: %v", t.Name, err)
			}
		}
	}

	return nil
}

// oldScheduledBackups returns list of the most old psmdb-bakups that execeed `keep` limit
func (r *ReconcilePerconaServerMongoDB) oldScheduledBackups(ctx context.Context, cr *api.PerconaServerMongoDB,
	ancestor string, keep int) ([]api.PerconaServerMongoDBBackup, error) {
	bcpList := api.PerconaServerMongoDBBackupList{}
	err := r.client.List(ctx,
		&bcpList,
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				"cluster":  cr.Name,
				"ancestor": ancestor,
			}),
		},
	)
	if err != nil {
		return []api.PerconaServerMongoDBBackup{}, err
	}

	if len(bcpList.Items) <= keep {
		return []api.PerconaServerMongoDBBackup{}, nil
	}

	h := &minHeap{}
	heap.Init(h)
	for _, bcp := range bcpList.Items {
		if bcp.Status.State == api.BackupStateReady {
			heap.Push(h, bcp)
		}
	}

	if h.Len() <= keep {
		return []api.PerconaServerMongoDBBackup{}, nil
	}

	ret := make([]api.PerconaServerMongoDBBackup, 0, h.Len()-keep)
	for i := h.Len() - keep; i > 0; i-- {
		o := heap.Pop(h).(api.PerconaServerMongoDBBackup)
		ret = append(ret, o)
	}

	return ret, nil
}

type minHeap []api.PerconaServerMongoDBBackup

func (h minHeap) Len() int { return len(h) }

func (h minHeap) Less(i, j int) bool {
	return h[i].CreationTimestamp.Before(&h[j].CreationTimestamp)
}

func (h minHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }

func (h *minHeap) Push(x interface{}) {
	*h = append(*h, x.(api.PerconaServerMongoDBBackup))
}

func (h *minHeap) Pop() interface{} {
	old := *h
	n := len(old)
	x := old[n-1]
	*h = old[0 : n-1]
	return x
}

func (r *ReconcilePerconaServerMongoDB) isRestoreRunning(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	restores := api.PerconaServerMongoDBRestoreList{}
	if err := r.client.List(ctx, &restores, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "get restore list")
	}

	for _, rst := range restores.Items {
		if rst.Status.State != api.RestoreStateReady &&
			rst.Status.State != api.RestoreStateError &&
			rst.Spec.ClusterName == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) isBackupRunning(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	bcps := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(ctx, &bcps, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, bcp := range bcps.Items {
		if bcp.Status.State != api.BackupStateReady &&
			bcp.Status.State != api.BackupStateError &&
			bcp.Spec.ClusterName == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) hasFullBackup(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	backups := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(ctx, &backups, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, b := range backups.Items {
		if b.Status.State == api.BackupStateReady && b.Spec.ClusterName == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) updatePITR(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	if !cr.Spec.Backup.Enabled {
		return nil
	}

	// pitr is disabled right before restore so it must not be re-enabled during restore
	isRestoring, err := r.isRestoreRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "checking if restore running on pbm update")
	}

	if isRestoring || cr.Status.State != api.AppStateReady {
		return nil
	}

	pbm, err := backup.NewPBM(ctx, r.client, cr)
	if err != nil {
		return errors.Wrap(err, "create pbm object")
	}
	defer pbm.Close(ctx)

	if cr.Spec.Backup.PITR.Enabled {
		hasFullBackup, err := r.hasFullBackup(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "check full backup")
		}

		if !hasFullBackup {
			log.Info("Point-in-time recovery will work only with full backup. Please create one manually or wait for scheduled backup to be created (if configured).")
			return nil
		}
	}

	val, err := pbm.C.GetConfigVar("pitr.enabled")
	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return errors.Wrap(err, "get pitr.enabled")
		}

		return nil
	}

	enabled, ok := val.(bool)
	if !ok {
		return errors.Wrap(err, "unexpected value of pitr.enabled")
	}

	if enabled != cr.Spec.Backup.PITR.Enabled {
		val := strconv.FormatBool(cr.Spec.Backup.PITR.Enabled)
		if err := pbm.C.SetConfigVar("pitr.enabled", val); err != nil {
			return errors.Wrap(err, "update pitr.enabled")
		}
	}

	val, err = pbm.C.GetConfigVar("pitr.oplogSpanMin")
	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return errors.Wrap(err, "get pitr.oplogSpanMin")
		}

		return nil
	}

	oplogSpanMin, ok := val.(float64)
	if !ok {
		return errors.Wrap(err, "unexpected value of pitr.oplogSpanMin")
	}

	if oplogSpanMin != cr.Spec.Backup.PITR.OplogSpanMin {
		val := strconv.FormatFloat(cr.Spec.Backup.PITR.OplogSpanMin, 'f', -1, 64)
		if err := pbm.C.SetConfigVar("pitr.oplogSpanMin", val); err != nil {
			return errors.Wrap(err, "update pitr.oplogSpanMin")
		}
	}

	val, err = pbm.C.GetConfigVar("pitr.compression")
	var compression = ""
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
			return errors.Wrap(err, "get pitr.compression")
		}
	} else {
		compression, ok = val.(string)
		if !ok {
			return errors.Wrap(err, "unexpected value of pitr.compression")
		}
	}

	if compression != string(cr.Spec.Backup.PITR.Compression) {
		if string(cr.Spec.Backup.PITR.Compression) == "" {
			if err := pbm.C.DeleteConfigVar("pitr.compression"); err != nil {
				return errors.Wrap(err, "delete pitr.compression")
			}
		} else if err := pbm.C.SetConfigVar("pitr.compression", string(cr.Spec.Backup.PITR.Compression)); err != nil {
			return errors.Wrap(err, "update pitr.compression")
		}
	}

	val, err = pbm.C.GetConfigVar("pitr.compressionLevel")
	var compressionLevel *int64 = nil
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
			return errors.Wrap(err, "get pitr.compressionLevel")
		}
	} else {
		tmpCompressionLevel, ok := val.(int64)
		if !ok {
			return errors.Wrap(err, "unexpected value of pitr.compressionLevel")
		}
		compressionLevel = Int64(tmpCompressionLevel)
	}

	if !reflect.DeepEqual(compressionLevel, cr.Spec.Backup.PITR.CompressionLevel) {
		if cr.Spec.Backup.PITR.CompressionLevel == nil {
			if err := pbm.C.DeleteConfigVar("pitr.compressionLevel"); err != nil {
				return errors.Wrap(err, "delete pitr.compressionLevel")
			}
		} else if err := pbm.C.SetConfigVar("pitr.compressionLevel", strconv.FormatInt(*(cr.Spec.Backup.PITR.CompressionLevel), 10)); err != nil {
			return errors.Wrap(err, "update pitr.compressionLevel")
		}
	}

	return nil
}

// Int64 returns a pointer to the int64 value passed in.
func Int64(v int64) *int64 {
	return &v
}
