package perconaservermongodbrestore

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm"
	"github.com/pkg/errors"
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

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
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
	client    client.Client
	scheme    *runtime.Scheme
	clientcmd *clientcmd.Client
}

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
		if cr.Status.State != status.State || cr.Status.Error != status.Error {
			log.Info("Restore state changed", "previous", cr.Status.State, "current", status.State)
			cr.Status = status
			uerr := r.updateStatus(ctx, cr)
			if uerr != nil {
				log.Error(uerr, "failed to updated restore status", "restore", cr.Name, "backup", cr.Spec.BackupName)
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

	bcp, err := r.getBackup(ctx, cr)
	if err != nil {
		return rr, errors.Wrap(err, "get backup")
	}

	if bcp.Status.State != psmdbv1.BackupStateReady {
		return reconcile.Result{}, errors.New("backup is not ready")
	}

	switch bcp.Status.Type {
	case "", pbm.LogicalBackup:
		status, err = r.reconcileLogicalRestore(ctx, cr, bcp)
		if err != nil {
			return rr, errors.Wrap(err, "reconcile logical restore")
		}
	case pbm.PhysicalBackup:
		status, err = r.reconcilePhysicalRestore(ctx, cr, bcp)
		if err != nil {
			return rr, errors.Wrap(err, "reconcile physical restore")
		}
	}

	return rr, nil
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

func (r *ReconcilePerconaServerMongoDBRestore) getBackup(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBRestore) (*psmdbv1.PerconaServerMongoDBBackup, error) {
	if len(cr.Spec.BackupName) == 0 && cr.Spec.BackupSource != nil {
		s := strings.Split(cr.Spec.BackupSource.Destination, "/")
		backupName := s[len(s)-1]

		return &psmdbv1.PerconaServerMongoDBBackup{
			ObjectMeta: metav1.ObjectMeta{
				Name:        cr.Name,
				Namespace:   cr.Namespace,
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
				PBMname:     backupName,
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
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &psmdbv1.PerconaServerMongoDBRestore{}

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
