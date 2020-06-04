package pbm

import (
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
)

// DeleteBackup deletes backup with the given name from the current storage
// and pbm database
func (p *PBM) DeleteBackup(name string) error {
	meta, err := p.GetBackupMeta(name)
	if err != nil {
		return errors.Wrap(err, "get backup meta")
	}
	err = probeDelete(meta)
	if err != nil {
		return err
	}

	stg, err := p.GetStorage()
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	err = p.DeleteBackupFiles(meta, stg)
	if err != nil {
		return errors.Wrap(err, "delete files from storage")
	}

	_, err = p.Conn.Database(DB).Collection(BcpCollection).DeleteOne(p.ctx, bson.M{"name": meta.Name})
	return errors.Wrap(err, "delete metadata from db")
}

func probeDelete(backup *BackupMeta) error {
	switch backup.Status {
	case StatusDone, StatusCancelled, StatusError:
		return nil
	default:
		return errors.Errorf("unable to delete backup in %s state", backup.Status)
	}
}

// DeleteBackupFiles removes backup's artefacts from storage
func (p *PBM) DeleteBackupFiles(meta *BackupMeta, stg storage.Storage) (err error) {
	for _, r := range meta.Replsets {
		err = stg.Delete(r.OplogName)
		if err != nil {
			return errors.Wrapf(err, "delete oplog %s", r.OplogName)
		}
		err = stg.Delete(r.DumpName)
		if err != nil {
			return errors.Wrapf(err, "delete dump %s", r.OplogName)
		}
	}

	err = stg.Delete(meta.Name + MetadataFileSuffix)
	return errors.Wrap(err, "delete metadata file from storage")
}

// DeleteOlderThan deletes backups which older than Time
func (p *PBM) DeleteOlderThan(t time.Time) error {
	stg, err := p.GetStorage()
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	cur, err := p.Conn.Database(DB).Collection(BcpCollection).Find(
		p.ctx,
		bson.M{
			"start_ts": bson.M{"$lt": t.Unix()},
		},
	)
	if err != nil {
		return errors.Wrap(err, "get backups list")
	}
	defer cur.Close(p.ctx)
	for cur.Next(p.ctx) {
		m := new(BackupMeta)
		err := cur.Decode(m)
		if err != nil {
			return errors.Wrap(err, "decode backup meta")
		}

		err = probeDelete(m)
		if err != nil {
			return err
		}

		err = p.DeleteBackupFiles(m, stg)
		if err != nil {
			return errors.Wrap(err, "delete backup files from storage")
		}

		_, err = p.Conn.Database(DB).Collection(BcpCollection).DeleteOne(p.ctx, bson.M{"name": m.Name})
		if err != nil {
			return errors.Wrap(err, "delete backup meta from db")
		}
	}

	return errors.Wrap(cur.Err(), "cursor")
}
