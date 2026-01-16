package perconaservermongodb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	pbmVersion "github.com/percona/percona-backup-mongodb/pbm/version"

	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/naming"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

func (r *ReconcilePerconaServerMongoDB) reconcilePBM(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx).WithName("PBM")
	ctx = logf.IntoContext(ctx, log)

	if err := r.reconcileBackupVersion(ctx, cr); err != nil {
		return errors.Wrap(err, "reconcile backup version")
	}

	if cr.CompareVersion("1.20.0") >= 0 {
		if err := r.reconcilePBMConfig(ctx, cr); err != nil {
			return errors.Wrap(err, "reconcile configuration")
		}
	}

	if err := r.reconcilePiTRConfig(ctx, cr); err != nil {
		return errors.Wrap(err, "update PiTR config")
	}

	if err := r.resyncPBMIfNeeded(ctx, cr); err != nil {
		return errors.Wrap(err, "resync PBM if needed")
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcilePBMConfig(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if !cr.Spec.Backup.Enabled ||
		cr.Status.BackupVersion == "" ||
		cr.Spec.Pause {
		return nil
	}

	// Restore will resync the storage. We shouldn't change config during the restore
	isRestoring, err := r.isRestoreRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "checking if restore running on pbm update")
	}

	if isRestoring {
		return nil
	}

	mainStgName, mainStg, err := cr.Spec.Backup.MainStorage()
	if err != nil {
		// no storage found
		return nil
	}

	for rs, status := range cr.Status.Replsets {
		if !status.Initialized {
			log.V(1).Info("waiting for replset to be initialized", "replset", rs)
			return nil
		}

		if rs == psmdbv1.ConfigReplSetName {
			continue
		}

		if cr.Spec.Sharding.Enabled && (status.AddedAsShard == nil || !*status.AddedAsShard) {
			log.V(1).Info("waiting for replset to be added as shard", "replset", rs)
			return nil
		}
	}

	main, err := backup.GetPBMConfig(ctx, r.client, cr, mainStg)
	if err != nil {
		return errors.Wrap(err, "get config")
	}

	// PITR config will be handled by reconcilePiTRConfig function
	main.PITR = nil

	var external []config.Config
	for name, stg := range cr.Spec.Backup.Storages {
		if name == mainStgName {
			continue
		}

		profile, err := backup.GetPBMProfile(ctx, r.client, cr, name, stg)
		if err != nil {
			return errors.Wrap(err, "get profile")
		}

		external = append(external, profile)
	}

	c := append([]config.Config{main}, external...)
	slices.SortFunc(c, func(a config.Config, b config.Config) int {
		if a.IsProfile && !b.IsProfile {
			return 1
		}

		if !a.IsProfile && b.IsProfile {
			return -1
		}

		return strings.Compare(a.Name, b.Name)
	})

	hash, err := hashPBMConfiguration(c)
	if err != nil {
		return errors.Wrap(err, "hash config")
	}

	pbm, err := backup.NewPBM(ctx, r.client, cr)
	if err != nil {
		return errors.Wrap(err, "new PBM connection")
	}
	defer func() {
		if err := pbm.Close(ctx); err != nil {
			log.Error(err, "failed to close PBM connection")
		}
	}()

	currentCfg, err := pbm.GetConfig(ctx)
	if err != nil && !backup.IsErrNoDocuments(err) {
		return errors.Wrap(err, "get current config")
	}
	if currentCfg == nil {
		currentCfg = new(config.Config)
	}

	// Hashes can be equal even if the actual PBM configuration differs from the one that was hashed
	// For example, a restore can modify the PBM config
	// We should use `isResyncNeeded` to compare the current configuration with the one we need
	// Also, storage credentials are not hashed since they are excluded from JSON representation. `isResyncNeeded` will handle it.
	if cr.Status.BackupConfigHash == hash && !isResyncNeeded(currentCfg, &main) {
		return nil
	}

	err = r.updateCondition(ctx, cr, psmdbv1.ClusterCondition{
		Type:   psmdbv1.ConditionTypePBMReady,
		Reason: "PBMConfigurationIsChanged",
		Status: psmdbv1.ConditionFalse,
	})
	if err != nil {
		return errors.Wrapf(err, "update %s condition", psmdbv1.ConditionTypePBMReady)
	}

	log.Info("configuration changed or resync is needed", "oldHash", cr.Status.BackupConfigHash, "newHash", hash)

	if err := pbm.GetNSetConfig(ctx, r.client, cr); err != nil {
		return errors.Wrap(err, "set config")
	}

	if isResyncNeeded(currentCfg, &main) {
		log.Info("main storage changed. starting resync", "old", currentCfg.Storage, "new", main.Storage)

		if err := pbm.ResyncMainStorageAndWait(ctx); err != nil {
			return errors.Wrap(err, "resync")
		}
	}

	cr.Status.BackupConfigHash = hash

	err = r.updateCondition(ctx, cr, psmdbv1.ClusterCondition{
		Type:   psmdbv1.ConditionTypePBMReady,
		Reason: "PBMConfigurationIsUpToDate",
		Status: psmdbv1.ConditionTrue,
	})
	if err != nil {
		return errors.Wrapf(err, "update %s condition", psmdbv1.ConditionTypePBMReady)
	}

	return nil
}

func hashPBMConfiguration(c []config.Config) (string, error) {
	v, err := json.Marshal(c)
	if err != nil {
		return "", err
	}

	return sha256Hash(v), nil
}

func isResyncNeeded(currentCfg *config.Config, newCfg *config.Config) bool {
	if currentCfg.Storage.Type != newCfg.Storage.Type {
		return true
	}

	if currentCfg.Storage.S3 != nil && newCfg.Storage.S3 != nil {
		if currentCfg.Storage.S3.Bucket != newCfg.Storage.S3.Bucket {
			return true
		}

		if currentCfg.Storage.S3.Region != newCfg.Storage.S3.Region {
			return true
		}

		if currentCfg.Storage.S3.EndpointURL != newCfg.Storage.S3.EndpointURL {
			return true
		}

		if currentCfg.Storage.S3.Prefix != newCfg.Storage.S3.Prefix {
			return true
		}

		if currentCfg.Storage.S3.Credentials.AccessKeyID != newCfg.Storage.S3.Credentials.AccessKeyID {
			return true
		}

		if currentCfg.Storage.S3.Credentials.SecretAccessKey != newCfg.Storage.S3.Credentials.SecretAccessKey {
			return true
		}

		if !reflect.DeepEqual(currentCfg.Storage.S3.ServerSideEncryption, newCfg.Storage.S3.ServerSideEncryption) {
			return true
		}

		if ptr.Deref(currentCfg.Storage.S3.ForcePathStyle, true) != ptr.Deref(newCfg.Storage.S3.ForcePathStyle, true) {
			return true
		}
	}
	if currentCfg.Storage.Minio != nil && newCfg.Storage.Minio != nil {
		if currentCfg.Storage.Minio.Bucket != newCfg.Storage.Minio.Bucket {
			return true
		}
		if currentCfg.Storage.Minio.Region != newCfg.Storage.Minio.Region {
			return true
		}
		if currentCfg.Storage.Minio.Endpoint != newCfg.Storage.Minio.Endpoint {
			return true
		}
		if currentCfg.Storage.Minio.Prefix != newCfg.Storage.Minio.Prefix {
			return true
		}
		if currentCfg.Storage.Minio.Credentials.AccessKeyID != newCfg.Storage.Minio.Credentials.AccessKeyID {
			return true
		}
		if currentCfg.Storage.Minio.Credentials.SecretAccessKey != newCfg.Storage.Minio.Credentials.SecretAccessKey {
			return true
		}
		if currentCfg.Storage.Minio.Secure != newCfg.Storage.Minio.Secure {
			return true
		}
		if currentCfg.Storage.Minio.InsecureSkipTLSVerify != newCfg.Storage.Minio.InsecureSkipTLSVerify {
			return true
		}
		if currentCfg.Storage.Minio.PartSize != newCfg.Storage.Minio.PartSize {
			return true
		}
		if !reflect.DeepEqual(currentCfg.Storage.Minio.Retryer, newCfg.Storage.Minio.Retryer) {
			return true
		}
		if !ptr.Equal(currentCfg.Storage.Minio.ForcePathStyle, newCfg.Storage.Minio.ForcePathStyle) {
			return true
		}
	}

	if currentCfg.Storage.GCS != nil && newCfg.Storage.GCS != nil {
		if currentCfg.Storage.GCS.Bucket != newCfg.Storage.GCS.Bucket {
			return true
		}

		if currentCfg.Storage.GCS.Prefix != newCfg.Storage.GCS.Prefix {
			return true
		}

		if currentCfg.Storage.GCS.Credentials.ClientEmail != newCfg.Storage.GCS.Credentials.ClientEmail {
			return true
		}

		if currentCfg.Storage.GCS.Credentials.PrivateKey != newCfg.Storage.GCS.Credentials.PrivateKey {
			return true
		}

		if currentCfg.Storage.GCS.Credentials.HMACAccessKey != newCfg.Storage.GCS.Credentials.HMACAccessKey {
			return true
		}

		if currentCfg.Storage.GCS.Credentials.HMACSecret != newCfg.Storage.GCS.Credentials.HMACSecret {
			return true
		}
	}

	if currentCfg.Storage.Azure != nil && newCfg.Storage.Azure != nil {
		if currentCfg.Storage.Azure.EndpointURL != newCfg.Storage.Azure.EndpointURL {
			return true
		}

		if currentCfg.Storage.Azure.Container != newCfg.Storage.Azure.Container {
			return true
		}

		if currentCfg.Storage.Azure.Account != newCfg.Storage.Azure.Account {
			return true
		}

		if currentCfg.Storage.Azure.Credentials.Key != newCfg.Storage.Azure.Credentials.Key {
			return true
		}
	}

	if currentCfg.Storage.Filesystem != nil && newCfg.Storage.Filesystem != nil {
		if currentCfg.Storage.Filesystem.Path != newCfg.Storage.Filesystem.Path {
			return true
		}
	}

	return false
}

func (r *ReconcilePerconaServerMongoDB) reconcilePiTRStorageLegacy(
	ctx context.Context,
	pbm backup.PBM,
	cr *psmdbv1.PerconaServerMongoDB,
) error {
	log := logf.FromContext(ctx)

	if len(cr.Spec.Backup.Storages) != 1 {
		log.Info("Expected exactly one storage for PiTR in legacy version", "configured", len(cr.Spec.Backup.Storages))
		return nil
	}

	// if PiTR is enabled user can configure only one storage
	var storage psmdbv1.BackupStorageSpec
	for name, stg := range cr.Spec.Backup.Storages {
		storage = stg
		log.Info("Configuring PBM with storage", "storage", name)
		break
	}

	var secretName string
	switch storage.Type {
	case psmdbv1.BackupStorageS3:
		secretName = storage.S3.CredentialsSecret
	case psmdbv1.BackupStorageAzure:
		secretName = storage.Azure.CredentialsSecret
	}

	if secretName != "" {
		exists, err := secretExists(ctx, r.client, types.NamespacedName{Name: secretName, Namespace: cr.Namespace})
		if err != nil {
			return errors.Wrap(err, "check storage credentials secret")
		}

		if !exists {
			log.Error(nil, "Storage credentials secret does not exist", "secret", secretName)
			return nil
		}
	}

	err := pbm.GetNSetConfigLegacy(ctx, r.client, cr, storage)
	if err != nil {
		return errors.Wrap(err, "set PBM config")
	}

	log.Info("Configured PBM storage")

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcilePiTRConfig(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if !cr.Spec.Backup.Enabled {
		return nil
	}

	if cr.PBMResyncNeeded() || cr.PBMResyncInProgress() {
		return nil
	}

	// pitr is disabled right before restore so it must not be re-enabled during restore
	isRestoring, err := r.isRestoreRunning(ctx, cr)
	if err != nil {
		return errors.Wrap(err, "checking if restore running on pbm update")
	}

	if isRestoring || cr.Status.State != psmdbv1.AppStateReady {
		return nil
	}

	var stgName string
	for name := range cr.Spec.Backup.Storages {
		stgName = name
		break
	}
	if cr.CompareVersion("1.20.0") >= 0 {
		stgName, _, err = cr.Spec.Backup.MainStorage()
		if err != nil {
			// no storage found
			return nil
		}
	}

	if cr.Spec.Backup.PITR.Enabled && !cr.Spec.Backup.PITR.OplogOnly {
		hasFullBackup, err := r.hasFullBackup(ctx, cr, stgName)
		if err != nil {
			return errors.Wrap(err, "check full backup")
		}

		if !hasFullBackup {
			log.Info(
				fmt.Sprintf("Point-in-time recovery will work only with full backup in main storage (%s).", stgName) +
					" Please create one manually or wait for scheduled backup to be created (if configured).")
			return nil
		}
	}

	pbm, err := backup.NewPBM(ctx, r.client, cr)
	if err != nil {
		return errors.Wrap(err, "create pbm object")
	}
	defer func() {
		if err := pbm.Close(ctx); err != nil {
			log.Error(err, "failed to close PBM connection")
		}
	}()

	if err := enablePiTRIfNeeded(ctx, pbm, cr); err != nil {
		if backup.IsErrNoDocuments(err) {
			if cr.CompareVersion("1.20.0") < 0 {
				if err := r.reconcilePiTRStorageLegacy(ctx, pbm, cr); err != nil {
					return errors.Wrap(err, "reconcile pitr storage")
				}
			}
			return nil
		}

		return errors.Wrap(err, "enable pitr if needed")
	}

	if !cr.Spec.Backup.PITR.Enabled {
		return nil
	}

	if err := updateOplogOnlyIfNeed(ctx, pbm, cr); err != nil {
		return errors.Wrap(err, "update oplog only if needed")
	}

	if err := updateOplogSpanMinIfNeeded(ctx, pbm, cr); err != nil {
		return errors.Wrap(err, "update oplog span min if needed")
	}

	if err := updateCompressionIfNeeded(ctx, pbm, cr); err != nil {
		return errors.Wrap(err, "update compression if needed")
	}

	if err := updateLatestRestorableTime(ctx, r.client, pbm, cr); err != nil {
		return errors.Wrap(err, "update latest restorable time")
	}

	return nil
}

func enablePiTRIfNeeded(ctx context.Context, pbm backup.PBM, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	val, err := pbm.GetConfigVar(ctx, "pitr.enabled")
	if err != nil {
		return errors.Wrap(err, "get pitr.enabled")
	}

	enabled, ok := val.(bool)
	if !ok {
		return errors.Errorf("unexpected value of pitr.enabled: %T", val)
	}

	if enabled == cr.Spec.Backup.PITR.Enabled {
		return nil
	}

	valBool := strconv.FormatBool(cr.Spec.Backup.PITR.Enabled)

	log.Info("Setting pitr.enabled", "enabled", valBool)

	if err := pbm.SetConfigVar(ctx, "pitr.enabled", valBool); err != nil {
		return errors.Wrap(err, "update pitr.enabled")
	}

	return nil
}

func updateOplogOnlyIfNeed(ctx context.Context, pbm backup.PBM, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	val, err := pbm.GetConfigVar(ctx, "pitr.oplogOnly")
	if err != nil {
		if errors.Is(err, bsoncore.ErrElementNotFound) {
			val = false
		} else {
			return errors.Wrap(err, "get pitr.oplogOnly")
		}
	}

	oplogOnly, ok := val.(bool)
	if !ok {
		return errors.Errorf("unexpected value of pitr.oplogOnly: %T", val)
	}

	if oplogOnly == cr.Spec.Backup.PITR.OplogOnly {
		return nil
	}

	enabled := strconv.FormatBool(cr.Spec.Backup.PITR.OplogOnly)

	log.Info("Setting pitr.oplogOnly", "value", enabled)

	if err := pbm.SetConfigVar(ctx, "pitr.oplogOnly", enabled); err != nil {
		return errors.Wrap(err, "update pitr.oplogOnly")
	}

	return nil
}

func updateOplogSpanMinIfNeeded(ctx context.Context, pbm backup.PBM, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	val, err := pbm.GetConfigVar(ctx, "pitr.oplogSpanMin")
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		}
		// PBM-1387
		// PBM has a bug to return config fields if they use default value
		if errors.Is(err, bsoncore.ErrElementNotFound) {
			val = 0.0
		} else {
			return errors.Wrap(err, "get pitr.oplogSpanMin")
		}
	}

	oplogSpanMin, ok := val.(float64)
	if !ok {
		return errors.Errorf("unexpected value of pitr.oplogSpanMin: %T", val)
	}

	if oplogSpanMin == cr.Spec.Backup.PITR.OplogSpanMin.Float64() {
		return nil
	}

	valStr := cr.Spec.Backup.PITR.OplogSpanMin.String()

	log.Info("Setting pitr.oplogSpanMin", "value", valStr)

	if err := pbm.SetConfigVar(ctx, "pitr.oplogSpanMin", valStr); err != nil {
		return errors.Wrap(err, "update pitr.oplogSpanMin")
	}

	return nil
}

func updateCompressionIfNeeded(ctx context.Context, pbm backup.PBM, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	val, err := pbm.GetConfigVar(ctx, "pitr.compression")
	compression := ""
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
			return errors.Wrap(err, "get pitr.compression")
		}
	} else {
		var ok bool
		compression, ok = val.(string)
		if !ok {
			return errors.Errorf("unexpected value of pitr.compression: %T", val)
		}
	}

	if compression != string(cr.Spec.Backup.PITR.CompressionType) {
		if string(cr.Spec.Backup.PITR.CompressionType) == "" {
			cfg, err := pbm.GetConfig(ctx)
			if err != nil {
				return errors.Wrap(err, "get pbm config")
			}

			cfg.PITR.Compression = ""
			log.Info("Delete pitr.compression")
			if err := pbm.SetConfig(ctx, cfg); err != nil {
				return errors.Wrap(err, "delete pitr.compression")
			}
		} else {
			val := string(cr.Spec.Backup.PITR.CompressionType)
			err := pbm.SetConfigVar(ctx, "pitr.compression", val)
			log.Info("Setting pitr.compression", "value", val)
			if err != nil {
				return errors.Wrap(err, "update pitr.compression")
			}
		}

		// PBM needs to disabling and enabling PITR to change compression type
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "false"); err != nil {
			return errors.Wrap(err, "disable pitr")
		}
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "true"); err != nil {
			return errors.Wrap(err, "enable pitr")
		}
	}

	val, err = pbm.GetConfigVar(ctx, "pitr.compressionLevel")
	var compressionLevel *int = nil
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil
		} else if !errors.Is(err, bsoncore.ErrElementNotFound) {
			return errors.Wrap(err, "get pitr.compressionLevel")
		}
	} else {
		var iVal int
		switch v := val.(type) {
		case int64:
			iVal = int(v)
		case int32:
			iVal = int(v)
		default:
			return errors.Errorf("unexpected value of pitr.compressionLevel: %T", val)
		}
		compressionLevel = &iVal
	}

	if !reflect.DeepEqual(compressionLevel, cr.Spec.Backup.PITR.CompressionLevel) {
		if cr.Spec.Backup.PITR.CompressionLevel == nil {
			cfg, err := pbm.GetConfig(ctx)
			if err != nil {
				return errors.Wrap(err, "get pbm config")
			}

			cfg.PITR.CompressionLevel = nil
			if err := pbm.SetConfig(ctx, cfg); err != nil {
				return errors.Wrap(err, "delete pitr.compressionLevel")
			}
		} else {
			val := strconv.FormatInt(int64(*cr.Spec.Backup.PITR.CompressionLevel), 10)
			log.Info("Setting pitr.compressionLevel", "value", val)
			err := pbm.SetConfigVar(ctx, "pitr.compressionLevel", val)
			if err != nil {
				return errors.Wrap(err, "update pitr.compressionLevel")
			}
		}

		// PBM needs to disabling and enabling PITR to change compression level
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "false"); err != nil {
			return errors.Wrap(err, "disable pitr")
		}
		if err := pbm.SetConfigVar(ctx, "pitr.enabled", "true"); err != nil {
			return errors.Wrap(err, "enable pitr")
		}
	}

	return nil
}

func (r *ReconcilePerconaServerMongoDB) resyncPBMIfNeeded(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if cr.Status.State != psmdbv1.AppStateReady || !cr.Spec.Backup.Enabled || !cr.PBMResyncNeeded() {
		return nil
	}

	l := r.lockers.LoadOrCreate(cr.NamespacedName().String())
	if !l.resyncMutex.TryLock() {
		return nil
	}

	log.Info("resync is needed for all storages")

	err := k8s.AnnotateObject(ctx, r.client, cr, map[string]string{psmdbv1.AnnotationResyncInProgress: "true"})
	if err != nil {
		return errors.Wrapf(err, "annotate %s", psmdbv1.AnnotationResyncInProgress)
	}

	if err := k8s.DeannotateObject(ctx, r.client, cr, psmdbv1.AnnotationResyncPBM); err != nil {
		return errors.Wrapf(err, "delete annotation %s", psmdbv1.AnnotationResyncPBM)
	}

	// running in separate goroutine to not block reconciliation
	// until all resync operations finished
	go func() {
		defer l.resyncMutex.Unlock()

		pbm, err := backup.NewPBM(ctx, r.client, cr)
		if err != nil {
			log.Error(err, "failed to open PBM connection")
			return
		}
		defer func() {
			if err := pbm.Close(ctx); err != nil {
				log.Error(err, "failed to close PBM connection")
			}
		}()

		log.Info("starting resync for main storage")

		if err := pbm.ResyncMainStorageAndWait(ctx); err != nil {
			log.Error(err, "failed to resync main storage")
			return
		}

		if len(cr.Spec.Backup.Storages) > 1 {
			log.Info("starting resync for all profiles")

			if err := pbm.ResyncProfileAndWait(ctx, "all"); err != nil {
				log.Error(err, "failed to resync profiles")
				return
			}
		}

		err = k8s.DeannotateObject(ctx, r.client, cr, psmdbv1.AnnotationResyncInProgress)
		if err != nil {
			log.Error(err, "failed to delete annotation")
			return
		}
	}()

	return nil
}

func (r *ReconcilePerconaServerMongoDB) reconcileBackupVersion(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx)

	if !cr.Spec.Backup.Enabled {
		return nil
	}

	if cr.Status.BackupVersion != "" && cr.Status.BackupImage == cr.Spec.Backup.Image {
		return nil
	}

	if cr.Status.Ready < 1 {
		return nil
	}

	if len(cr.Spec.Replsets) < 1 {
		return errors.New("no replsets found")
	}

	var rs *psmdbv1.ReplsetSpec
	for _, r := range cr.Spec.Replsets {
		rs = r
		break
	}

	stsName := naming.MongodStatefulSetName(cr, rs)
	sts := psmdb.NewStatefulSet(stsName, cr.Namespace)
	err := r.client.Get(ctx, client.ObjectKeyFromObject(sts), sts)
	if err != nil {
		return errors.Wrapf(err, "get statefulset/%s", stsName)
	}

	matchLabels := naming.RSLabels(cr, rs)
	label, ok := sts.Labels[naming.LabelKubernetesComponent]
	if ok {
		matchLabels[naming.LabelKubernetesComponent] = label
	}

	podList := corev1.PodList{}
	if err := r.client.List(ctx,
		&podList,
		&client.ListOptions{
			Namespace:     cr.Namespace,
			LabelSelector: labels.SelectorFromSet(matchLabels),
		},
	); err != nil {
		return errors.Wrap(err, "get pod list")
	}

	var pod *corev1.Pod
	for _, p := range podList.Items {
		if !k8s.IsPodReady(p) {
			continue
		}

		if !isContainerAndPodRunning(p, naming.ContainerBackupAgent) {
			continue
		}

		if !isPodUpToDate(&p, sts.Status.UpdateRevision, cr.Spec.Backup.Image) {
			continue
		}

		pod = &p
		break
	}
	if pod == nil {
		log.V(1).Error(nil, "no ready pods to get pbm-agent version")
		return nil
	}

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := []string{"pbm-agent", "version", "--short"}

	err = r.clientcmd.Exec(ctx, pod, naming.ContainerBackupAgent, cmd, nil, stdout, stderr, false)
	if err != nil {
		return errors.Wrap(err, "get pbm-agent version")
	}

	// PBM v2.9.0 and above prints version to stderr, below prints it to stdout
	stdoutStr := strings.TrimSpace(stdout.String())
	stderrStr := strings.TrimSpace(stderr.String())
	if stdoutStr != "" && stderrStr != "" {
		log.V(1).Info("pbm-agent version found in both stdout and stderr; using stdout",
			"stdout", stdoutStr, "stderr", stderrStr)
		cr.Status.BackupVersion = stdoutStr
	} else if stdoutStr != "" {
		cr.Status.BackupVersion = stdoutStr
	} else if stderrStr != "" {
		cr.Status.BackupVersion = stderrStr
	} else {
		return errors.New("pbm-agent version not found in stdout or stderr")
	}

	cr.Status.BackupImage = cr.Spec.Backup.Image

	log.Info("pbm-agent version",
		"pod", pod.Name,
		"image", cr.Status.BackupImage,
		"version", cr.Status.BackupVersion)

	pbmInfo := pbmVersion.Current()

	compare, err := cr.ComparePBMAgentVersion(pbmInfo.Version)
	if err != nil {
		return errors.Wrap(err, "compare pbm-agent version with go module")
	}

	if compare != 0 {
		log.Info("pbm-agent version is different than the go module, this might create problems",
			"pbmAgentVersion", cr.Status.BackupVersion,
			"goModuleVersion", pbmInfo.Version)
	}

	return nil
}
