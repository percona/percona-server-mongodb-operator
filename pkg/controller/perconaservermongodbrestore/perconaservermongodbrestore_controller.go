package perconaservermongodbrestore

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/pkg/errors"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
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
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pbm"
)

// Add creates a new PerconaServerMongoDBRestore Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	r, err := newReconciler(mgr)
	if err != nil {
		return err
	}

	return add(mgr, r)
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) (reconcile.Reconciler, error) {
	cli, err := clientcmd.NewClient()
	if err != nil {
		return nil, errors.Wrap(err, "create clientcmd")
	}

	return &ReconcilePerconaServerMongoDBRestore{
		client:    mgr.GetClient(),
		scheme:    mgr.GetScheme(),
		clientcmd: cli,
	}, nil
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("psmdbrestore-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource PerconaServerMongoDBRestore
	err = c.Watch(source.Kind(mgr.GetCache(), new(psmdbv1.PerconaServerMongoDBRestore)), new(handler.EnqueueRequestForObject))
	if err != nil {
		return err
	}

	// Watch for changes to secondary resource Pods and requeue the owner PerconaServerMongoDBRestore
	err = c.Watch(source.Kind(mgr.GetCache(), new(corev1.Pod)), handler.EnqueueRequestForOwner(
		mgr.GetScheme(), mgr.GetRESTMapper(), new(psmdbv1.PerconaServerMongoDBRestore), handler.OnlyControllerOwner(),
	))
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
	client    client.Client
	scheme    *runtime.Scheme
	clientcmd *clientcmd.Client
}

const leaseName = "percona-server-mongodb-restore"

// Reconcile reads that state of the cluster for a PerconaServerMongoDBRestore object and makes changes based on the state read
// and what is in the PerconaServerMongoDBRestore.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDBRestore) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	rr := reconcile.Result{
		RequeueAfter: time.Second * 5,
	}

	// Fetch the PerconaSMDBBackupRestore instance
	cr := &psmdbv1.PerconaServerMongoDBRestore{}
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

	status := cr.Status

	defer func() {
		if err != nil {
			status.State = psmdbv1.RestoreStateError
			status.Error = err.Error()
			log.Error(err, "failed to make restore", "restore", cr.Name, "backup", cr.Spec.BackupName)
		}
		if cr.Status.State != status.State || cr.Status.Error != status.Error || !reflect.DeepEqual(cr.Status.Conditions, status.Conditions) {
			log.Info("Restore state changed", "previous", cr.Status.State, "current", status.State)
			cr.Status = status
			uerr := r.updateStatus(ctx, cr)
			if uerr != nil {
				log.Error(uerr, "failed to updated restore status", "restore", cr.Name, "backup", cr.Spec.BackupName)
			}
		}

		if cr.Status.State == psmdbv1.RestoreStateReady || cr.Status.State == psmdbv1.RestoreStateError {
			if err = k8s.DeleteLease(ctx, r.client, cr.Namespace, leaseName, cr.Name); err != nil {
				log.Error(err, "delete lease for restore")
			}
		}
	}()

	err = cr.CheckFields()
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("fields check: %v", err)
	}

	switch cr.Status.State {
	case psmdbv1.RestoreStateReady, psmdbv1.RestoreStateError:
		return reconcile.Result{}, nil
	}

	gotLease, err := k8s.GetLease(ctx, r.client, cr.Namespace, leaseName, cr.Name)
	if err != nil {
		return rr, errors.Wrap(err, "get lease for restore")
	}

	if !gotLease {
		log.Info("Another restore is in progress")
		return rr, nil
	}

	bcp, err := r.getBackup(ctx, cr)
	if err != nil {
		return rr, errors.Wrap(err, "get backup")
	}

	switch bcp.Status.State {
	case psmdbv1.BackupStateError:
		err = errors.New("backup is in error state")
		return rr, nil
	case psmdbv1.BackupStateReady:
	default:
		return reconcile.Result{}, errors.New("backup is not ready")
	}

	if meta.FindStatusCondition(status.Conditions, "PBMIsConfigured") == nil {
		cluster := &psmdbv1.PerconaServerMongoDB{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cr.Spec.ClusterName,
				Namespace: cr.Namespace,
			},
		}
		err = r.client.Get(ctx, client.ObjectKeyFromObject(cluster), cluster)
		if err != nil {
			return rr, errors.Wrapf(err, "get cluster %s", client.ObjectKeyFromObject(cluster))
		}

		var pbmClient *pbm.PBM
		restoreRunning := false
		for _, rs := range cluster.Spec.Replsets {
			restoreRunning, err = r.restoreInProgress(ctx, cluster, rs)
			if err != nil {
				return rr, errors.Wrap(err, "check restore in progress")
			}

			if restoreRunning {
				break
			}
		}

		if restoreRunning {
			pbmClient, err = pbm.New(ctx, r.clientcmd, r.client, cluster, pbm.WithContainerName(psmdb.MongodContainerName), pbm.WithPBMPath(pbm.PhysicalRestorePBMPath))
			if err != nil {
				return rr, errors.Wrap(err, "create pbm client")
			}
		} else {
			pbmClient, err = pbm.New(ctx, r.clientcmd, r.client, cluster)
			if err != nil {
				return rr, errors.Wrap(err, "create pbm client")
			}
		}

		if cr.Spec.BackupSource == nil {
			log.Info("Setting PBM config for storage", "restore", cr.Name, "storage", bcp.Spec.StorageName)
			err = pbmClient.SetConfigFile(ctx, pbm.GetConfigPathForStorage(bcp.Spec.StorageName))
			if err != nil {
				return rr, errors.Wrapf(err, "set pbm config for storage %s", bcp.Spec.StorageName)
			}
		} else {
			var stg psmdbv1.BackupStorageSpec
			var ok bool
			switch {
			case cr.Spec.StorageName != "":
				stg, ok = cluster.Spec.Backup.Storages[cr.Spec.StorageName]
				if !ok {
					return reconcile.Result{}, errors.Errorf("storage %s not found in cluster spec", cr.Spec.StorageName)
				}
			case bcp.Status.S3 != nil:
				stg = psmdbv1.BackupStorageSpec{
					Type: storage.S3,
					S3:   *bcp.Status.S3,
				}
			case bcp.Status.Azure != nil:
				stg = psmdbv1.BackupStorageSpec{
					Type:  storage.Azure,
					Azure: *bcp.Status.Azure,
				}
			}
			log.Info("Setting PBM storage config for backup source", "restore", cr.Name, "storage", stg)
			if err := pbmClient.SetStorageConfig(ctx, stg); err != nil {
				return rr, errors.Wrapf(err, "set pbm storage config for backup source")
			}
		}

		err = pbmClient.DisablePITR(ctx)
		if err != nil {
			return rr, errors.Wrap(err, "set pbm config")
		}

		if bcp.Spec.StorageName != "" {
			err = retry.RetryOnConflict(retry.DefaultRetry, func() error {
				c := &psmdbv1.PerconaServerMongoDB{}
				err := r.client.Get(ctx, client.ObjectKeyFromObject(cluster), c)
				if err != nil {
					return err
				}

				c.Status.BackupStorage = bcp.Spec.StorageName

				return r.client.Status().Update(ctx, c)
			})
			if err != nil {
				return rr, errors.Wrap(err, "update cluster status")
			}
		}

		// Set the PBMIsConfigured condition to true
		meta.SetStatusCondition(&status.Conditions, metav1.Condition{
			Type:               "PBMIsConfigured",
			Status:             metav1.ConditionTrue,
			Reason:             "PBMIsConfigured",
			Message:            "PBM is configured",
			LastTransitionTime: metav1.Now(),
		})

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		timeout := time.NewTimer(900 * time.Second)
		defer timeout.Stop()

	outer:
		for {
			select {
			case <-timeout.C:
				return rr, errors.Errorf("timeout while waiting PBM operation to finish")
			case <-ticker.C:
				var running pbm.Running
				err := retry.OnError(retry.DefaultBackoff, func(err error) bool { return true }, func() error {
					running, err = pbmClient.GetRunningOperation(ctx)
					return err
				})
				if err != nil {
					return rr, errors.Wrapf(err, "check running operations status")
				}

				if running.OpID == "" {
					break outer
				}

				log.Info("Waiting for PBM operation to finish", "operation", running.Name, "type", running.Type, "opid", running.OpID)
			}
		}

		return rr, nil
	}

	switch bcp.Status.Type {
	case "", defs.LogicalBackup:
		status, err = r.reconcileLogicalRestore(ctx, cr, bcp)
		if err != nil {
			return rr, errors.Wrap(err, "reconcile logical restore")
		}
	case defs.PhysicalBackup:
		status, err = r.reconcilePhysicalRestore(ctx, cr, bcp)
		if err != nil {
			return rr, errors.Wrap(err, "reconcile physical restore")
		}
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDBRestore) getBackup(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore) (*psmdbv1.PerconaServerMongoDBBackup, error) {
	if len(cr.Spec.BackupName) == 0 && cr.Spec.BackupSource != nil {
		s := strings.Split(cr.Spec.BackupSource.Destination, "/")
		backupName := s[len(s)-1]

		return &psmdbv1.PerconaServerMongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:      cr.Name,
				Namespace: cr.Namespace,
			},
			Spec: psmdbv1.PerconaServerMongoDBBackupSpec{
				ClusterName: cr.Spec.ClusterName,
				StorageName: cr.Spec.StorageName,
			},
			Status: psmdbv1.PerconaServerMongoDBBackupStatus{
				Type:        cr.Spec.BackupSource.Type,
				State:       psmdbv1.BackupStateReady,
				Destination: cr.Spec.BackupSource.Destination,
				StorageName: cr.Spec.StorageName,
				S3:          cr.Spec.BackupSource.S3,
				Azure:       cr.Spec.BackupSource.Azure,
				PBMName:     backupName,
			},
		}, nil
	}

	backup := &psmdbv1.PerconaServerMongoDBBackup{}
	err := r.client.Get(ctx, types.NamespacedName{
		Name:      cr.Spec.BackupName,
		Namespace: cr.Namespace,
	}, backup)

	return backup, err
}

func (r *ReconcilePerconaServerMongoDBRestore) updateStatus(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore) error {
	var backoff = wait.Backoff{
		Steps:    5,
		Duration: 500 * time.Millisecond,
		Factor:   5.0,
		Jitter:   0.1,
	}

	err := retry.OnError(backoff, func(error) bool { return true }, func() error {
		c := &psmdbv1.PerconaServerMongoDBRestore{}

		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		c.Status = cr.Status

		err = r.client.Status().Update(ctx, c)
		if err != nil {
			return err
		}

		// ensure status is updated
		c = &psmdbv1.PerconaServerMongoDBRestore{}
		err = r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		if c.Status.State != cr.Status.State {
			return errors.New("status not updated")
		}

		return nil
	})

	if k8serrors.IsNotFound(err) {
		return nil
	}

	return errors.Wrap(err, "write status")
}

func (r *ReconcilePerconaServerMongoDBRestore) restoreInProgress(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB, replset *psmdbv1.ReplsetSpec) (bool, error) {
	sts := appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + replset.Name,
			Namespace: cr.Namespace,
		},
	}

	if err := r.client.Get(ctx, client.ObjectKeyFromObject(&sts), &sts); err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrapf(err, "get statefulset %s", sts.Name)
	}

	_, ok := sts.Annotations[psmdbv1.AnnotationRestoreInProgress]
	return ok, nil
}
