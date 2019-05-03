package perconaservermongodbrestore

import (
	"context"
	"fmt"

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

var log = logf.Log.WithName("controller_perconaservermongodbrestore")

/**
* USER ACTION REQUIRED: This is a scaffold file intended for the user to modify with their own Controller
* business logic.  Delete these comments after modifying this file.*
 */

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
	err = c.Watch(&source.Kind{Type: &psmdbv1alpha1.PerconaServerMongoDBRestore{}}, &handler.EnqueueRequestForObject{})
	if err != nil {
		return err
	}

	// TODO(user): Modify this to be the types you create that are owned by the primary resource
	// Watch for changes to secondary resource Pods and requeue the owner PerconaServerMongoDBRestore
	err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
		IsController: true,
		OwnerType:    &psmdbv1alpha1.PerconaServerMongoDBRestore{},
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
// TODO(user): Modify this Reconcile function to implement your Controller logic.  This example creates
// a Pod as an example
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDBRestore) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	// Fetch the PerconaSMDBBackupRestore instance
	instance := &psmdbv1alpha1.PerconaServerMongoDBRestore{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	if instance.Status.State == psmdbv1alpha1.RestoreStateRequested {
		return reconcile.Result{}, nil
	}
	cluster, err := r.checkBackup(instance, request)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("check backup: %v", err)
	}
	if len(cluster) == 0 {
		return reconcile.Result{}, fmt.Errorf("backup is not exist")
	}

	restoreHandler, err := newRestoreHandler(cluster)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("create handler: %v", err)
	}

	err = restoreHandler.StartRestore(instance)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("start restore: %v", err)
	}

	instance.Status.State = psmdbv1alpha1.RestoreStateRequested

	r.updateStatus(instance)
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("update status: %v", err)
	}

	return reconcile.Result{}, nil
}

// checkBackup return cluster name if backup exist
func (r *ReconcilePerconaServerMongoDBRestore) checkBackup(cr *psmdbv1alpha1.PerconaServerMongoDBRestore, request reconcile.Request) (string, error) {
	backups := &psmdbv1alpha1.PerconaServerMongoDBBackupList{}
	err := r.client.List(context.TODO(), &client.ListOptions{
		Namespace: request.Namespace,
	},
		backups)
	if err != nil {
		if errors.IsNotFound(err) {
			return "", nil
		}
		return "", err
	}
	for _, v := range backups.Items {
		if v.Spec.StorageName == cr.Spec.StorageName {
			if v.Status.Destination == cr.Spec.BackupFileName {
				return v.Spec.PSMDBCluster, nil
			}
		}
	}
	return "", nil
}

// newPodForCR returns a busybox pod with the same name/namespace as the cr
func newPodForCR(cr *psmdbv1alpha1.PerconaServerMongoDBRestore) *corev1.Pod {
	labels := map[string]string{
		"app": cr.Name,
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pod",
			Namespace: cr.Namespace,
			Labels:    labels,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:    "busybox",
					Image:   "busybox",
					Command: []string{"sleep", "3600"},
				},
			},
		},
	}
}

func (r *ReconcilePerconaServerMongoDBRestore) updateStatus(cr *psmdbv1alpha1.PerconaServerMongoDBRestore) error {
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
