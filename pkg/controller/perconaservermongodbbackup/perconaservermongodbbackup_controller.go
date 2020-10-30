package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"time"

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
	instance := &psmdbv1.PerconaServerMongoDBBackup{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
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

	err = instance.CheckFields()
	if err != nil {
		return rr, errors.Wrap(err, "fields check")
	}

	switch instance.Status.State {
	case psmdbv1.BackupStateReady, psmdbv1.BackupStateError:
		return rr, nil
	}

	err = r.reconcile(instance)
	if err != nil {
		return rr, errors.Wrap(err, "reconcile backup")
	}

	return rr, nil
}

// reconcile backup. first we chek if there are concurrent job running
func (r *ReconcilePerconaServerMongoDBBackup) reconcile(cr *psmdbv1.PerconaServerMongoDBBackup) (err error) {
	status := cr.Status

	defer func() {
		if err != nil {
			status.State = psmdbv1.BackupStateError
			status.Error = err.Error()
			log.Error(err, "failed to make restore", "backup", cr.Name)
		}
		if cr.Status.State != status.State {
			cr.Status = status
			uerr := r.updateStatus(cr)
			if uerr != nil {
				log.Error(uerr, "failed to updated restore status", "backup", cr.Name)
			}
		}
	}()

	cluster := &api.PerconaServerMongoDB{}
	err = r.client.Get(context.TODO(), types.NamespacedName{Name: cr.Spec.PSMDBCluster, Namespace: cr.Namespace}, cluster)
	if err != nil {
		return errors.Wrapf(err, "get cluster %s/%s", cr.Namespace, cr.Spec.PSMDBCluster)
	}

	if cluster.Status.State != api.AppStateReady {
		return fmt.Errorf("failed to run backup on cluster with status %s", cluster.Status.State)
	}

	cjobs, err := backup.HasActiveJobs(r.client, cr.Spec.PSMDBCluster, cr.Namespace, backup.Job{Name: cr.Name, Type: backup.TypeBackup})
	if err != nil {
		return errors.Wrap(err, "check for concurrent jobs")
	}
	if cjobs {
		if status.State != psmdbv1.BackupStateWaiting {
			log.Info("Waiting to finish another backup/restore.")
		}
		status.State = psmdbv1.BackupStateWaiting
		return nil
	}

	bcp, err := r.newBackup(cr)
	if err != nil {
		return errors.Wrap(err, "create backup object")
	}
	defer bcp.Close()

	if cr.Status.State == psmdbv1.BackupStateNew || cr.Status.State == psmdbv1.BackupStateWaiting {
		status, err = bcp.Start(cr)
		return err
	}

	status, err = bcp.Status(cr)
	return err
}

func (r *ReconcilePerconaServerMongoDBBackup) updateStatus(cr *psmdbv1.PerconaServerMongoDBBackup) error {
	err := r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		// may be it's k8s v1.10 and erlier (e.g. oc3.9) that doesn't support status updates
		// so try to update whole CR
		//TODO: Update will not return error if user have no rights to update Status. Do we need to do something?
		err := r.client.Update(context.TODO(), cr)
		if err != nil {
			return errors.Wrap(err, "send update")
		}
	}
	return nil
}
