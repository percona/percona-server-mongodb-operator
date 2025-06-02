package perconaservermongodb

import (
	"container/heap"
	"context"
	"time"

	"github.com/pkg/errors"
	"github.com/robfig/cron/v3"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/defs"

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
	t := BackupScheduleJob{}
	bj, ok := r.crons.backupJobs.Load(task.JobName(cr))
	if ok {
		t = bj.(BackupScheduleJob)

		if t.Schedule == task.Schedule && t.StorageName == task.StorageName {
			return nil
		}
	}

	logf.FromContext(ctx).Info("Creating or updating backup job", "name", task.Name, "namespace", cr.Namespace, "schedule", task.Schedule)

	r.deleteBackupTask(cr, t.BackupTaskSpec)
	jobID, err := r.crons.crons.AddFunc(task.Schedule, r.createBackupTask(ctx, cr, task))
	if err != nil {
		return errors.Wrapf(err, "can't parse cronjob schedule for backup %s with schedule %s", task.Name, task.Schedule)
	}

	if !ok && t.Type == defs.IncrementalBackup {
		logf.FromContext(ctx).Info(".keep option does not work with incremental backups", "name", task.Name, "namespace", cr.Namespace)
	}

	r.crons.backupJobs.Store(task.JobName(cr), BackupScheduleJob{
		BackupTaskSpec: task,
		JobID:          jobID,
	})
	return nil
}

func (r *ReconcilePerconaServerMongoDB) deleteOldBackupTasks(ctx context.Context, cr *api.PerconaServerMongoDB, ctasks map[string]api.BackupTaskSpec) error {
	log := logf.FromContext(ctx)

	r.crons.backupJobs.Range(func(k, v interface{}) bool {
		item := v.(BackupScheduleJob)
		if spec, ok := ctasks[item.Name]; ok {
			// TODO: make .keep to work with incremental backups
			if spec.Type == defs.IncrementalBackup {
				return true
			}

			ret := spec.GetRetention(cr)
			if ret.Type != api.BackupTaskSpecRetentionTypeCount {
				log.Error(nil, "unsupported retention type", "type", ret.Type)
				return true
			}

			if ret.Count <= 0 {
				return true
			}

			oldjobs, err := r.oldScheduledBackups(ctx, cr, item.Name, ret.Count)
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

// oldScheduledBackups returns list of the most old psmdb-backups that execeed `keep` limit
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

func (r *ReconcilePerconaServerMongoDB) hasFullBackup(ctx context.Context, cr *api.PerconaServerMongoDB, stgName string) (bool, error) {
	backups := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(ctx, &backups, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, b := range backups.Items {
		if b.Status.State == api.BackupStateReady && b.Spec.GetClusterName() == cr.Name && b.Spec.StorageName == stgName {
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

func updateLatestRestorableTime(ctx context.Context, cl client.Client, pbm backup.PBM, cr *api.PerconaServerMongoDB) error {
	if cr.CompareVersion("1.16.0") < 0 {
		return nil
	}

	log := logf.FromContext(ctx)

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

	latestRestorableTime := metav1.Time{
		Time: time.Unix(int64(tl.End), 0),
	}

	if !bcp.Status.LatestRestorableTime.Equal(&latestRestorableTime) {
		log.Info("updating latest restorable time", "backup", bcp.Name, "latestRestorableTime", latestRestorableTime)
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		b := new(api.PerconaServerMongoDBBackup)
		if err := cl.Get(ctx, types.NamespacedName{Name: bcp.Name, Namespace: bcp.Namespace}, b); err != nil {
			return errors.Wrap(err, "get backup")
		}
		b.Status.LatestRestorableTime = &latestRestorableTime
		return cl.Status().Update(ctx, b)
	}); err != nil {
		return errors.Wrap(err, "update status")
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
