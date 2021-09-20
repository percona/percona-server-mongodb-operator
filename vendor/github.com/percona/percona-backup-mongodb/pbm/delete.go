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

	tlns, err := p.PITRTimelines()
	if err != nil {
		return errors.Wrap(err, "get PITR chunks")
	}

	err = p.probeDelete(meta, tlns)
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

	return nil
}

func (p *PBM) probeDelete(backup *BackupMeta, tlns []Timeline) error {
	// check if backup isn't running
	switch backup.Status {
	case StatusDone, StatusCancelled, StatusError:
	default:
		return errors.Errorf("unable to delete backup in %s state", backup.Status)
	}

	// if backup isn't a base for any PITR timeline
	for _, t := range tlns {
		if backup.LastWriteTS.T == t.Start {
			return errors.Errorf("unable to delete: backup is a base for '%s'", t)
		}
	}

	ispitr, err := p.IsPITR()
	if err != nil {
		return errors.Wrap(err, "unable check pitr state")
	}

	// if PITR is ON and there are no chunks yet we shouldn't delete the most recent back
	if !ispitr || tlns != nil {
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

	tlns, err := p.PITRTimelines()
	if err != nil {
		return errors.Wrap(err, "get PITR chunks")
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

		err = p.probeDelete(m, tlns)
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

	return nil
}

// DeletePITR deletes backups which older than given `until` Time. It will round `until` down
// to the last write of the closest preceding backup in order not to make gaps. So usually it
// gonna leave some extra chunks. E.g. if `until = 13` and the last write of the closest preceding
// backup is `10` it will leave `11` and `12` chunks as well since `13` won't be restorable
// without `11` and `12` (contiguous timeline from the backup).
// It deletes all chunks if `until` is nil.
func (p *PBM) DeletePITR(until *time.Time, l *log.Event) error {
	stg, err := p.GetStorage(l)
	if err != nil {
		return errors.Wrap(err, "get storage")
	}

	var zerots primitive.Timestamp
	if until == nil {
		return p.deleteChunks(zerots, zerots, stg)
	}

	bcp, err := p.GetLastBackup(&primitive.Timestamp{T: uint32(until.Unix()), I: 0})
	if err != nil {
		return errors.Wrap(err, "get recent backup")
	}

	return p.deleteChunks(zerots, bcp.LastWriteTS, stg)
}

func (p *PBM) deleteChunks(start, until primitive.Timestamp, stg storage.Storage) (err error) {
	var chunks []PITRChunk

	if until.T > 0 {
		chunks, err = p.PITRGetChunksSliceUntil("", until)
	} else {
		chunks, err = p.PITRGetChunksSlice("", start, until)
	}
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
