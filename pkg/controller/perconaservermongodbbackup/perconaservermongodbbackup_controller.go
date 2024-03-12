package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
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

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pbm"
	"github.com/percona/percona-server-mongodb-operator/version"
)

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new PerconaServerMongoDBBackup Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	reconciler, err := newReconciler(mgr)
	if err != nil {
		return errors.Wrap(err, "create reconciler")
	}
	return add(mgr, reconciler)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	cli, err := clientcmd.NewClient()
	if err != nil {
		return nil, errors.Wrap(err, "create clientcmd")
	}

	return &ReconcilePerconaServerMongoDBBackup{
		client:    mgr.GetClient(),
		scheme:    mgr.GetScheme(),
		clientcmd: cli,
	}, nil
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("psmdbbackup-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource PerconaServerMongoDBBackup
	err = c.Watch(source.Kind(mgr.GetCache(), new(psmdbv1.PerconaServerMongoDBBackup)), new(handler.EnqueueRequestForObject))
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner PerconaServerMongoDBBackup
	err = c.Watch(source.Kind(mgr.GetCache(), new(corev1.Pod)), handler.EnqueueRequestForOwner(
		mgr.GetScheme(), mgr.GetRESTMapper(), new(psmdbv1.PerconaServerMongoDBBackup), handler.OnlyControllerOwner(),
	))
	if err != nil {
		return err
	}

	return nil
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDBBackup{}

// ReconcilePerconaServerMongoDBBackup reconciles a PerconaServerMongoDBBackup object
type ReconcilePerconaServerMongoDBBackup struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	scheme    *runtime.Scheme
	clientcmd *clientcmd.Client
}

// Reconcile reads that state of the cluster for a PerconaServerMongoDBBackup object and makes changes based on the state read
// and what is in the PerconaServerMongoDBBackup.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDBBackup) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	rr := reconcile.Result{
		RequeueAfter: time.Second * 5,
	}
	// Fetch the PerconaServerMongoDBBackup instance
	cr := &psmdbv1.PerconaServerMongoDBBackup{}
	err := r.client.Get(ctx, request.NamespacedName, cr)
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

	if (cr.Status.State == psmdbv1.BackupStateReady || cr.Status.State == psmdbv1.BackupStateError) &&
		cr.ObjectMeta.DeletionTimestamp == nil {
		return rr, nil
	}

	status := cr.Status

	defer func() {
		if err != nil {
			status.State = psmdbv1.BackupStateError
			status.Error = err.Error()
			log.Error(err, "failed to make backup", "backup", cr.Name)
		}
		if cr.Status.State != status.State || cr.Status.Error != status.Error || !reflect.DeepEqual(cr.Status.Conditions, status.Conditions) {
			cr.Status = status
			uerr := r.updateStatus(ctx, cr)
			if uerr != nil {
				log.Error(uerr, "failed to update backup status", "backup", cr.Name)
			}
		}
	}()

	err = cr.CheckFields()
	if err != nil {
		return rr, errors.Wrap(err, "fields check")
	}

	cluster := new(psmdbv1.PerconaServerMongoDB)
	err = r.client.Get(ctx, types.NamespacedName{Name: cr.Spec.GetClusterName(), Namespace: cr.Namespace}, cluster)
	if err != nil {
		if !k8serrors.IsNotFound(err) {
			return rr, errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.GetClusterName())
		}
		cluster = nil
	}

	if cluster != nil {
		var svr *version.ServerVersion
		svr, err = version.Server()
		if err != nil {
			return rr, errors.Wrapf(err, "fetch server version")
		}

		err = cluster.CheckNSetDefaults(svr.Platform, log)
		if err != nil {
			return rr, errors.Wrapf(err, "set defaults for %s/%s", cluster.Namespace, cluster.Name)
		}
		// TODO: Remove after 1.15
		if cluster.CompareVersion("1.12.0") >= 0 && cr.Spec.ClusterName == "" {
			cr.Spec.ClusterName = cr.Spec.PSMDBCluster
			cr.Spec.PSMDBCluster = ""
			err = r.client.Update(ctx, cr)
			if err != nil {
				return rr, errors.Wrap(err, "failed to update clusterName")
			}
		}
	}

	err = r.checkFinalizers(ctx, cr, cluster)
	if err != nil {
		return rr, errors.Wrap(err, "failed to run finalizer")
	}

	if cr.ObjectMeta.DeletionTimestamp != nil {
		return rr, nil
	}

	status, err = r.reconcile(ctx, cluster, cr)
	if err != nil {
		return rr, errors.Wrap(err, "reconcile backup")
	}

	return rr, nil
}

// reconcile backup. firstly we check if there are concurrent jobs running
func (r *ReconcilePerconaServerMongoDBBackup) reconcile(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	cr *psmdbv1.PerconaServerMongoDBBackup,
) (psmdbv1.PerconaServerMongoDBBackupStatus, error) {
	log := logf.FromContext(ctx)
	status := cr.Status
	if cluster == nil {
		return status, errors.New("cluster not found")
	}

	if err := cluster.CanBackup(); err != nil {
		return status, errors.Wrap(err, "failed to run backup")
	}

	pbmClient, err := pbm.New(ctx, r.clientcmd, r.client, cluster)
	if err != nil {
		return status, errors.Wrap(err, "create PBM client")
	}

	running, err := pbmClient.GetRunningOperation(ctx)
	if err != nil {
		return status, errors.Wrap(err, "check for concurrent jobs")
	}

	if running.Name != status.PBMName && running.Name != "" {
		if cr.Status.State != psmdbv1.BackupStateWaiting {
			log.Info("Waiting to finish another backup/restore.", "operation", running.Name, "type", running.Type, "opId", running.OpID, "status", running.Status)
		}
		status.State = psmdbv1.BackupStateWaiting
		return status, nil
	}

	if meta.FindStatusCondition(cr.Status.Conditions, "PBMConfigured") == nil {
		log.Info("Configuring PBM", "backup", cr.Name)

		if err := pbmClient.SetConfigFile(ctx, pbm.ConfigFileDir+"/"+cr.Spec.StorageName); err != nil {
			return status, errors.Wrapf(err, "set PBM config file %s", pbm.ConfigFileDir+"/"+cr.Spec.StorageName)
		}

		err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
			c := &psmdbv1.PerconaServerMongoDB{}
			err := r.client.Get(ctx, client.ObjectKeyFromObject(cluster), c)
			if err != nil {
				return err
			}

			c.Status.BackupStorage = cr.Spec.StorageName

			return r.client.Status().Update(ctx, c)
		})
		if err != nil {
			return status, errors.Wrap(err, "update cluster status")
		}

		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               "PBMConfigured",
			Reason:             "PBMConfigured",
			Message:            "PBM is configured with storage " + cr.Spec.StorageName,
			Status:             metav1.ConditionTrue,
			LastTransitionTime: metav1.Time{Time: time.Now()},
		})

		return status, nil
	}

	if cr.Status.State == psmdbv1.BackupStateNew || cr.Status.State == psmdbv1.BackupStateWaiting {
		log.Info("Starting backup", "backup", cr.Name)

		backup, err := pbmClient.RunBackup(ctx, pbm.BackupOptions{
			Type:        cr.Spec.Type,
			Compression: cr.Spec.Compression,
		})
		if err != nil {
			if pbm.IsAnotherOperationInProgress(err) {
				log.Info("Another operation is in progress", "backup", cr.Name, "error", err)
				return status, nil
			}
			return status, errors.Wrap(err, "run backup")
		}

		status.PBMName = backup.Name
		status.State = psmdbv1.BackupStateRequested

		return status, nil
	}

	var backupMeta pbm.DescribeBackupResponse
	err = retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
		backupMeta, err = pbmClient.DescribeBackup(ctx, pbm.DescribeBackupOptions{Name: status.PBMName})

		if err != nil {
			return err
		}

		return nil
	})
	if err != nil {
		return status, errors.Wrap(err, "describe backup")
	}

	return getBackupStatus(ctx, cr, cluster, backupMeta)
}

func (r *ReconcilePerconaServerMongoDBBackup) checkFinalizers(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup, cluster *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	var err error
	if cr.ObjectMeta.DeletionTimestamp == nil {
		return nil
	}

	finalizers := []string{}

	if cr.Status.State == psmdbv1.BackupStateReady {
		for _, f := range cr.GetFinalizers() {
			switch f {
			case "delete-backup":
				if err := r.deleteBackupFinalizer(ctx, cr, cluster); err != nil {
					log.Error(err, "failed to run finalizer", "finalizer", f)
					finalizers = append(finalizers, f)
				}
			}
		}
	}

	cr.SetFinalizers(finalizers)
	err = r.client.Update(ctx, cr)

	return err
}

func (r *ReconcilePerconaServerMongoDBBackup) deleteBackupFinalizer(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup, cluster *psmdbv1.PerconaServerMongoDB) error {
	l := logf.FromContext(ctx)

	if len(cr.Status.PBMName) == 0 {
		return nil
	}

	if cluster == nil {
		return errors.Errorf("PerconaServerMongoDB %s is not found", cr.Spec.GetClusterName())
	}

	var stg psmdbv1.BackupStorageSpec
	switch {
	case cr.Status.S3 != nil:
		stg.Type = storage.S3
		stg.S3 = *cr.Status.S3
	case cr.Status.Azure != nil:
		stg.Type = storage.Azure
		stg.Azure = *cr.Status.Azure
	}

	pbmClient, err := pbm.New(ctx, r.clientcmd, r.client, cluster)
	if err != nil {
		return errors.Wrap(err, "create PBM client")
	}

	l.V(1).Info("Setting storage config", "backup", cr.Status.PBMName, "stg", stg)

	if err := pbmClient.SetStorageConfig(ctx, stg); err != nil {
		return errors.Wrap(err, "set storage config")
	}

	l.V(1).Info("Deleting backup", "backup", cr.Status.PBMName)

	if err := pbmClient.DeleteBackup(ctx, cr.Status.PBMName); err != nil {
		return errors.Wrap(err, "delete backup")
	}

	l.Info("Deleted backup", "backup", cr.Status.PBMName)

	return nil
}

func getBackupStatus(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup, cluster *psmdbv1.PerconaServerMongoDB, backupMeta pbm.DescribeBackupResponse) (psmdbv1.PerconaServerMongoDBBackupStatus, error) {
	log := logf.FromContext(ctx)

	status := cr.Status

	stg, ok := cluster.Spec.Backup.Storages[cr.Spec.StorageName]
	if !ok {
		return status, errors.Errorf("unable to get storage '%s'", cr.Spec.StorageName)
	}

	switch stg.Type {
	case storage.S3:
		status.S3 = &stg.S3

		status.Destination = stg.S3.Bucket

		if stg.S3.Prefix != "" {
			status.Destination = stg.S3.Bucket + "/" + stg.S3.Prefix
		}
		if !strings.HasPrefix(stg.S3.Bucket, "s3://") {
			status.Destination = "s3://" + status.Destination
		}
	case storage.Azure:
		status.Azure = &stg.Azure

		status.Destination = stg.Azure.Container

		if stg.Azure.Prefix != "" {
			status.Destination = stg.Azure.Container + "/" + stg.Azure.Prefix
		}
		if !strings.HasPrefix(stg.Azure.Container, "azure://") {
			status.Destination = "azure://" + status.Destination
		}
	}

	status.Destination += "/" + status.PBMName

	switch backupMeta.Status {
	case defs.StatusError:
		log.Info("Backup failed", "backup", cr.Name, "error", backupMeta.Error)
		status.State = psmdbv1.BackupStateError
		status.Error = fmt.Sprintf("%v", backupMeta.Error)
	case defs.StatusDone:
		log.Info("Backup completed", "backup", cr.Name)
		status.State = psmdbv1.BackupStateReady
		status.CompletedAt = &metav1.Time{
			Time: time.Unix(backupMeta.LastTransitionTS, 0),
		}
	case defs.StatusStarting:
		log.V(1).Info("Backup is starting", "backup", cr.Name)
		status.State = psmdbv1.BackupStateRequested
	default:
		log.V(1).Info("Backup is running", "backup", cr.Name)
		status.State = psmdbv1.BackupStateRunning
	}

	status.Size = backupMeta.SizeH
	status.LastWrite = &metav1.Time{
		Time: time.Unix(backupMeta.LastWriteTS, 0),
	}
	status.LastTransition = &metav1.Time{
		Time: time.Unix(backupMeta.LastTransitionTS, 0),
	}
	status.Type = backupMeta.Type

	return status, nil
}

func (r *ReconcilePerconaServerMongoDBBackup) updateStatus(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup) error {
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &psmdbv1.PerconaServerMongoDBBackup{}

		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		c.Status = cr.Status

		return r.client.Status().Update(ctx, c)
	})

	if k8serrors.IsNotFound(err) {
		return nil
	}

	return errors.Wrap(err, "write status")
}
