package pbm

import (
	"bytes"
	"encoding/json"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"

	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/version"
)

const StorInitFile = ".pbm.init"

// ResyncStorage updates PBM metadata (snapshots and pitr) according to the data in the storage
func (p *PBM) ResyncStorage(l *log.Event) error {
	stg, err := p.GetStorage(l)
	if err != nil {
		return errors.Wrap(err, "unable to get backup store")
	}

	_, err = stg.FileStat(StorInitFile)
	if errors.Is(err, storage.ErrNotExist) {
		err = stg.Save(StorInitFile, bytes.NewBufferString(version.DefaultInfo.Version), 0)
	}
	if err != nil {
		return errors.Wrap(err, "init storage")
	}

	bcps, err := stg.Files(MetadataFileSuffix)
	if err != nil {
		return errors.Wrap(err, "get a backups list from the storage")
	}

	err = p.moveCollection(BcpCollection, BcpOldCollection)
	if err != nil {
		return errors.Wrapf(err, "copy current backups meta from %s to %s", BcpCollection, BcpOldCollection)
	}
	err = p.moveCollection(PITRChunksCollection, PITRChunksOldCollection)
	if err != nil {
		return errors.Wrapf(err, "copy current pitr meta from %s to %s", PITRChunksCollection, PITRChunksOldCollection)
	}

	if len(bcps) == 0 {
		return nil
	}

	var ins []interface{}
	for _, b := range bcps {
		v := BackupMeta{}
		err = json.Unmarshal(b, &v)
		if err != nil {
			return errors.Wrap(err, "unmarshal backup meta")
		}
		err = checkBackupFiles(&v, stg)
		if err != nil {
			l.Warning("skip snapshot %s: %v", v.Name, err)
			continue
		}
		ins = append(ins, v)
	}
	_, err = p.Conn.Database(DB).Collection(BcpCollection).InsertMany(p.ctx, ins)
	if err != nil {
		return errors.Wrap(err, "insert retrieved backups meta")
	}

	pitrf, err := stg.List(PITRfsPrefix)
	if err != nil {
		return errors.Wrap(err, "get list of pitr chunks")
	}
	if len(pitrf) == 0 {
		return nil
	}

	var pitr []interface{}
	for _, f := range pitrf {
		_, err := stg.FileStat(PITRfsPrefix + "/" + f.Name)
		if err != nil {
			l.Warning("skip %s because of %v", f, err)
			continue
		}
		chnk := PITRmetaFromFName(f.Name)
		if chnk != nil {
			pitr = append(pitr, chnk)
		}
	}

	if len(pitr) == 0 {
		return nil
	}

	_, err = p.Conn.Database(DB).Collection(PITRChunksCollection).InsertMany(p.ctx, pitr)
	if err != nil {
		return errors.Wrap(err, "insert retrieved pitr meta")
	}

	return nil
}

func checkBackupFiles(bcp *BackupMeta, stg storage.Storage) error {
	for _, rs := range bcp.Replsets {
		f, err := stg.FileStat(rs.DumpName)
		if err != nil {
			return errors.Wrapf(err, "file %s", rs.DumpName)
		}
		if f.Size == 0 {
			return errors.Errorf("%s is empty", rs.DumpName)
		}

		f, err = stg.FileStat(rs.OplogName)
		if err != nil {
			return errors.Wrapf(err, "file %s", rs.OplogName)
		}
		if f.Size == 0 {
			return errors.Errorf("%s is empty", rs.OplogName)
		}
	}

	return nil
}

func (p *PBM) moveCollection(coll, as string) error {
	err := p.Conn.Database(DB).Collection(as).Drop(p.ctx)
	if err != nil {
		return errors.Wrap(err, "failed to remove old archive from backups metadata")
	}

	cur, err := p.Conn.Database(DB).Collection(coll).Find(p.ctx, bson.M{})
	if err != nil {
		return errors.Wrap(err, "get current data")
	}
	for cur.Next(p.ctx) {
		_, err = p.Conn.Database(DB).Collection(as).InsertOne(p.ctx, cur.Current)
		if err != nil {
			return errors.Wrapf(err, "insert")
		}
	}

	if cur.Err() != nil {
		return cur.Err()
	}

	_, err = p.Conn.Database(DB).Collection(coll).DeleteMany(p.ctx, bson.M{})
	return errors.Wrap(err, "remove current data")
}
