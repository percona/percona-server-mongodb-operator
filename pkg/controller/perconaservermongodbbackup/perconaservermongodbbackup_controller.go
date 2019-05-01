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
	//reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)

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

	err = instance.CheckFields()
	if err != nil {
		return rr, fmt.Errorf("fields check: %v", err)
	}

	if instance.Status.State == psmdbv1alpha1.StateReady {
		return rr, nil
	}

	err = r.reconcileBC(instance)
	if err != nil {
		return rr, fmt.Errorf("reconcile backup: " + err.Error())
	}

	err = r.updateStatus(instance)
	if err != nil {
		return rr, fmt.Errorf("update status:" + err.Error())
	}

	return rr, nil
}

func (r *ReconcilePerconaServerMongoDBBackup) reconcileBC(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) error {
	bh, err := newBackupHandler(cr)
	if err != nil {
		return fmt.Errorf("handler creation: %v", err)
	}
	if len(cr.Status.State) == 0 {
		cr.Status = psmdbv1alpha1.PerconaServerMongoDBBackupStatus{
			StorageName: cr.Spec.StorageName,
			StartAt:     &metav1.Time{},
			CompletedAt: &metav1.Time{},
		}
	}
	backupStatus, err := bh.CheckBackup(cr)
	if err != nil {
		return fmt.Errorf("check and update backup: %v", err)
	}
	if len(backupStatus.State) > 0 {
		cr.Status = backupStatus
		return nil
	}

	backupStatus, err = bh.StartBackup(cr)
	if err != nil {
		return fmt.Errorf("start backup: %v", err)
	}
	if len(backupStatus.State) > 0 {
		cr.Status = backupStatus
		return nil
	}
	return nil
}

func (r *ReconcilePerconaServerMongoDBBackup) updateStatus(cr *psmdbv1alpha1.PerconaServerMongoDBBackup) error {
	err := r.client.Status().Update(context.TODO(), cr)
	if err != nil {
		// may be it's k8s v1.10 and erlier (e.g. oc3.9) that doesn't support status updates
		// so try to update whole CR
		//TODO: Update will not return error if user have no rights to update Status. Do we need to do something?
		err := r.client.Update(context.TODO(), cr)
		if err != nil {
			return fmt.Errorf("send update: %v", err)
		}
	}
	return nil
}
