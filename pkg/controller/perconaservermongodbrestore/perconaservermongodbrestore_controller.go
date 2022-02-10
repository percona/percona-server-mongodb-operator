package perconaservermongodbrestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson/primitive"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/version"
)

var log = logf.Log.WithName("controller_perconaservermongodbrestore")

// Add creates a new PerconaServerMongoDBRestore Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcilePerconaServerMongoDBRestore{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("perconaservermongodbrestore-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource PerconaServerMongoDBRestore
	err = c.Watch(&source.Kind{Type: &psmdbv1.PerconaServerMongoDBRestore{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Pods and requeue the owner PerconaServerMongoDBRestore
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &psmdbv1.PerconaServerMongoDBRestore{},
	})
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDBRestore{}

// ReconcilePerconaServerMongoDBRestore reconciles a PerconaServerMongoDBRestore object
type ReconcilePerconaServerMongoDBRestore struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a PerconaServerMongoDBRestore object and makes changes based on the state read
// and what is in the PerconaServerMongoDBRestore.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDBRestore) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	rr := reconcile.Result{
		RequeueAfter: time.Second * 5,
	}

	// Fetch the PerconaSMDBBackupRestore instance
	cr := &psmdbv1.PerconaServerMongoDBRestore{}
	err := r.client.Get(context.TODO(), request.NamespacedName, cr)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return rr, nil
		}
		// Error reading the object - requeue the request.
		return rr, err
	}

	status := cr.Status

	defer func() {
		if err != nil {
			status.State = psmdbv1.RestoreStateError
			status.Error = err.Error()
			log.Error(err, "failed to make restore", "restore", cr.Name, "backup", cr.Spec.BackupName)
		}
		if cr.Status.State != status.State || cr.Status.Error != status.Error {
			cr.Status = status
			uerr := r.updateStatus(cr)
			if uerr != nil {
				log.Error(uerr, "failed to updated restore status", "restore", cr.Name, "backup", cr.Spec.BackupName)
			}
		}
	}()

	err = cr.CheckFields()
	if err != nil {
		return rr, fmt.Errorf("fields check: %v", err)
	}

	switch cr.Status.State {
	case psmdbv1.RestoreStateReady, psmdbv1.RestoreStateError:
		return rr, nil
	}

	status, err = r.reconcileRestore(cr)
	if err != nil {
		return rr, fmt.Errorf("reconcile: %v", err)
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) reconcileRestore(cr *psmdbv1.PerconaServerMongoDBRestore) (psmdbv1.PerconaServerMongoDBRestoreStatus, error) {
	status := cr.Status

	cluster := &psmdbv1.PerconaServerMongoDB{}
	err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Spec.ClusterName, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return status, errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.ClusterName)
	}

	if cluster.Spec.Unmanaged {
		return status, errors.New("cluster is unmanaged")
	}

	svr, err := version.Server()
	if err != nil {
		return status, errors.Wrapf(err, "fetch server version")
	}

	if err := cluster.CheckNSetDefaults(svr.Platform, log); err != nil {
		return status, errors.Wrapf(err, "set defaults for %s/%s", cluster.Namespace, cluster.Name)
	}

	cjobs, err := backup.HasActiveJobs(r.client, cluster, backup.NewRestoreJob(cr), backup.NotPITRLock)
	if err != nil {
		return status, errors.Wrap(err, "check for concurrent jobs")
	}
	if cjobs {
		if cr.Status.State != psmdbv1.RestoreStateWaiting {
			log.Info("waiting to finish another backup/restore.")
		}
		status.State = psmdbv1.RestoreStateWaiting
		return status, nil
	}

	var (
		backupName  = cr.Spec.BackupName
		storageName = cr.Spec.StorageName
	)

	if backupName == "" || storageName == "" {
		bcp, err := r.getBackup(cr)
		if err != nil {
			return status, errors.Wrap(err, "get backup")
		}
		if bcp.Status.State != psmdbv1.BackupStateReady {
			return status, errors.New("backup is not ready")
		}

		backupName = bcp.Status.PBMname
		storageName = bcp.Spec.StorageName
	}

	if cluster.Spec.Sharding.Enabled {
		mongos := appsv1.Deployment{}
		err = r.client.Get(context.Background(), cluster.MongosNamespacedName(), &mongos)
		if err != nil && !k8serrors.IsNotFound(err) {
			return status, errors.Wrapf(err, "failed to get mongos")
		}

		if err == nil {
			log.Info("waiting for mongos termination")

			status.State = psmdbv1.RestoreStateWaiting
			return status, nil
		}
	}

	pbmc, errPBM := backup.NewPBM(r.client, cluster)
	if errPBM != nil {
		log.Info("Waiting for pbm-agent.")
		status.State = psmdbv1.RestoreStateWaiting
		return status, nil
	}
	defer pbmc.Close()

	if status.State == psmdbv1.RestoreStateNew || status.State == psmdbv1.RestoreStateWaiting {
		storage, err := r.getStorage(cr, cluster, storageName)
		if err != nil {
			return status, errors.Wrap(err, "get storage")
		}

		priorities, err := pbmc.GetPriorities(r.client, cluster)
		if err != nil {
			return status, errors.Wrap(err, "get pbm priorities")
		}

		err = pbmc.SetConfig(storage, cluster.Spec.Backup.PITR.Disabled(), priorities)
		if err != nil {
			return status, errors.Wrap(err, "set pbm config")
		}

		isBlockedByPITR, err := pbmc.HasLocks(backup.IsPITRLock)
		if err != nil {
			return status, errors.Wrap(err, "checking pbm pitr locks")
		}

		if isBlockedByPITR {
			log.Info("Waiting for PITR to be disabled.")
			status.State = psmdbv1.RestoreStateWaiting
			return status, nil
		}

		status.PBMname, err = runRestore(backupName, pbmc, cr.Spec.PITR)
		status.State = psmdbv1.RestoreStateRequested
		return status, err
	}

	meta, err := pbmc.C.GetRestoreMeta(cr.Status.PBMname)
	if err != nil && !errors.Is(err, pbm.ErrNotFound) {
		return status, errors.Wrap(err, "get pbm metadata")
	}

	if meta == nil || meta.Name == "" {
		log.Info("Waiting for restore metadata", "PBM name", cr.Status.PBMname, "restore", cr.Name, "backup", cr.Spec.BackupName)
		return status, nil
	}

	switch meta.Status {
	case pbm.StatusError:
		status.State = psmdbv1.RestoreStateError
		status.Error = meta.Error
		if err = reEnablePITR(pbmc, cluster.Spec.Backup); err != nil {
			return status, err
		}
	case pbm.StatusDone:
		status.State = psmdbv1.RestoreStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(meta.LastTransitionTS, 0),
		}
		if err = reEnablePITR(pbmc, cluster.Spec.Backup); err != nil {
			return status, err
		}
	case pbm.StatusStarting, pbm.StatusRunning:
		status.State = psmdbv1.RestoreStateRunning
	}

	return status, nil
}

func reEnablePITR(pbm *backup.PBM, backup psmdbv1.BackupSpec) (err error) {
	if !backup.IsEnabledPITR() {
		return
	}

	err = pbm.C.SetConfigVar("pitr.enabled", "true")
	if err != nil {
		return
	}

	return
}

func runRestore(backup string, pbmc *backup.PBM, pitr *psmdbv1.PITRestoreSpec) (string, error) {
	e := pbmc.C.Logger().NewEvent(string(pbm.CmdResyncBackupList), "", "", primitive.Timestamp{})
	err := pbmc.C.ResyncStorage(e)
	if err != nil {
		return "", errors.Wrap(err, "set resync backup list from the store")
	}

	var (
		cmd   pbm.Cmd
		rName = time.Now().UTC().Format(time.RFC3339Nano)
	)

	switch {
	case pitr == nil:
		cmd = pbm.Cmd{
			Cmd: pbm.CmdRestore,
			Restore: pbm.RestoreCmd{
				Name:       rName,
				BackupName: backup,
			},
		}
	case pitr.Type == psmdbv1.PITRestoreTypeDate:
		ts := pitr.Date.Unix()

		if _, err := pbmc.GetPITRChunkContains(ts); err != nil {
			return "", err
		}

		cmd = pbm.Cmd{
			Cmd: pbm.CmdPITRestore,
			PITRestore: pbm.PITRestoreCmd{
				Name: rName,
				TS:   ts,
			},
		}
	case pitr.Type == psmdbv1.PITRestoreTypeLatest:
		tl, err := pbmc.GetLatestTimelinePITR()
		if err != nil {
			return "", err
		}

		cmd = pbm.Cmd{
			Cmd: pbm.CmdPITRestore,
			PITRestore: pbm.PITRestoreCmd{
				Name: rName,
				TS:   int64(tl.End),
			},
		}
	}

	if err = pbmc.C.SendCmd(cmd); err != nil {
		return "", errors.Wrap(err, "send restore cmd")
	}

	return rName, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) getStorage(cr *psmdbv1.PerconaServerMongoDBRestore, cluster *psmdbv1.PerconaServerMongoDB, storageName string) (psmdbv1.BackupStorageSpec, error) {
	if len(storageName) > 0 {
		storage, ok := cluster.Spec.Backup.Storages[storageName]
		if !ok {
			return psmdbv1.BackupStorageSpec{}, errors.Errorf("unable to get storage '%s'", storageName)
		}
		return storage, nil
	}
	var azure psmdbv1.BackupStorageAzureSpec
	var s3 psmdbv1.BackupStorageS3Spec
	storageType := psmdbv1.BackupStorageS3

	if cr.Spec.BackupSource.Azure != nil {
		storageType = psmdbv1.BackupStorageAzure
		azure = *cr.Spec.BackupSource.Azure
	} else if cr.Spec.BackupSource.S3 != nil {
		s3 = *cr.Spec.BackupSource.S3
	}
	return psmdbv1.BackupStorageSpec{
		Type:  storageType,
		S3:    s3,
		Azure: azure,
	}, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) getBackup(cr *psmdbv1.PerconaServerMongoDBRestore) (*psmdbv1.PerconaServerMongoDBBackup, error) {
	if len(cr.Spec.BackupName) == 0 && cr.Spec.BackupSource != nil {
		s := strings.Split(cr.Spec.BackupSource.Destination, "/")
		backupName := s[len(s)-1]

		return &psmdbv1.PerconaServerMongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:        cr.Name,
				Namespace:   cr.Namespace,
				ClusterName: cr.ClusterName,
			},
			Spec: psmdbv1.PerconaServerMongoDBBackupSpec{
				ClusterName: cr.Spec.ClusterName,
				StorageName: cr.Spec.StorageName,
			},
			Status: psmdbv1.PerconaServerMongoDBBackupStatus{
				State:       psmdbv1.BackupStateReady,
				Destination: cr.Spec.BackupSource.Destination,
				StorageName: cr.Spec.StorageName,
				S3:          cr.Spec.BackupSource.S3,
				Azure:       cr.Spec.BackupSource.Azure,
				PBMname:     backupName,
			},
		}, nil
	}

	backup := &psmdbv1.PerconaServerMongoDBBackup{}
	err := r.client.Get(context.TODO(), types.NamespacedName{
		Name:      cr.Spec.BackupName,
		Namespace: cr.Namespace,
	}, backup)

	return backup, err
}

func (r *ReconcilePerconaServerMongoDBRestore) updateStatus(cr *psmdbv1.PerconaServerMongoDBRestore) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &psmdbv1.PerconaServerMongoDBRestore{}

		err := r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		c.Status = cr.Status

		return r.client.Status().Update(context.TODO(), c)
	})

	return errors.Wrap(err, "write status")
}
