package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"time"

	psmdbv1alpha1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
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
	err = c.Watch(&source.Kind{Type: &psmdbv1alpha1.PerconaServerMongoDBBackup{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner PerconaServerMongoDBBackup
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &psmdbv1alpha1.PerconaServerMongoDBBackup{},
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
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDBBackup) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling PerconaServerMongoDBBackup")
	rr := reconcile.Result{
		RequeueAfter: time.Second * 5,
	}
	// Fetch the PerconaServerMongoDBBackup instance
	instance := &psmdbv1alpha1.PerconaServerMongoDBBackup{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			log.Info("Backup get:" + err.Error())
			return rr, nil
		}
		// Error reading the object - requeue the request.
		log.Info("Backup get:" + err.Error())
		return rr, err
	}

	err = r.reconcileBC(instance)
	if err != nil {
		log.Error(err, "BackupHandler: ")
		return rr, err
	}

	err = r.updateStatus(instance)
	if err != nil {
		log.Info("Backup status:" + err.Error())
		return rr, err
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDBBackup) reconcileBC(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) error {
	if len(cr.Name) == 0 || len(cr.Spec.StorageName) == 0 || len(cr.Spec.PSMDBCluster) == 0 {
		return fmt.Errorf("not enough data")
	}
	bh, err := newBackupHandler(cr.Spec.PSMDBCluster, cr.Name, cr.Spec.StorageName)
	if err != nil {
		return err
	}

	exist, _, err := bh.BackupExist()
	if err != nil {
		return err
	}
	if exist {
		cr.Status = setupNewStatus(bh.BackupData)
		return nil
	}
	log.Info("Backup handling: Start backup")
	err = bh.StartBackup()
	if err != nil {
		log.Info("Backup handling err: " + err.Error())
		return err
	}
	cr.Status = setupNewStatus(bh.BackupData)

	return nil
}

func setupNewStatus(backupData BackupData) (status psmdbv1alpha1.PerconaServerMongoDBBackupStatus) {
	status.StorageName = backupData.StorageName
	status.StartAt = &metav1.Time{
		Time: time.Unix(backupData.Start, 0),
	}
	status.CompletedAt = &metav1.Time{
		Time: time.Unix(backupData.End, 0),
	}
	status.State = backupData.Status
	return
}

func (r *ReconcilePerconaServerMongoDBBackup) updateStatus(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) error {
	err := r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		// may be it's k8s v1.10 and erlier (e.g. oc3.9) that doesn't support status updates
		// so try to update whole CR
		err := r.client.Update(context.TODO(), cr)
		if err != nil {
			return fmt.Errorf("send update: %v", err)
		}
	}
	return nil
}
