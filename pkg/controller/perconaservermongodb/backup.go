package perconaservermongodb

import (
	"bytes"
	"container/heap"
	"context"
	"fmt"
	"reflect"
	"strconv"
	"time"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
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
		ls := naming.NewBackupCronJobLabels(cr, cr.Spec.Backup.Labels)
		tasksList := &batchv1.CronJobList{}
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
	ancestor string, keep int,
) ([]api.PerconaServerMongoDBBackup, error) {
	bcpList := api.PerconaServerMongoDBBackupList{}
	err := r.client.List(ctx,
		&bcpList,
		&client.ListOptions{
			Namespace: cr.Namespace,
			LabelSelector: labels.SelectorFromSet(map[string]string{
				naming.LabelCluster:        cr.Name,
				naming.LabelBackupAncestor: ancestor,
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
		if rst.Spec.ClusterName != cr.Name {
			continue
		}

		if rst.Status.State == api.RestoreStateReady || rst.Status.State == api.RestoreStateError {
			continue
		}

		return true, nil
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

func getLatestBackup(ctx context.Context, cl client.Client, cr *api.PerconaServerMongoDB) (*api.PerconaServerMongoDBBackup, error) {
	backups := api.PerconaServerMongoDBBackupList{}
	if err := cl.List(ctx, &backups, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, errors.Wrap(err, "get backup list")
	}

	var latest *api.PerconaServerMongoDBBackup
	for _, b := range backups.Items {
		b := b

		if b.Status.State != api.BackupStateReady || b.Spec.GetClusterName() != cr.Name {
			continue
		}

		if latest == nil || latest.CreationTimestamp.Before(&b.ObjectMeta.CreationTimestamp) {
			latest = &b
		}
	}

	return latest, nil
}

func (r *ReconcilePerconaServerMongoDB) updatePITR(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if !cr.Spec.Backup.Enabled {
		return nil
	}

	_, resyncNeeded := cr.Annotations[api.AnnotationResyncPBM]
	if resyncNeeded {
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

	if cr.Spec.Backup.PITR.Enabled && !cr.Spec.Backup.PITR.OplogOnly {
		hasFullBackup, err := r.hasFullBackup(ctx, cr)
		if err != nil {
			return errors.Wrap(err, "check full backup")
		}

		if !hasFullBackup {
			log.Info("Point-in-time recovery will work only with full backup. Please create one manually or wait for scheduled backup to be created (if configured).")
			return nil
		}
	}

	val, err := pbm.GetConfigVar(ctx, "pitr.enabled")
	if err != nil {
		if !errors.Is(err, mongo.ErrNoDocuments) {
			return errors.Wrap(err, "get pitr.enabled")
		}

		if len(cr.Spec.Backup.Storages) == 1 {
			// if PiTR is enabled user can configure only one storage
			var storage api.BackupStorageSpec
			for name, stg := range cr.Spec.Backup.Storages {
				storage = stg
				log.Info("Configuring PBM with storage", "storage", name)
				break
			}

			var secretName string
			switch storage.Type {
			case api.BackupStorageS3:
				secretName = storage.S3.CredentialsSecret
			case api.BackupStorageAzure:
				secretName = storage.Azure.CredentialsSecret
			}

			if secretName != "" {
				exists, err := secretExists(ctx, r.client, types.NamespacedName{Name: secretName, Namespace: cr.Namespace})
				if err != nil {
					return errors.Wrap(err, "check storage credentials secret")
				}

				if !exists {
					log.Error(nil, "Storage credentials secret does not exist", "secret", secretName)
					return nil
				}
			}

			err = pbm.GetNSetConfig(ctx, r.client, cr, storage)
			if err != nil {
				return errors.Wrap(err, "set PBM config")
			}

			log.Info("Configured PBM storage")
		}

		return nil
	}

	enabled, ok := val.(bool)
	if !ok {
		return errors.Errorf("unexpected value of pitr.enabled: %T", val)
	}

	if enabled != cr.Spec.Backup.PITR.Enabled {
		val := strconv.FormatBool(cr.Spec.Backup.PITR.Enabled)
		log.Info("Setting pitr.enabled in PBM config", "enabled", val)
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", val); err != nil {
			return errors.Wrap(err, "update pitr.enabled")
		}
	}

	if !cr.Spec.Backup.PITR.Enabled {
		return nil
	}

	val, err = pbm.GetConfigVar(ctx, "pitr.oplogOnly")
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		}

		if errors.Is(err, bsoncore.ErrElementNotFound) {
			val = false
		} else {
			return errors.Wrap(err, "get pitr.oplogOnly")
		}
	}

	oplogOnly, ok := val.(bool)
	if !ok {
		return errors.Errorf("unexpected value of pitr.oplogOnly: %T", val)
	}

	if oplogOnly != cr.Spec.Backup.PITR.OplogOnly {
		enabled := strconv.FormatBool(cr.Spec.Backup.PITR.OplogOnly)
		log.Info("Setting pitr.oplogOnly in PBM config", "value", enabled)
		if err := pbm.SetConfigVar(ctx, "pitr.oplogOnly", enabled); err != nil {
			return errors.Wrap(err, "update pitr.oplogOnly")
		}
	}

	val, err = pbm.GetConfigVar(ctx, "pitr.oplogSpanMin")
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		}
		// PBM-1387
		// PBM has a bug to return config fields if they use default value
		if errors.Is(err, bsoncore.ErrElementNotFound) {
			val = 0.0
		} else {
			return errors.Wrap(err, "get pitr.oplogSpanMin")
		}
	}

	oplogSpanMin, ok := val.(float64)
	if !ok {
		return errors.Errorf("unexpected value of pitr.oplogSpanMin: %T", val)
	}

	if oplogSpanMin != cr.Spec.Backup.PITR.OplogSpanMin.Float64() {
		val := cr.Spec.Backup.PITR.OplogSpanMin.String()
		if err := pbm.SetConfigVar(ctx, "pitr.oplogSpanMin", val); err != nil {
			return errors.Wrap(err, "update pitr.oplogSpanMin")
		}
	}

	val, err = pbm.GetConfigVar(ctx, "pitr.compression")
	compression := ""
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
			return errors.Wrap(err, "get pitr.compression")
		}
	} else {
		compression, ok = val.(string)
		if !ok {
			return errors.Errorf("unexpected value of pitr.compression: %T", val)
		}
	}

	if compression != string(cr.Spec.Backup.PITR.CompressionType) {
		if string(cr.Spec.Backup.PITR.CompressionType) == "" {
			cfg, err := pbm.GetConfig(ctx)
			if err != nil {
				return errors.Wrap(err, "get pbm config")
			}

			cfg.PITR.Compression = ""
			if err := pbm.SetConfig(ctx, cfg); err != nil {
				return errors.Wrap(err, "delete pitr.compression")
			}
		} else if err := pbm.SetConfigVar(ctx, "pitr.compression", string(cr.Spec.Backup.PITR.CompressionType)); err != nil {
			return errors.Wrap(err, "update pitr.compression")
		}

		// PBM needs to disabling and enabling PITR to change compression type
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "false"); err != nil {
			return errors.Wrap(err, "disable pitr")
		}
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "true"); err != nil {
			return errors.Wrap(err, "enable pitr")
		}
	}

	val, err = pbm.GetConfigVar(ctx, "pitr.compressionLevel")
	var compressionLevel *int = nil
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
			return errors.Wrap(err, "get pitr.compressionLevel")
		}
	} else {
		var iVal int
		switch v := val.(type) {
		case int64:
			iVal = int(v)
		case int32:
			iVal = int(v)
		default:
			return errors.Errorf("unexpected value of pitr.compressionLevel: %T", val)
		}
		compressionLevel = &iVal
	}

	if !reflect.DeepEqual(compressionLevel, cr.Spec.Backup.PITR.CompressionLevel) {
		if cr.Spec.Backup.PITR.CompressionLevel == nil {
			cfg, err := pbm.GetConfig(ctx)
			if err != nil {
				return errors.Wrap(err, "get pbm config")
			}

			cfg.PITR.CompressionLevel = nil
			if err := pbm.SetConfig(ctx, cfg); err != nil {
				return errors.Wrap(err, "delete pitr.compressionLevel")
			}
		} else if err := pbm.SetConfigVar(ctx, "pitr.compressionLevel", strconv.FormatInt(int64(*cr.Spec.Backup.PITR.CompressionLevel), 10)); err != nil {
			return errors.Wrap(err, "update pitr.compressionLevel")
		}

		// PBM needs to disabling and enabling PITR to change compression level
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "false"); err != nil {
			return errors.Wrap(err, "disable pitr")
		}
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "true"); err != nil {
			return errors.Wrap(err, "enable pitr")
		}
	}

	if err := updateLatestRestorableTime(ctx, r.client, pbm, cr); err != nil {
		return errors.Wrap(err, "update latest restorable time")
	}

	return nil
}

func updateLatestRestorableTime(ctx context.Context, cl client.Client, pbm backup.PBM, cr *api.PerconaServerMongoDB) error {
	if cr.CompareVersion("1.16.0") < 0 {
		return nil
	}

	tl, err := pbm.GetLatestTimelinePITR(ctx)
	if err != nil {
		if err == backup.ErrNoOplogsForPITR {
			return nil
		}
		return errors.Wrap(err, "get latest PITR timeline")
	}

	bcp, err := getLatestBackup(ctx, cl, cr)
	if err != nil {
		return errors.Wrap(err, "get latest backup")
	}
	if bcp == nil {
		return nil
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		b := new(api.PerconaServerMongoDBBackup)
		if err := cl.Get(ctx, types.NamespacedName{Name: bcp.Name, Namespace: bcp.Namespace}, b); err != nil {
			return errors.Wrap(err, "get backup")
		}
		b.Status.LatestRestorableTime = &metav1.Time{
			Time: time.Unix(int64(tl.End), 0),
		}
		return cl.Status().Update(ctx, b)
	}); err != nil {
		return errors.Wrap(err, "update status")
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

	err = r.clientcmd.Exec(ctx, pod, naming.ContainerBackupAgent, command, nil, &stdoutBuffer, &stderrBuffer, false)
	if err != nil {
		return errors.Wrapf(err, "start PBM resync: run %v", command)
	}

	return nil
}

func secretExists(ctx context.Context, cl client.Client, nn types.NamespacedName) (bool, error) {
	var secret corev1.Secret
	err := cl.Get(ctx, nn, &secret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}
