package pbm

import (
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

// DeleteBackup deletes backup with the given name from the current storage
// and pbm database
func (p *PBM) DeleteBackup(name string, l *log.Event) error {
	meta, err := p.GetBackupMeta(name)
	if err != nil {
		return errors.Wrap(err, "get backup meta")
	}
	err = p.probeDelete(meta)
	if err != nil {
		return err
	}

	stg, err := p.GetStorage(l)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	err = p.DeleteBackupFiles(meta, stg)
	if err != nil {
		return errors.Wrap(err, "delete files from storage")
	}

	_, err = p.Conn.Database(DB).Collection(BcpCollection).DeleteOne(p.ctx, bson.M{"name": meta.Name})
	if err != nil {
		return errors.Wrap(err, "delete metadata from db")
	}

	// need to delete invalidated PITR chunks
	nxt, err := p.BackupGetNext(meta)
	if err != nil {
		return errors.Wrap(err, "get next backup")
	}

	upto := primitive.Timestamp{}
	if nxt == nil {
		upto.T = uint32(time.Now().Unix())
	} else {
		upto.T = uint32(nxt.StartTS)
	}

	return p.deleteChunks(meta.LastWriteTS, upto, stg)
}

func (p *PBM) probeDelete(backup *BackupMeta) error {
	// check if backup isn't running
	switch backup.Status {
	case StatusDone, StatusCancelled, StatusError:
	default:
		return errors.Errorf("unable to delete backup in %s state", backup.Status)
	}

	// we shouldn't delete the last backup if PITR is ON
	ispitr, err := p.IsPITR()
	if err != nil {
		return errors.Wrap(err, "unable check pitr state")
	}
	if !ispitr {
		return nil
	}

	nxt, err := p.BackupGetNext(backup)
	if err != nil {
		return errors.Wrap(err, "check next backup")
	}

	if nxt == nil {
		return errors.New("unable to delete the last backup while PITR is on")
	}

	return nil
}

// DeleteBackupFiles removes backup's artefacts from storage
func (p *PBM) DeleteBackupFiles(meta *BackupMeta, stg storage.Storage) (err error) {
	for _, r := range meta.Replsets {
		err = stg.Delete(r.OplogName)
		if err != nil && err != storage.ErrNotExist {
			return errors.Wrapf(err, "delete oplog %s", r.OplogName)
		}
		err = stg.Delete(r.DumpName)
		if err != nil && err != storage.ErrNotExist {
			return errors.Wrapf(err, "delete dump %s", r.OplogName)
		}
	}

	err = stg.Delete(meta.Name + MetadataFileSuffix)
	if err == storage.ErrNotExist {
		return nil
	}

	return errors.Wrap(err, "delete metadata file from storage")
}

// DeleteOlderThan deletes backups which older than given Time
func (p *PBM) DeleteOlderThan(t time.Time, l *log.Event) error {
	stg, err := p.GetStorage(l)
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

		err = p.probeDelete(m)
		if err != nil {
			l.Info("deleting %s: %v", m.Name, err)
			continue
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

	if cur.Err() != nil {
		return errors.Wrap(cur.Err(), "cursor")
	}

	fbcp, err := p.GetFirstBackup()
	if err != nil {
		return errors.Wrap(err, "get first backup")
	}
	tsto := primitive.Timestamp{}
	if fbcp == nil {
		tsto.T = uint32(time.Now().Unix())
	} else {
		tsto.T = uint32(fbcp.StartTS)
	}

	return p.deleteChunks(primitive.Timestamp{T: 1, I: 1}, tsto, stg)
}

func (p *PBM) deleteChunks(start, end primitive.Timestamp, stg storage.Storage) error {
	chunks, err := p.PITRGetChunksSlice("", start, end)
	if err != nil {
		return errors.Wrap(err, "get pitr chunks")
	}

	for _, chnk := range chunks {
		err = stg.Delete(chnk.FName)
		if err != nil && err != storage.ErrNotExist {
			return errors.Wrapf(err, "delete pitr chunk '%s' (%v) from storage", chnk.FName, chnk)
		}

		_, err = p.Conn.Database(DB).Collection(PITRChunksCollection).DeleteOne(
			p.ctx,
			bson.D{
				{"rs", chnk.RS},
				{"start_ts", chnk.StartTS},
				{"end_ts", chnk.EndTS},
			},
		)

		if err != nil {
			return errors.Wrap(err, "delete pitr chunk metadata")
		}
	}

	return nil
}
