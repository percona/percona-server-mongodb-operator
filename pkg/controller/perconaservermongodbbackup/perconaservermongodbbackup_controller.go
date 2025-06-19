package perconaservermongodbbackup

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	pbmBackup "github.com/percona/percona-backup-mongodb/pbm/backup"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	pbmErrors "github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"

	"github.com/percona/percona-server-mongodb-operator/clientcmd"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
	"github.com/percona/percona-server-mongodb-operator/pkg/version"
)

// Add creates a new PerconaServerMongoDBBackup Controller and adds it to the Manager. The Manager will set fields on the Controller
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
	cli, err := clientcmd.NewClient(mgr.GetConfig())
	if err != nil {
		return nil, errors.Wrap(err, "create clientcmd")
	}

	return &ReconcilePerconaServerMongoDBBackup{
		client:     mgr.GetClient(),
		apiReader:  mgr.GetAPIReader(),
		scheme:     mgr.GetScheme(),
		newPBMFunc: backup.NewPBM,
		clientcmd:  cli,
	}, nil
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler

func add(mgr manager.Manager, r reconcile.Reconciler) error {
	return builder.ControllerManagedBy(mgr).
		Named("psmdbbackup-controller").
		For(&psmdbv1.PerconaServerMongoDBBackup{}).
		Watches(
			&corev1.Pod{},
			handler.EnqueueRequestForOwner(
				mgr.GetScheme(), mgr.GetRESTMapper(),
				&psmdbv1.PerconaServerMongoDBBackup{},
				handler.OnlyControllerOwner(),
			),
		).
		Complete(r)
}

var _ reconcile.Reconciler = &ReconcilePerconaServerMongoDBBackup{}

// ReconcilePerconaServerMongoDBBackup reconciles a PerconaServerMongoDBBackup object
type ReconcilePerconaServerMongoDBBackup struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client    client.Client
	apiReader client.Reader
	scheme    *runtime.Scheme
	clientcmd *clientcmd.Client

	newPBMFunc backup.NewPBMFunc
}

// Reconcile reads that state of the cluster for a PerconaServerMongoDBBackup object and makes changes based on the state read
// and what is in the PerconaServerMongoDBBackup.Spec
// Note:
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
func (r *ReconcilePerconaServerMongoDBBackup) Reconcile(ctx context.Context, request reconcile.Request) (reconcile.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("Reconciling")
	defer log.V(1).Info("Reconcile finished")

	rr := reconcile.Result{
		RequeueAfter: time.Second * 5,
	}
	// Fetch the PerconaServerMongoDBBackup instance
	cr := &psmdbv1.PerconaServerMongoDBBackup{}

	// Here we use k8s APIReader to read the k8s object by making the
	// direct call to k8s apiserver instead of using k8sClient.
	// The reason is that k8sClient uses a cache and sometimes k8sClient can
	// return stale copy of object.
	// It is okay to make direct call to k8s apiserver because we are only
	// making single read call for complete reconciler loop.
	err := r.apiReader.Get(ctx, request.NamespacedName, cr)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return rr, err
	}

	log.V(1).Info("Got object from API server", "state", cr.Status.State)

	if (cr.Status.State == psmdbv1.BackupStateReady || cr.Status.State == psmdbv1.BackupStateError) &&
		cr.ObjectMeta.DeletionTimestamp == nil {
		return reconcile.Result{}, nil
	}

	status := cr.Status

	defer func() {
		if err != nil {
			status.State = psmdbv1.BackupStateError
			status.Error = err.Error()
			log.Error(err, "failed to make backup", "backup", cr.Name)
		}
		if cr.Status.State != status.State || cr.Status.Error != status.Error {
			log.Info("Backup state changed", "previous", cr.Status.State, "current", status.State)
			cr.Status = status
			uerr := r.updateStatus(ctx, cr)
			if uerr != nil {
				log.Error(uerr, "failed to update backup status", "backup", cr.Name)
			}

			switch cr.Status.State {
			case psmdbv1.BackupStateReady, psmdbv1.BackupStateError:
				log.Info("Releasing backup lock", "lease", naming.BackupLeaseName(cr.Spec.ClusterName))

				err := k8s.ReleaseLease(ctx, r.client, naming.BackupLeaseName(cr.Spec.ClusterName), cr.Namespace, naming.BackupHolderId(cr))
				if err != nil {
					log.Error(err, "failed to release the lock")
				}
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

	if err = checkStartingDeadline(ctx, cluster, cr); err != nil {
		return reconcile.Result{}, err
	}

	if cluster != nil {
		var svr *version.ServerVersion
		svr, err = version.Server(r.clientcmd)
		if err != nil {
			return rr, errors.Wrapf(err, "fetch server version")
		}

		err = cluster.CheckNSetDefaults(ctx, svr.Platform)
		if err != nil {
			return reconcile.Result{}, errors.Wrapf(err, "invalid cr oprions used for %s/%s", cluster.Namespace, cluster.Name)
		}
	}

	var bcp *Backup
	if err = retry.OnError(defaultBackoff, func(err error) bool { return err != nil }, func() error {
		var err error
		bcp, err = r.newBackup(ctx, cluster)
		if err != nil {
			return errors.Wrap(err, "create backup object")
		}
		return nil
	}); err != nil {
		return rr, err
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
		log.Error(err, "Cluster is not ready for backup")
		return status, nil
	}

	cjobs, err := backup.HasActiveJobs(ctx, r.newPBMFunc, r.client, cluster, backup.NewBackupJob(cr.Name), backup.NotPITRLock)
	if err != nil {
		return status, errors.Wrap(err, "check for concurrent jobs")
	}

	if cjobs {
		log.Info("Waiting to finish another backup/restore.")
		status.State = psmdbv1.BackupStateWaiting
		return status, nil
	}

	log.Info("Acquiring the backup lock")
	lease, err := k8s.AcquireLease(ctx, r.client, naming.BackupLeaseName(cluster.Name), cr.Namespace, naming.BackupHolderId(cr))
	if err != nil {
		return status, errors.Wrap(err, "acquire backup lock")
	}

	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != naming.BackupHolderId(cr) {
		log.Info("Another backup is holding the lock", "holder", *lease.Spec.HolderIdentity)
		status.State = psmdbv1.BackupStateWaiting
		return status, nil
	}

	if err := r.ensureReleaseLockFinalizer(ctx, cluster, cr); err != nil {
		return status, errors.Wrapf(err, "ensure %s finalizer", naming.FinalizerReleaseLock)
	}

	switch status.State {
	case psmdbv1.BackupStateNew, psmdbv1.BackupStateWaiting:
		return bcp.Start(ctx, r.client, cluster, cr)
	}

	err = retry.OnError(defaultBackoff, func(err error) bool { return err != nil }, func() error {
		updatedStatus, err := bcp.Status(ctx, cr)
		if err == nil {
			status = updatedStatus
		}
		return err
	})

	return status, err
}

func (r *ReconcilePerconaServerMongoDBBackup) ensureReleaseLockFinalizer(
	ctx context.Context,
	cluster *psmdbv1.PerconaServerMongoDB,
	cr *psmdbv1.PerconaServerMongoDBBackup,
) error {
	for _, f := range cr.GetFinalizers() {
		if f == naming.FinalizerReleaseLock {
			return nil
		}
	}

	orig := cr.DeepCopy()
	cr.SetFinalizers(append(cr.GetFinalizers(), naming.FinalizerReleaseLock))
	if err := r.client.Patch(ctx, cr.DeepCopy(), client.MergeFrom(orig)); err != nil {
		return errors.Wrap(err, "patch finalizers")
	}

	logf.FromContext(ctx).V(1).Info("Added finalizer", "finalizer", naming.FinalizerReleaseLock)

	return nil
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
		azureConf := &azure.Config{
			Account:     string(azureSecret.Data[backup.AzureStorageAccountNameSecretKey]),
			Container:   cr.Status.Azure.Container,
			EndpointURL: cr.Status.Azure.EndpointURL,
			Prefix:      cr.Status.Azure.Prefix,
			Credentials: azure.Credentials{
				Key: string(azureSecret.Data[backup.AzureStorageAccountKeySecretKey]),
			},
		}
		return azure.New(azureConf, "", nil)
	case cr.Status.S3 != nil:
		s3Conf := &s3.Config{
			Region:                cr.Status.S3.Region,
			EndpointURL:           cr.Status.S3.EndpointURL,
			Bucket:                cr.Status.S3.Bucket,
			Prefix:                cr.Status.S3.Prefix,
			UploadPartSize:        cr.Status.S3.UploadPartSize,
			MaxUploadParts:        cr.Status.S3.MaxUploadParts,
			StorageClass:          cr.Status.S3.StorageClass,
			InsecureSkipTLSVerify: cr.Status.S3.InsecureSkipTLSVerify,
		}

		if cr.Status.S3.CredentialsSecret != "" {
			s3secret, err := secret(ctx, r.client, cr.Namespace, cr.Status.S3.CredentialsSecret)
			if err != nil {
				return nil, errors.Wrap(err, "getting s3 credentials secret name")
			}
			s3Conf.Credentials = s3.Credentials{
				AccessKeyID:     string(s3secret.Data[backup.AWSAccessKeySecretKey]),
				SecretAccessKey: string(s3secret.Data[backup.AWSSecretAccessKeySecretKey]),
			}
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

		return s3.New(s3Conf, "", nil)
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

	for _, f := range cr.GetFinalizers() {
		switch f {
		case "delete-backup":
			log.Info("delete-backup finalizer is deprecated and will be deleted in 1.20.0. Use percona.com/delete-backup instead")
			fallthrough
		case naming.FinalizerDeleteBackup:
			if cr.Status.State == psmdbv1.BackupStateReady {
				if err := r.deleteBackupFinalizer(ctx, cr, cluster, b); err != nil {
					log.Error(err, "failed to run finalizer", "finalizer", f)
					finalizers = append(finalizers, f)
				}
			}
		case naming.FinalizerReleaseLock:
			err = r.runReleaseLockFinalizer(ctx, cr)
			if err != nil {
				log.Error(err, "failed to release backup lock")
				finalizers = append(finalizers, f)
			}
		}
	}

	cr.SetFinalizers(finalizers)
	err = r.client.Update(ctx, cr)

	return err
}

func (r *ReconcilePerconaServerMongoDBBackup) runReleaseLockFinalizer(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup) error {
	leaseName := naming.BackupLeaseName(cr.Spec.ClusterName)
	holderId := naming.BackupHolderId(cr)
	log := logf.FromContext(ctx).WithValues("lease", leaseName, "holder", holderId)

	log.Info("releasing backup lock")
	err := k8s.ReleaseLease(ctx, r.client, leaseName, cr.Namespace, holderId)
	if k8serrors.IsNotFound(err) || errors.Is(err, k8s.ErrNotTheHolder) {
		log.V(1).Info("failed to release backup lock", "error", err)
		return nil
	}
	return errors.Wrap(err, "release backup lock")
}

func (r *ReconcilePerconaServerMongoDBBackup) deleteBackupFinalizer(ctx context.Context, cr *psmdbv1.PerconaServerMongoDBBackup, cluster *psmdbv1.PerconaServerMongoDB, b *Backup) error {
	if len(cr.Status.PBMname) == 0 {
		return nil
	}

	log := logf.FromContext(ctx).WithName("deleteBackup").WithValues(
		"backup", cr.Name,
		"namespace", cr.Namespace,
		"pbmName", cr.Status.PBMname,
		"storage", cr.Status.StorageName,
	)

	if cr.Spec.Type == defs.IncrementalBackup {
		log.Info("Skipping " + naming.FinalizerDeleteBackup + " finalizer. It's not supported for incremental backups")
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
		if err := pbmBackup.DeleteBackupFiles(stg, getPBMBackupMeta(cr).Name); err != nil {
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
	case cr.Status.Filesystem != nil:
		err := r.deleteFilesystemBackup(ctx, cluster, cr)
		if err != nil {
			return errors.Wrap(err, "delete filesystem backup")
		}
		return nil
	}

	if cluster.CompareVersion("1.20.0") < 0 {
		err = b.pbm.DeletePITRChunks(ctx, meta.LastWriteTS)
		if err != nil {
			return errors.Wrap(err, "failed to delete PITR")
		}
		log.Info("PiTR chunks deleted", "until", meta.LastWriteTS)

		err = b.pbm.DeleteBackup(ctx, cr.Status.PBMname)
		if err != nil {
			return errors.Wrap(err, "failed to delete backup")
		}

		log.Info("Backup deleted")

		return nil
	}

	mainStgName, _, err := cluster.Spec.Backup.MainStorage()
	if err != nil {
		return errors.Wrap(err, "get main storage")
	}

	if mainStgName == cr.Status.StorageName {
		// We should delete PITR oplog chunks until `LastWriteTS` of the backup,
		// as it's not possible to delete backup if it is a base for the PITR timeline
		err = b.pbm.DeletePITRChunks(ctx, meta.LastWriteTS)
		if err != nil {
			return errors.Wrap(err, "failed to delete PITR")
		}
		log.Info("PiTR chunks deleted", "until", meta.LastWriteTS)
	}

	err = b.pbm.DeleteBackup(ctx, cr.Status.PBMname)
	if err != nil {
		return errors.Wrap(err, "failed to delete backup")
	}

	log.Info("Backup deleted")

	return nil
}

func (r *ReconcilePerconaServerMongoDBBackup) deleteFilesystemBackup(ctx context.Context, cluster *psmdbv1.PerconaServerMongoDB, bcp *psmdbv1.PerconaServerMongoDBBackup) error {
	log := logf.FromContext(ctx).WithName("deleteBackup").WithValues("backup", bcp.Name, "namespace", bcp.Namespace, "pbmName", bcp.Status.PBMname)

	rsName := bcp.Status.ReplsetNames[0]
	rsPods, err := psmdb.GetRSPods(ctx, r.client, cluster, rsName)
	if err != nil {
		return errors.Wrapf(err, "get %s pods", rsName)
	}

	pod := rsPods.Items[0]

	cmd := []string{
		"pbm",
		"delete-backup",
		bcp.Status.PBMname,
		"--yes",
	}

	log.V(1).Info("Deleting filesystem backup", "pod", pod.Name, "cmd", strings.Join(cmd, " "))

	outB := bytes.Buffer{}
	errB := bytes.Buffer{}
	err = r.clientcmd.Exec(ctx, &pod, naming.ContainerBackupAgent, cmd, nil, &outB, &errB, false)
	if err != nil {
		return errors.Wrapf(err, "exec delete-backup: stdout=%s, stderr=%s", outB.String(), errB.String())
	}

	log.Info("Backup deleted")

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
