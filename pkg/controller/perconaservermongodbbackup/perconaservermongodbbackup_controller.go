package perconaservermongodbbackup

import (
	"context"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/version"
)

var log = logf.Log.WithName("controller_perconaservermongodbbackup")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

// Add creates a new PerconaServerMongoDBBackup Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	return &ReconcilePerconaServerMongoDBBackup{client: mgr.GetClient(), scheme: mgr.GetScheme()}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("perconaservermongodbbackup-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	// Watch for changes to primary resource PerconaServerMongoDBBackup
	err = c.Watch(&source.Kind{Type: &psmdbv1.PerconaServerMongoDBBackup{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner PerconaServerMongoDBBackup
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &psmdbv1.PerconaServerMongoDBBackup{},
	})
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
	client client.Client
	scheme *runtime.Scheme
}

// Reconcile reads that state of the cluster for a PerconaServerMongoDBBackup object and makes changes based on the state read
// and what is in the PerconaServerMongoDBBackup.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDBBackup) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	rr := reconcile.Result{
		RequeueAfter: time.Second * 5,
	}
	// Fetch the PerconaServerMongoDBBackup instance
	cr := &psmdbv1.PerconaServerMongoDBBackup{}
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
		if cr.Status.State != status.State || cr.Status.Error != status.Error {
			cr.Status = status
			uerr := r.updateStatus(cr)
			if uerr != nil {
				log.Error(uerr, "failed to update backup status", "backup", cr.Name)
			}
		}
	}()

	err = cr.CheckFields()
	if err != nil {
		return rr, errors.Wrap(err, "fields check")
	}

	cluster := &api.PerconaServerMongoDB{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Spec.PSMDBCluster, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return rr, errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.PSMDBCluster)
	}

	svr, err := version.Server()
	if err != nil {
		return rr, errors.Wrapf(err, "fetch server version")
	}

	if err := cluster.CheckNSetDefaults(svr.Platform, log); err != nil {
		return rr, errors.Wrapf(err, "set defaults for %s/%s", cluster.Namespace, cluster.Name)
	}

	bcp, err := r.newBackup(cluster, cr)
	if err != nil {
		return rr, errors.Wrap(err, "create backup object")
	}
	defer bcp.Close()

	err = r.checkFinalizers(cr, bcp)
	if err != nil {
		return rr, errors.Wrap(err, "failed to run finalizer")
	}

	status, err = r.reconcile(cluster, cr, bcp)
	if err != nil {
		return rr, errors.Wrap(err, "reconcile backup")
	}

	return rr, nil
}

// reconcile backup. firstly we check if there are concurrent jobs running
func (r *ReconcilePerconaServerMongoDBBackup) reconcile(
	cluster *psmdbv1.PerconaServerMongoDB,
	cr *psmdbv1.PerconaServerMongoDBBackup,
	bcp *Backup,
) (psmdbv1.PerconaServerMongoDBBackupStatus, error) {
	status := cr.Status

	if err := cluster.CanBackup(); err != nil {
		return status, errors.Wrap(err, "failed to run backup")
	}

	cjobs, err := backup.HasActiveJobs(r.client, cluster, backup.NewBackupJob(cr.Name), backup.NotPITRLock)
	if err != nil {
		return status, errors.Wrap(err, "check for concurrent jobs")
	}

	if cjobs {
		if cr.Status.State != psmdbv1.BackupStateWaiting {
			log.Info("Waiting to finish another backup/restore.")
		}
		status.State = psmdbv1.BackupStateWaiting
		return status, nil
	}

	if cr.Status.State == psmdbv1.BackupStateNew || cr.Status.State == psmdbv1.BackupStateWaiting {
		priorities, err := bcp.pbm.GetPriorities(r.client, cluster)
		if err != nil {
			return status, errors.Wrap(err, "get PBM priorities")
		}
		time.Sleep(10 * time.Second)
		return bcp.Start(cr, priorities)
	}

	time.Sleep(1 * time.Minute)
	return bcp.Status(cr)
}

func (r *ReconcilePerconaServerMongoDBBackup) checkFinalizers(cr *psmdbv1.PerconaServerMongoDBBackup, b *Backup) error {
	var err error
	if cr.ObjectMeta.DeletionTimestamp == nil {
		return nil
	}

	finalizers := []string{}

	if cr.Status.State == psmdbv1.BackupStateReady {
		for _, f := range cr.GetFinalizers() {
			switch f {
			case "delete-backup":
				if len(cr.Status.PBMname) == 0 {
					continue
				}

				e := b.pbm.C.Logger().NewEvent(string(pbm.CmdDeleteBackup), "", "", primitive.Timestamp{})
				err = b.pbm.C.DeleteBackup(cr.Status.PBMname, e)
				if err != nil {
					log.Error(err, "failed to run finalizer", "finalizer", f)
					finalizers = append(finalizers, f)
				}
			}
		}
	}

	cr.SetFinalizers(finalizers)
	err = r.client.Update(context.TODO(), cr)

	return err
}

func (r *ReconcilePerconaServerMongoDBBackup) updateStatus(cr *psmdbv1.PerconaServerMongoDBBackup) error {
	err := r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		// may be it's k8s v1.10 and erlier (e.g. oc3.9) that doesn't support status updates
		// so try to update whole CR
		// TODO: Update will not return error if user have no rights to update Status. Do we need to do something?
		err := r.client.Update(context.TODO(), cr)
		if err != nil {
			return errors.Wrap(err, "send update")
		}
	}
	return nil
}
