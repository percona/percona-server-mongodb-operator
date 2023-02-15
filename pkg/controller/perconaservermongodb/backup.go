package perconaservermongodb

import (
	"bytes"
	"container/heap"
	"context"
	"fmt"
	"reflect"
	"strconv"

	"github.com/robfig/cron/v3"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	batchv1beta1 "k8s.io/api/batch/v1beta1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/pkg/errors"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

type BackupScheduleJob struct {
	api.BackupTaskSpec
	JobID cron.EntryID
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupTasks(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	ctasks := make(map[string]api.BackupTaskSpec)

	for _, task := range cr.Spec.Backup.Tasks {
		if !task.Enabled {
			continue
		}
		_, ok := cr.Spec.Backup.Storages[task.StorageName]
		if !ok {
			return errors.Errorf("there is no storage %s in cluster %s for %s task", task.StorageName, cr.Name, task.Name)
		}
		ctasks[task.Name] = task

		if err := r.createOrUpdateBackupTask(ctx, cr, task); err != nil {
			return err
		}
	}

	// Remove old/unused tasks
	if err := r.deleteOldBackupTasks(ctx, cr, ctasks); err != nil {
		return err
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createOrUpdateBackupTask(ctx context.Context, cr *api.PerconaServerMongoDB, task api.BackupTaskSpec) error {
	if cr.CompareVersion("1.13.0") < 0 {
		cjob, err := backup.BackupCronJob(cr, &task)
		if err != nil {
			return errors.Wrap(err, "can't create job")
		}
		err = setControllerReference(cr, &cjob, r.scheme)
		if err != nil {
			return errors.Wrapf(err, "set owner reference for backup task %s", cjob.Name)
		}

		err = r.createOrUpdate(ctx, &cjob)
		if err != nil {
			return errors.Wrap(err, "create or update backup job")
		}
		return nil
	}
	t := BackupScheduleJob{}
	bj, ok := r.crons.backupJobs.Load(task.JobName(cr))
	if ok {
		t = bj.(BackupScheduleJob)
	}

	if !ok || t.Schedule != task.Schedule || t.StorageName != task.StorageName {
		logf.FromContext(ctx).Info("Creating or updating backup job", "name", task.Name, "namespace", cr.Namespace, "schedule", task.Schedule)

		r.deleteBackupTask(cr, t.BackupTaskSpec)
		jobID, err := r.crons.crons.AddFunc(task.Schedule, r.createBackupTask(ctx, cr, task))
		if err != nil {
			return errors.Wrapf(err, "can't parse cronjob schedule for backup %s with schedule %s", task.Name, task.Schedule)
		}

		r.crons.backupJobs.Store(task.JobName(cr), BackupScheduleJob{
			BackupTaskSpec: task,
			JobID:          jobID,
		})
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteOldBackupTasks(ctx context.Context, cr *api.PerconaServerMongoDB, ctasks map[string]api.BackupTaskSpec) error {
	log := logf.FromContext(ctx)

	if cr.CompareVersion("1.13.0") < 0 {
		ls := backup.NewBackupCronJobLabels(cr.Name, cr.Spec.Backup.Labels)
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
	r.crons.backupJobs.Range(func(k, v interface{}) bool {
		item := v.(BackupScheduleJob)
		if spec, ok := ctasks[item.Name]; ok {
			if spec.Keep > 0 {
				oldjobs, err := r.oldScheduledBackups(ctx, cr, item.Name, spec.Keep)
				if err != nil {
					log.Error(err, "failed to list old backups", "job", item.Name)
					return true
				}

				for _, todel := range oldjobs {
					err = r.client.Delete(ctx, &todel)
					if err != nil {
						log.Error(err, "failed to delete backup object")
						return true
					}
				}

			}
		} else {
			log.Info("deleting outdated backup job", "job", item.Name)
			r.deleteBackupTask(cr, item.BackupTaskSpec)
		}

		return true
	})
	return nil
}

func (r *ReconcilePerconaServerMongoDB) createBackupTask(ctx context.Context, cr *api.PerconaServerMongoDB, task api.BackupTaskSpec) func() {
	log := logf.FromContext(ctx)

	return func() {
		localCr := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, localCr)
		if k8sErrors.IsNotFound(err) {
			log.Info("cluster is not found, deleting the job", "job", task.Name)
			r.deleteBackupTask(cr, task)
			return
		}
		bcp, err := backup.BackupFromTask(cr, &task)
		if err != nil {
			log.Error(err, "failed to create backup")
			return
		}
		bcp.Namespace = cr.Namespace
		err = r.client.Create(ctx, bcp)
		if err != nil {
			log.Error(err, "failed to create backup")
		}
	}
}

func (r *ReconcilePerconaServerMongoDB) deleteBackupTask(cr *api.PerconaServerMongoDB, task api.BackupTaskSpec) {
	job, ok := r.crons.backupJobs.LoadAndDelete(task.JobName(cr))
	if !ok {
		return
	}
	r.crons.crons.Remove(job.(BackupScheduleJob).JobID)
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
			bcp.Spec.GetClusterName() == cr.Name {
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
		if b.Status.State == api.BackupStateReady && b.Spec.GetClusterName() == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) updatePITR(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

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
		log.Info("Setting pitr.enabled in PBM config", "enabled", val)
		if err := pbm.C.SetConfigVar("pitr.enabled", val); err != nil {
			return errors.Wrap(err, "update pitr.enabled")
		}
	}

	if !cr.Spec.Backup.PITR.Enabled {
		return nil
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

	if oplogSpanMin != cr.Spec.Backup.PITR.OplogSpanMin.Float64() {
		val := cr.Spec.Backup.PITR.OplogSpanMin.String()
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

	if compression != string(cr.Spec.Backup.PITR.CompressionType) {
		if string(cr.Spec.Backup.PITR.CompressionType) == "" {
			if err := pbm.C.DeleteConfigVar("pitr.compression"); err != nil {
				return errors.Wrap(err, "delete pitr.compression")
			}
		} else if err := pbm.C.SetConfigVar("pitr.compression", string(cr.Spec.Backup.PITR.CompressionType)); err != nil {
			return errors.Wrap(err, "update pitr.compression")
		}

		// PBM needs to disabling and enabling PITR to change compression type
		if err := pbm.C.SetConfigVar("pitr.enabled", "false"); err != nil {
			return errors.Wrap(err, "disable pitr")
		}
		if err := pbm.C.SetConfigVar("pitr.enabled", "true"); err != nil {
			return errors.Wrap(err, "enable pitr")
		}
	}

	val, err = pbm.C.GetConfigVar("pitr.compressionLevel")
	var compressionLevel *int = nil
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
			return errors.Wrap(err, "get pitr.compressionLevel")
		}
	} else {
		tmpCompressionLevel, ok := val.(int)
		if !ok {
			return errors.Wrap(err, "unexpected value of pitr.compressionLevel")
		}
		compressionLevel = &tmpCompressionLevel
	}

	if !reflect.DeepEqual(compressionLevel, cr.Spec.Backup.PITR.CompressionLevel) {
		if cr.Spec.Backup.PITR.CompressionLevel == nil {
			if err := pbm.C.DeleteConfigVar("pitr.compressionLevel"); err != nil {
				return errors.Wrap(err, "delete pitr.compressionLevel")
			}
		} else if err := pbm.C.SetConfigVar("pitr.compressionLevel", strconv.FormatInt(int64(*cr.Spec.Backup.PITR.CompressionLevel), 10)); err != nil {
			return errors.Wrap(err, "update pitr.compressionLevel")
		}

		// PBM needs to disabling and enabling PITR to change compression level
		if err := pbm.C.SetConfigVar("pitr.enabled", "false"); err != nil {
			return errors.Wrap(err, "disable pitr")
		}
		if err := pbm.C.SetConfigVar("pitr.enabled", "true"); err != nil {
			return errors.Wrap(err, "enable pitr")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) resyncPBMIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if cr.Status.State != api.AppStateReady || !cr.Spec.Backup.Enabled {
		return nil
	}

	_, resyncNeeded := cr.Annotations[api.AnnotationResyncPBM]
	if !resyncNeeded {
		return nil
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		orig := c.DeepCopy()
		delete(c.Annotations, api.AnnotationResyncPBM)

		return r.client.Patch(ctx, c, client.MergeFrom(orig))
	})
	if err != nil {
		return errors.Wrap(err, "delete annotation")
	}

	log.V(1).Info("Deleted annotation", "annotation", api.AnnotationResyncPBM)

	pod := &corev1.Pod{}
	podName := fmt.Sprintf("%s-%s-0", cr.Name, cr.Spec.Replsets[0].Name)
	err = r.client.Get(ctx, types.NamespacedName{Name: podName, Namespace: cr.Namespace}, pod)
	if err != nil {
		return errors.Wrapf(err, "get pod/%s", podName)
	}

	stdoutBuffer := bytes.Buffer{}
	stderrBuffer := bytes.Buffer{}
	command := []string{"pbm", "config", "--force-resync"}
	log.Info("Starting PBM resync", "command", command)

	err = r.clientcmd.Exec(pod, "backup-agent", command, nil, &stdoutBuffer, &stderrBuffer, false)
	if err != nil {
		return errors.Wrapf(err, "start PBM resync: run %v", command)
	}

	return nil
}
