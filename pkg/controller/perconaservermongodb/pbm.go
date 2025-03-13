package perconaservermongodb

import (
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
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/percona/percona-backup-mongodb/pbm/config"
	psmdbv1 "github.com/percona/percona-server-mongodb-operator/pkg/apis/psmdb/v1"
	"github.com/percona/percona-server-mongodb-operator/pkg/k8s"
	"github.com/percona/percona-server-mongodb-operator/pkg/psmdb/backup"
)

func (r *ReconcilePerconaServerMongoDB) reconcilePBM(ctx context.Context, cr *psmdbv1.PerconaServerMongoDB) error {
	log := logf.FromContext(ctx).WithName("PBM")
	ctx = logf.IntoContext(ctx, log)

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

	if cr.Status.State != psmdbv1.AppStateReady {
		return nil
	}

	mainStgName, mainStg, err := cr.Spec.Backup.MainStorage()
	if err != nil {
		// no storage found
		return nil
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

	if cr.Status.BackupConfigHash == hash {
		return nil
	}

	log.Info("configuration changed", "oldHash", cr.Status.BackupConfigHash, "newHash", hash)

	pbm, err := backup.NewPBM(ctx, r.client, cr)
	if err != nil {
		return errors.Wrap(err, "new PBM connection")
	}

	currentCfg, err := pbm.GetConfig(ctx)
	if err != nil && !backup.IsErrNoDocuments(err) {
		return errors.Wrap(err, "get current config")
	}

	if currentCfg == nil {
		currentCfg = new(config.Config)
	}

	if err := pbm.GetNSetConfig(ctx, r.client, cr); err != nil {
		return errors.Wrap(err, "set config")
	}

	if !reflect.DeepEqual(currentCfg.Storage, main.Storage) {
		log.Info("resync storage", "storage", mainStgName)

		if err := pbm.ResyncMainStorage(ctx); err != nil {
			return errors.Wrap(err, "resync")
		}
	}

	cr.Status.BackupConfigHash = hash

	return nil
}

func hashPBMConfiguration(c []config.Config) (string, error) {
	v, err := json.Marshal(c)
	if err != nil {
		return "", err
	}

	return sha256Hash(v), nil
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

	stgName, _, err := cr.Spec.Backup.MainStorage()
	if err != nil {
		// no storage found
		return nil
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
	defer pbm.Close(ctx)

	if err := enablePiTRIfNeeded(ctx, pbm, cr); err != nil {
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
