package perconaservermongodbbackup

import (
	"context"
	"fmt"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
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

	pbmBackup "github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"
	pbmLog "github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/version"
)

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
	return &ReconcilePerconaServerMongoDBBackup{
		client:     mgr.GetClient(),
		scheme:     mgr.GetScheme(),
		newPBMFunc: backup.NewPBM,
	}
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
	client client.Client
	scheme *runtime.Scheme

	newPBMFunc backup.NewPBMFunc
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
		if cr.Status.State != status.State || cr.Status.Error != status.Error {
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

	bcp, err := r.newBackup(ctx, cluster)
	if err != nil {
		return rr, errors.Wrap(err, "create backup object")
	}
	defer bcp.Close(ctx)

	err = r.checkFinalizers(ctx, cr, cluster, bcp)
	if err != nil {
		return rr, errors.Wrap(err, "failed to run finalizer")
	}

	if cr.ObjectMeta.DeletionTimestamp != nil {
		return rr, nil
	}

	status, err = r.reconcile(ctx, cluster, cr, bcp)
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
	bcp *Backup,
) (psmdbv1.PerconaServerMongoDBBackupStatus, error) {
	log := logf.FromContext(ctx)
	status := cr.Status
	if cluster == nil {
		return status, errors.New("cluster not found")
	}

	if err := cluster.CanBackup(ctx); err != nil {
		return status, errors.Wrap(err, "failed to run backup")
	}

	cjobs, err := backup.HasActiveJobs(ctx, r.newPBMFunc, r.client, cluster, backup.NewBackupJob(cr.Name), backup.NotPITRLock)
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
		time.Sleep(10 * time.Second)
		return bcp.Start(ctx, r.client, cluster, cr)
	}

	time.Sleep(5 * time.Second)
	return bcp.Status(ctx, cr)
}

func (r *ReconcilePerconaServerMongoDBBackup) getPBMStorage(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, cr *psmdbv1.PerconaServerMongoDBBackup) (storage.Storage, error) {
	switch {
	case cr.Status.Azure != nil:
		if cr.Status.Azure.CredentialsSecret == "" {
			return nil, errors.New("no azure credentials specified for the secret name")
		}
		azureSecret, err := secret(ctx, r.client, cr.Namespace, cr.Status.Azure.CredentialsSecret)
		if err != nil {
			return nil, errors.Wrap(err, "getting azure credentials secret name")
		}
		azureConf := azure.Conf{
			Account:     string(azureSecret.Data[backup.AzureStorageAccountNameSecretKey]),
			Container:   cr.Status.Azure.Container,
			EndpointURL: cr.Status.Azure.EndpointURL,
			Prefix:      cr.Status.Azure.Prefix,
			Credentials: azure.Credentials{
				Key: string(azureSecret.Data[backup.AzureStorageAccountKeySecretKey]),
			},
		}
		return azure.New(azureConf, nil)
	case cr.Status.S3 != nil:
		if cr.Status.S3.CredentialsSecret == "" {
			return nil, errors.New("no s3 credentials specified for the secret name")
		}
		s3Conf := s3.Conf{
			Region:                cr.Status.S3.Region,
			EndpointURL:           cr.Status.S3.EndpointURL,
			Bucket:                cr.Status.S3.Bucket,
			Prefix:                cr.Status.S3.Prefix,
			UploadPartSize:        cr.Status.S3.UploadPartSize,
			MaxUploadParts:        cr.Status.S3.MaxUploadParts,
			StorageClass:          cr.Status.S3.StorageClass,
			InsecureSkipTLSVerify: cr.Status.S3.InsecureSkipTLSVerify,
		}
		s3secret, err := secret(ctx, r.client, cr.Namespace, cr.Status.S3.CredentialsSecret)
		if err != nil {
			return nil, errors.Wrap(err, "getting s3 credentials secret name")
		}

		if len(cr.Status.S3.ServerSideEncryption.SSECustomerAlgorithm) != 0 {
			switch {
			case len(cr.Status.S3.ServerSideEncryption.SSECustomerKey) != 0:
				s3Conf.ServerSideEncryption = &s3.AWSsse{
					SseCustomerAlgorithm: cr.Status.S3.ServerSideEncryption.SSECustomerAlgorithm,
					SseCustomerKey:       cr.Status.S3.ServerSideEncryption.SSECustomerKey,
				}
			case len(cluster.Spec.Secrets.SSE) != 0:
				sseSecret, err := secret(ctx, r.client, cr.Namespace, cluster.Spec.Secrets.SSE)
				if err != nil {
					return nil, errors.Wrap(err, "get sse credentials secret")
				}
				s3Conf.ServerSideEncryption = &s3.AWSsse{
					SseCustomerAlgorithm: cr.Status.S3.ServerSideEncryption.SSECustomerAlgorithm,
					SseCustomerKey:       string(sseSecret.Data[backup.SSECustomerKey]),
				}
			default:
				return nil, errors.New("no SseCustomerKey specified")
			}
		}

		if len(cr.Status.S3.ServerSideEncryption.SSEAlgorithm) != 0 {
			switch {
			case len(cr.Status.S3.ServerSideEncryption.KMSKeyID) != 0:
				s3Conf.ServerSideEncryption = &s3.AWSsse{
					SseAlgorithm: cr.Status.S3.ServerSideEncryption.SSEAlgorithm,
					KmsKeyID:     cr.Status.S3.ServerSideEncryption.KMSKeyID,
				}

			case len(cluster.Spec.Secrets.SSE) != 0:
				sseSecret, err := secret(ctx, r.client, cr.Namespace, cluster.Spec.Secrets.SSE)
				if err != nil {
					return nil, errors.Wrap(err, "get sse credentials secret")
				}
				s3Conf.ServerSideEncryption = &s3.AWSsse{
					SseAlgorithm: cr.Status.S3.ServerSideEncryption.SSEAlgorithm,
					KmsKeyID:     string(sseSecret.Data[backup.KMSKeyID]),
				}
			default:
				return nil, errors.New("no KmsKeyID specified")
			}
		}

		s3Conf.Credentials = s3.Credentials{
			AccessKeyID:     string(s3secret.Data[backup.AWSAccessKeySecretKey]),
			SecretAccessKey: string(s3secret.Data[backup.AWSSecretAccessKeySecretKey]),
		}
		return s3.New(s3Conf, nil)
	default:
		return nil, errors.New("no storage info in backup status")
	}
}

func secret(ctx context.Context, cl client.Client, namespace, secretName string) (*corev1.Secret, error) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
		},
	}
	err := cl.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret)
	return secret, err
}

func getPBMBackupMeta(cr *psmdbv1.PerconaServerMongoDBBackup) *pbmBackup.BackupMeta {
	meta := &pbmBackup.BackupMeta{
		Name:        cr.Status.PBMname,
		Compression: cr.Spec.Compression,
	}
	for _, rs := range cr.Status.ReplsetNames {
		meta.Replsets = append(meta.Replsets, pbmBackup.BackupReplset{
			Name:      rs,
			OplogName: fmt.Sprintf("%s_%s.oplog.gz", meta.Name, rs),
			DumpName:  fmt.Sprintf("%s_%s.dump.gz", meta.Name, rs),
		})
	}
	return meta
}

func (r *ReconcilePerconaServerMongoDBBackup) checkFinalizers(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup, cluster *psmdbv1.PerconaServerMongoDB, b *Backup) error {
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
				if err := r.deleteBackupFinalizer(ctx, cr, cluster, b); err != nil {
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

func (r *ReconcilePerconaServerMongoDBBackup) deleteBackupFinalizer(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup, cluster *psmdbv1.PerconaServerMongoDB, b *Backup) error {
	if len(cr.Status.PBMname) == 0 {
		return nil
	}

	var meta *backup.BackupMeta
	var err error

	if b.pbm != nil {
		meta, err = b.pbm.GetBackupMeta(ctx, cr.Status.PBMname)
		if err != nil {
			if !errors.Is(err, pbmErrors.ErrNotFound) {
				return errors.Wrap(err, "get backup meta")
			}
			meta = nil
		}
	}
	if b.pbm == nil || meta == nil {
		stg, err := r.getPBMStorage(ctx, cluster, cr)
		if err != nil {
			return errors.Wrap(err, "get storage")
		}
		if err := pbmBackup.DeleteBackupFiles(getPBMBackupMeta(cr), stg); err != nil {
			return errors.Wrap(err, "failed to delete backup files with dummy PBM")
		}
		return nil
	}

	if cluster == nil {
		return errors.Errorf("PerconaServerMongoDB %s is not found", cr.Spec.GetClusterName())
	}

	var storage psmdbv1.BackupStorageSpec
	switch {
	case cr.Status.S3 != nil:
		storage.Type = psmdbv1.BackupStorageS3
		storage.S3 = *cr.Status.S3
	case cr.Status.Azure != nil:
		storage.Type = psmdbv1.BackupStorageAzure
		storage.Azure = *cr.Status.Azure
	}

	err = b.pbm.SetConfig(ctx, r.client, cluster, storage)
	if err != nil {
		return errors.Wrapf(err, "set backup config with storage %s", cr.Spec.StorageName)
	}
	e := b.pbm.Logger().NewEvent(string(ctrl.CmdDeleteBackup), "", "", primitive.Timestamp{})
	// We should delete PITR oplog chunks until `LastWriteTS` of the backup,
	// as it's not possible to delete backup if it is a base for the PITR timeline
	err = r.deletePITR(ctx, b, meta.LastWriteTS, e)
	if err != nil {
		return errors.Wrap(err, "failed to delete PITR")
	}
	err = b.pbm.DeleteBackup(ctx, cr.Status.PBMname)
	if err != nil {
		return errors.Wrap(err, "failed to delete backup")
	}
	return nil
}

// deletePITR deletes PITR oplog chunks whose StartTS is less or equal to the `until` timestamp. Deletes all chunks if `until` is 0.
func (r *ReconcilePerconaServerMongoDBBackup) deletePITR(ctx context.Context, b *Backup, until primitive.Timestamp, e pbmLog.LogEvent) error {
	log := logf.FromContext(ctx)

	stg, err := b.pbm.GetStorage(ctx, e)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	chunks, err := b.pbm.PITRGetChunksSlice(ctx, "", primitive.Timestamp{}, until)
	if err != nil {
		return errors.Wrap(err, "get pitr chunks")
	}
	if len(chunks) == 0 {
		log.Info("nothing to delete")
	}

	for _, chnk := range chunks {
		err = stg.Delete(chnk.FName)
		if err != nil && err != storage.ErrNotExist {
			return errors.Wrapf(err, "delete pitr chunk '%s' (%v) from storage", chnk.FName, chnk)
		}

		_, err = b.pbm.PITRChunksCollection().DeleteOne(
			ctx,
			bson.D{
				{Key: "rs", Value: chnk.RS},
				{Key: "start_ts", Value: chnk.StartTS},
				{Key: "end_ts", Value: chnk.EndTS},
			},
		)
		if err != nil {
			return errors.Wrap(err, "delete pitr chunk metadata")
		}

		log.Info("deleted " + chnk.FName)
	}
	return nil
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
