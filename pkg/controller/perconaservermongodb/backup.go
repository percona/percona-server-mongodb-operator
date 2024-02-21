package perconaservermongodb

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	api "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/pbm"
)

func (r *ReconcilePerconaServerMongoDB) isRestoreRunning(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	restores := api.PerconaServerMongoDBRestoreList{}
	if err := r.client.List(ctx, &restores, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}

		return false, errors.Wrap(err, "get restore list")
	}

	for _, rst := range restores.Items {
		if rst.Status.State != api.RestoreStateReady &&
			rst.Status.State != api.RestoreStateError &&
			rst.Spec.ClusterName == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) isBackupRunning(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	bcps := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(ctx, &bcps, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, bcp := range bcps.Items {
		if bcp.Status.State != api.BackupStateReady &&
			bcp.Status.State != api.BackupStateError &&
			bcp.Spec.GetClusterName() == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) hasFullBackup(ctx context.Context, cr *api.PerconaServerMongoDB) (bool, error) {
	backups := api.PerconaServerMongoDBBackupList{}
	if err := r.client.List(ctx, &backups, &client.ListOptions{Namespace: cr.Namespace}); err != nil {
		if k8sErrors.IsNotFound(err) {
			return false, nil
		}
		return false, errors.Wrap(err, "get backup list")
	}

	for _, b := range backups.Items {
		if b.Status.State == api.BackupStateReady && b.Spec.GetClusterName() == cr.Name {
			return true, nil
		}
	}

	return false, nil
}

func (r *ReconcilePerconaServerMongoDB) updatePITR(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	// log := logf.FromContext(ctx)

	// if !cr.Spec.Backup.Enabled {
	// 	return nil
	// }

	// _, resyncNeeded := cr.Annotations[api.AnnotationResyncPBM]
	// if resyncNeeded {
	// 	return nil
	// }

	// // pitr is disabled right before restore so it must not be re-enabled during restore
	// isRestoring, err := r.isRestoreRunning(ctx, cr)
	// if err != nil {
	// 	return errors.Wrap(err, "checking if restore running on pbm update")
	// }

	// if isRestoring || cr.Status.State != api.AppStateReady {
	// 	return nil
	// }

	// if cr.Spec.Backup.PITR.Enabled && !cr.Spec.Backup.PITR.OplogOnly {
	// 	hasFullBackup, err := r.hasFullBackup(ctx, cr)
	// 	if err != nil {
	// 		return errors.Wrap(err, "check full backup")
	// 	}

	// 	if !hasFullBackup {
	// 		log.Info("Point-in-time recovery will work only with full backup. Please create one manually or wait for scheduled backup to be created (if configured).")
	// 		return nil
	// 	}
	// }

	// val, err := pbm.GetConfigVar("pitr.enabled")
	// if err != nil {
	// 	if !errors.Is(err, mongo.ErrNoDocuments) {
	// 		return errors.Wrap(err, "get pitr.enabled")
	// 	}

	// 	if len(cr.Spec.Backup.Storages) == 1 {
	// 		// if PiTR is enabled user can configure only one storage
	// 		var storage api.BackupStorageSpec
	// 		for name, stg := range cr.Spec.Backup.Storages {
	// 			storage = stg
	// 			log.Info("Configuring PBM with storage", "storage", name)
	// 			break
	// 		}

	// 		var secretName string
	// 		switch storage.Type {
	// 		case pbm.StorageTypeS3:
	// 			secretName = storage.S3.CredentialsSecret
	// 		case pbm.StorageTypeAzure:
	// 			secretName = storage.Azure.CredentialsSecret
	// 		}

	// 		if secretName != "" {
	// 			exists, err := secretExists(ctx, r.client, types.NamespacedName{Name: secretName, Namespace: cr.Namespace})
	// 			if err != nil {
	// 				return errors.Wrap(err, "check storage credentials secret")
	// 			}

	// 			if !exists {
	// 				log.Error(nil, "Storage credentials secret does not exist", "secret", secretName)
	// 				return nil
	// 			}
	// 		}

	// 		err = pbm.SetConfig(ctx, r.client, cr, storage)
	// 		if err != nil {
	// 			return errors.Wrap(err, "set PBM config")
	// 		}

	// 		log.Info("Configured PBM storage")
	// 	}

	// 	return nil
	// }

	// enabled, ok := val.(bool)
	// if !ok {
	// 	return errors.Wrap(err, "unexpected value of pitr.enabled")
	// }

	// if enabled != cr.Spec.Backup.PITR.Enabled {
	// 	val := strconv.FormatBool(cr.Spec.Backup.PITR.Enabled)
	// 	log.Info("Setting pitr.enabled in PBM config", "enabled", val)
	// 	if err := pbm.SetConfigVar("pitr.enabled", val); err != nil {
	// 		return errors.Wrap(err, "update pitr.enabled")
	// 	}
	// }

	// if !cr.Spec.Backup.PITR.Enabled {
	// 	return nil
	// }

	// val, err = pbm.GetConfigVar("pitr.oplogOnly")
	// if err != nil {
	// 	if errors.Is(err, mongo.ErrNoDocuments) {
	// 		return nil
	// 	}

	// 	if errors.Is(err, bsoncore.ErrElementNotFound) {
	// 		val = false
	// 	} else {
	// 		return errors.Wrap(err, "get pitr.oplogOnly")
	// 	}
	// }

	// oplogOnly, ok := val.(bool)
	// if !ok {
	// 	return errors.Wrap(err, "unexpected value of pitr.oplogOnly")
	// }

	// if oplogOnly != cr.Spec.Backup.PITR.OplogOnly {
	// 	enabled := strconv.FormatBool(cr.Spec.Backup.PITR.OplogOnly)
	// 	log.Info("Setting pitr.oplogOnly in PBM config", "value", enabled)
	// 	if err := pbm.SetConfigVar("pitr.oplogOnly", enabled); err != nil {
	// 		return errors.Wrap(err, "update pitr.oplogOnly")
	// 	}
	// }

	// val, err = pbm.GetConfigVar("pitr.oplogSpanMin")
	// if err != nil {
	// 	if !errors.Is(err, mongo.ErrNoDocuments) {
	// 		return errors.Wrap(err, "get pitr.oplogSpanMin")
	// 	}

	// 	return nil
	// }

	// oplogSpanMin, ok := val.(float64)
	// if !ok {
	// 	return errors.Wrap(err, "unexpected value of pitr.oplogSpanMin")
	// }

	// if oplogSpanMin != cr.Spec.Backup.PITR.OplogSpanMin.Float64() {
	// 	val := cr.Spec.Backup.PITR.OplogSpanMin.String()
	// 	if err := pbm.SetConfigVar("pitr.oplogSpanMin", val); err != nil {
	// 		return errors.Wrap(err, "update pitr.oplogSpanMin")
	// 	}
	// }

	// val, err = pbm.GetConfigVar("pitr.compression")
	// var compression = ""
	// if err != nil {
	// 	if errors.Is(err, mongo.ErrNoDocuments) {
	// 		return nil
	// 	} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
	// 		return errors.Wrap(err, "get pitr.compression")
	// 	}
	// } else {
	// 	compression, ok = val.(string)
	// 	if !ok {
	// 		return errors.Wrap(err, "unexpected value of pitr.compression")
	// 	}
	// }

	// if compression != string(cr.Spec.Backup.PITR.CompressionType) {
	// 	if string(cr.Spec.Backup.PITR.CompressionType) == "" {
	// 		if err := pbm.DeleteConfigVar("pitr.compression"); err != nil {
	// 			return errors.Wrap(err, "delete pitr.compression")
	// 		}
	// 	} else if err := pbm.SetConfigVar("pitr.compression", string(cr.Spec.Backup.PITR.CompressionType)); err != nil {
	// 		return errors.Wrap(err, "update pitr.compression")
	// 	}

	// 	// PBM needs to disabling and enabling PITR to change compression type
	// 	if err := pbm.SetConfigVar("pitr.enabled", "false"); err != nil {
	// 		return errors.Wrap(err, "disable pitr")
	// 	}
	// 	if err := pbm.SetConfigVar("pitr.enabled", "true"); err != nil {
	// 		return errors.Wrap(err, "enable pitr")
	// 	}
	// }

	// val, err = pbm.GetConfigVar("pitr.compressionLevel")
	// var compressionLevel *int = nil
	// if err != nil {
	// 	if errors.Is(err, mongo.ErrNoDocuments) {
	// 		return nil
	// 	} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
	// 		return errors.Wrap(err, "get pitr.compressionLevel")
	// 	}
	// } else {
	// 	tmpCompressionLevel, ok := val.(int)
	// 	if !ok {
	// 		return errors.Wrap(err, "unexpected value of pitr.compressionLevel")
	// 	}
	// 	compressionLevel = &tmpCompressionLevel
	// }

	// if !reflect.DeepEqual(compressionLevel, cr.Spec.Backup.PITR.CompressionLevel) {
	// 	if cr.Spec.Backup.PITR.CompressionLevel == nil {
	// 		if err := pbm.DeleteConfigVar("pitr.compressionLevel"); err != nil {
	// 			return errors.Wrap(err, "delete pitr.compressionLevel")
	// 		}
	// 	} else if err := pbm.SetConfigVar("pitr.compressionLevel", strconv.FormatInt(int64(*cr.Spec.Backup.PITR.CompressionLevel), 10)); err != nil {
	// 		return errors.Wrap(err, "update pitr.compressionLevel")
	// 	}

	// 	// PBM needs to disabling and enabling PITR to change compression level
	// 	if err := pbm.SetConfigVar("pitr.enabled", "false"); err != nil {
	// 		return errors.Wrap(err, "disable pitr")
	// 	}
	// 	if err := pbm.SetConfigVar("pitr.enabled", "true"); err != nil {
	// 		return errors.Wrap(err, "enable pitr")
	// 	}
	// }

	return nil
}

func (r *ReconcilePerconaServerMongoDB) resyncPBMIfNeeded(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if cr.Status.State != api.AppStateReady || !cr.Spec.Backup.Enabled {
		return nil
	}

	_, resyncNeeded := cr.Annotations[api.AnnotationResyncPBM]
	if !resyncNeeded {
		return nil
	}

	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		c := &api.PerconaServerMongoDB{}
		err := r.client.Get(ctx, types.NamespacedName{Name: cr.Name, Namespace: cr.Namespace}, c)
		if err != nil {
			return err
		}

		orig := c.DeepCopy()
		delete(c.Annotations, api.AnnotationResyncPBM)

		return r.client.Patch(ctx, c, client.MergeFrom(orig))
	})
	if err != nil {
		return errors.Wrap(err, "delete annotation")
	}

	log.V(1).Info("Deleted annotation", "annotation", api.AnnotationResyncPBM)

	pod := &corev1.Pod{}
	podName := fmt.Sprintf("%s-%s-0", cr.Name, cr.Spec.Replsets[0].Name)
	err = r.client.Get(ctx, types.NamespacedName{Name: podName, Namespace: cr.Namespace}, pod)
	if err != nil {
		return errors.Wrapf(err, "get pod/%s", podName)
	}

	log.Info("Starting PBM resync", "pod", pod.Name)
	if err := pbm.ForceResync(ctx, r.clientcmd, pod); err != nil {
		return errors.Wrap(err, "force PBM resync")

	}

	return nil
}

func secretExists(ctx context.Context, cl client.Client, nn types.NamespacedName) (bool, error) {
	var secret corev1.Secret
	err := cl.Get(ctx, nn, &secret)
	if err != nil {
		if k8serrors.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	return true, nil
}

func (r *ReconcilePerconaServerMongoDB) reconcilePBMConfiguration(ctx context.Context, cr *api.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if !cr.Spec.Backup.Enabled {
		return nil
	}

	if cr.Spec.Backup.Storages == nil {
		log.Info("PBM is not configured", "reason", "backup storages are not configured")
		return nil
	}

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-" + cr.Spec.Replsets[0].Name + "-0",
			Namespace: cr.Namespace,
		},
	}
	err := r.client.Get(ctx, client.ObjectKeyFromObject(pod), pod)
	if err != nil {
		return errors.Wrapf(err, "get pod %s", client.ObjectKeyFromObject(pod))
	}

	hasRunning, err := pbm.HasRunningOperation(ctx, r.clientcmd, pod)
	if err != nil {
		return errors.Wrap(err, "check if PBM has running operation")
	}

	if hasRunning {
		log.V(1).Info("PBM has running operation")
		return nil
	}

	log.Info("Reconciling PBM configuration", "storages", cr.Spec.Backup.Storages)

	var firstStorage api.BackupStorageSpec
	for _, stg := range cr.Spec.Backup.Storages {
		firstStorage = stg
		break
	}

	log.Info("Configuring PBM with storage", "storage", firstStorage)

	if err := pbm.CreateOrUpdateConfig(ctx, r.clientcmd, r.client, cr, firstStorage); err != nil {
		return errors.Wrap(err, "create or update PBM configuration")
	}

	// Initialize PBM configuration
	if cond := meta.FindStatusCondition(cr.Status.Conditions, "PBMReady"); cond != nil && cond.Reason == "PBMIsNotConfigured" && cond.Status != metav1.ConditionTrue {
		if !pbm.FileExists(ctx, r.clientcmd, pod, pbm.ConfigFilePath) {
			log.Info("Waiting for PBM configuration to be propagated to the pod")
			return nil
		}

		if err := pbm.SetConfigFile(ctx, r.clientcmd, pod, pbm.ConfigFilePath); err != nil {
			return errors.Wrap(err, "set PBM config file")
		}

		return nil
	}

	secret := corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cr.Name + "-pbm-config",
			Namespace: cr.Namespace,
		},
	}
	err = r.client.Get(ctx, client.ObjectKeyFromObject(&secret), &secret)
	if err != nil {
		return errors.Wrapf(err, "get secret %s", client.ObjectKeyFromObject(&secret))
	}

	_, ok := secret.Annotations["percona.com/config-applied"]
	if ok {
		return nil
	}

	checksum, ok := secret.Annotations["percona.com/config-sum"]
	if !ok {
		return errors.New("missing checksum annotation")
	}

	log.Info("PBM configuration checksum", "checksum", checksum)

	// is secret propagated to the pod
	if !pbm.CheckSHA256Sum(ctx, r.clientcmd, pod, checksum, pbm.ConfigFilePath) {
		return nil
	}

	log.Info("PBM configuration is propagated to the pod")

	if err := pbm.SetConfigFile(ctx, r.clientcmd, pod, pbm.ConfigFilePath); err != nil {
		return errors.Wrap(err, "set PBM config file")
	}

	log.Info("PBM configuration is applied to the DB")

	secret.Annotations["percona.com/config-applied"] = "true"

	return r.client.Update(ctx, &secret)
}
