package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/bsontype"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/ctrl"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
	"github.com/percona/percona-backup-mongodb/pbm/util"
)

const cursorCreateRetries = 10

type Meta struct {
	ID           UUID                `bson:"backupId"`
	DBpath       string              `bson:"dbpath"`
	OplogStart   BCoplogTS           `bson:"oplogStart"`
	OplogEnd     BCoplogTS           `bson:"oplogEnd"`
	CheckpointTS primitive.Timestamp `bson:"checkpointTimestamp"`
}

type BCoplogTS struct {
	TS primitive.Timestamp `bson:"ts"`
	T  int64               `bson:"t"`
}

// see https://www.percona.com/blog/2021/06/07/experimental-feature-backupcursorextend-in-percona-server-for-mongodb/
type BackupCursorData struct {
	Meta *Meta
	Data []File
}

type BackupCursor struct {
	id    UUID
	conn  *mongo.Client
	l     log.LogEvent
	opts  bson.D
	close chan struct{}

	CustomThisID string
}

func NewBackupCursor(conn *mongo.Client, l log.LogEvent, opts bson.D) *BackupCursor {
	return &BackupCursor{
		conn: conn,
		l:    l,
		opts: opts,
	}
}

var errTriesLimitExceeded = errors.New("tries limit exceeded")

func (bc *BackupCursor) create(ctx context.Context, retry int) (*mongo.Cursor, error) {
	opts := bc.opts
	for i := 0; i < retry; i++ {
		if i != 0 {
			// on retry, make new thisBackupName
			// otherwise, WT error: "Incremental identifier already exists"
			opts = make(bson.D, len(bc.opts))
			for j, a := range bc.opts {
				val := a.Value
				if a.Key == "thisBackupName" {
					bc.CustomThisID = fmt.Sprintf("%s.%d", a.Value, i)
					val = bc.CustomThisID
				}

				opts[j] = bson.E{a.Key, val}
			}
		}

		cur, err := bc.conn.Database("admin").Aggregate(ctx, mongo.Pipeline{
			{{"$backupCursor", opts}},
		})
		if err != nil {
			se, ok := err.(mongo.ServerError) //nolint:errorlint
			if !ok {
				return nil, err
			}

			if se.HasErrorCode(50915) {
				// {code: 50915,name: BackupCursorOpenConflictWithCheckpoint, categories: [RetriableError]}
				// https://github.com/percona/percona-server-mongodb/blob/psmdb-6.0.6-5/src/mongo/base/error_codes.yml#L526
				bc.l.Debug("a checkpoint took place, retrying")
				time.Sleep(time.Second * time.Duration(i+1))
				continue
			}

			return nil, err
		}

		return cur, nil
	}

	return nil, errTriesLimitExceeded
}

//nolint:nonamedreturns
func (bc *BackupCursor) Data(ctx context.Context) (_ *BackupCursorData, err error) {
	cur, err := bc.create(ctx, cursorCreateRetries)
	if err != nil {
		return nil, errors.Wrap(err, "create backupCursor")
	}
	defer func() {
		if err != nil {
			cur.Close(context.Background())
		}
	}()

	var m *Meta
	var files []File
	for cur.TryNext(ctx) {
		// metadata is the first
		if m == nil {
			mc := struct {
				Data Meta `bson:"metadata"`
			}{}
			err = cur.Decode(&mc)
			if err != nil {
				return nil, errors.Wrap(err, "decode metadata")
			}
			m = &mc.Data
			continue
		}

		var d File
		err = cur.Decode(&d)
		if err != nil {
			return nil, errors.Wrap(err, "decode filename")
		}

		files = append(files, d)
	}

	bc.id = m.ID

	bc.close = make(chan struct{})
	go func() {
		tk := time.NewTicker(time.Minute * 1)
		defer tk.Stop()

		for {
			select {
			case <-bc.close:
				bc.l.Debug("stop cursor polling: %v, cursor err: %v",
					cur.Close(context.Background()), cur.Err()) // `ctx` is already canceled, so use a background context
				return
			case <-tk.C:
				cur.TryNext(ctx)
			}
		}
	}()

	return &BackupCursorData{m, files}, nil
}

func (bc *BackupCursor) Journals(upto primitive.Timestamp) ([]File, error) {
	ctx := context.Background()
	cur, err := bc.conn.Database("admin").Aggregate(ctx,
		mongo.Pipeline{
			{{"$backupCursorExtend", bson.D{{"backupId", bc.id}, {"timestamp", upto}}}},
		})
	if err != nil {
		return nil, errors.Wrap(err, "create backupCursorExtend")
	}
	defer cur.Close(ctx)

	var j []File

	err = cur.All(ctx, &j)
	return j, err
}

func (bc *BackupCursor) Close() {
	if bc.close != nil {
		close(bc.close)
	}
}

func backupCursorName(s string) string {
	return strings.NewReplacer("-", "", ":", "").Replace(s)
}

func (b *Backup) doPhysical(
	ctx context.Context,
	bcp *ctrl.BackupCmd,
	opid ctrl.OPID,
	rsMeta *BackupReplset,
	inf *topo.NodeInfo,
	stg storage.Storage,
	l log.LogEvent,
) error {
	currOpts := bson.D{}
	if b.typ == defs.IncrementalBackup {
		currOpts = bson.D{
			// thisBackupName can be customized on retry
			{"thisBackupName", backupCursorName(bcp.Name)},
			{"incrementalBackup", true},
		}
		if !b.incrBase {
			src, err := LastIncrementalBackup(ctx, b.leadConn)
			if err != nil {
				return errors.Wrap(err, "define source backup")
			}
			if src == nil {
				return errors.Wrap(err, "nil source backup")
			}

			// ? should be done during Init()?
			if inf.IsLeader() {
				err := SetSrcBackup(ctx, b.leadConn, bcp.Name, src.Name)
				if err != nil {
					return errors.Wrap(err, "set source backup in meta")
				}
			}

			if !b.config.Storage.Equal(&src.Store.StorageConf) {
				return errors.New("cannot use the configured storage: " +
					"source backup is stored on a different storage")
			}

			// realSrcID is actual thisBackupName of the replset
			var realSrcID string
			for _, rs := range src.Replsets {
				if rs.Name == rsMeta.Name {
					realSrcID = rs.CustomThisID
					break
				}
			}
			if realSrcID == "" {
				// no custom thisBackupName was used. fallback to default
				realSrcID = backupCursorName(src.Name)
			}

			currOpts = append(currOpts, bson.E{"srcBackupName", realSrcID})
		} else {
			// We don't need any previous incremental backup history if
			// this is a base backup. So we can flush it to free up resources.
			l.Debug("flush incremental backup history")
			cr, err := NewBackupCursor(b.nodeConn, l, bson.D{
				{"disableIncrementalBackup", true},
			}).create(ctx, cursorCreateRetries)
			if err != nil {
				l.Warning("flush incremental backup history error: %v", err)
			} else {
				err = cr.Close(ctx)
				if err != nil {
					l.Warning("close cursor disableIncrementalBackup: %v", err)
				}
			}
		}
	}
	cursor := NewBackupCursor(b.nodeConn, l, currOpts)
	defer cursor.Close()

	bcur, err := cursor.Data(ctx)
	if err != nil {
		if b.typ == defs.IncrementalBackup && strings.Contains(err.Error(), "(UnknownError) 2: No such file or directory") {
			return errors.New("can't find incremental backup history." +
				" Previous backup was made on another node." +
				" You can make a new base incremental backup to start a new history.")
		}
		return errors.Wrap(err, "get backup files")
	}

	l.Debug("backup cursor id: %s", bcur.Meta.ID)

	lwts, err := topo.GetLastWrite(ctx, b.nodeConn, true)
	if err != nil {
		return errors.Wrap(err, "get shard's last write ts")
	}

	defOpts := &topo.MongodOpts{}
	defOpts.Storage.WiredTiger.EngineConfig.JournalCompressor = "snappy"
	defOpts.Storage.WiredTiger.CollectionConfig.BlockCompressor = "snappy"
	defOpts.Storage.WiredTiger.IndexConfig.PrefixCompression = true

	mopts, err := topo.GetMongodOpts(ctx, b.nodeConn, defOpts)
	if err != nil {
		return errors.Wrap(err, "get mongod options")
	}

	rsMeta.MongodOpts = mopts
	rsMeta.Status = defs.StatusRunning
	rsMeta.FirstWriteTS = bcur.Meta.OplogEnd.TS
	rsMeta.LastWriteTS = lwts
	if cursor.CustomThisID != "" {
		// custom thisBackupName was used
		rsMeta.CustomThisID = cursor.CustomThisID
	}
	err = AddRSMeta(ctx, b.leadConn, bcp.Name, *rsMeta)
	if err != nil {
		return errors.Wrap(err, "add shard's metadata")
	}

	if inf.IsLeader() {
		err := b.reconcileStatus(ctx,
			bcp.Name, opid.String(), defs.StatusRunning, util.Ref(b.timeouts.StartingStatus()))
		if err != nil {
			if errors.Is(err, errConvergeTimeOut) {
				return errors.Wrap(err, "couldn't get response from all shards")
			}
			return errors.Wrap(err, "check cluster for backup started")
		}

		err = b.setClusterFirstWrite(ctx, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "set cluster first write ts")
		}

		err = b.setClusterLastWriteForPhysical(ctx, bcp.Name)
		if err != nil {
			return errors.Wrap(err, "set cluster last write ts")
		}
	} else {
		// Waiting for cluster's StatusRunning to move further.
		err = b.waitForStatus(ctx, bcp.Name, defs.StatusRunning, nil)
		if err != nil {
			return errors.Wrap(err, "waiting for running")
		}
	}

	_, lwTS, err := b.waitForFirstLastWrite(ctx, bcp.Name)
	if err != nil {
		return errors.Wrap(err, "get cluster first & last write ts")
	}

	l.Debug("set journal up to %v", lwTS)

	jrnls, err := cursor.Journals(lwTS)
	if err != nil {
		return errors.Wrap(err, "get journal files")
	}

	data := bcur.Data
	stgb, err := getStorageBSON(bcur.Meta.DBpath)
	if err != nil {
		if !errors.Is(err, storage.ErrNotExist) {
			return errors.Wrap(err, "check storage.bson file")
		}
	} else {
		data = append(data, *stgb)
	}

	if b.typ == defs.ExternalBackup {
		return b.handleExternal(ctx, bcp, rsMeta, data, jrnls, bcur.Meta.DBpath, opid, inf, stg, l)
	}

	return b.uploadPhysical(ctx, bcp, rsMeta, data, jrnls, bcur.Meta.DBpath, stg, l)
}

func (b *Backup) handleExternal(
	ctx context.Context,
	bcp *ctrl.BackupCmd,
	rsMeta *BackupReplset,
	data []File,
	jrnls []File,
	dbpath string,
	opid ctrl.OPID,
	inf *topo.NodeInfo,
	stg storage.Storage,
	l log.LogEvent,
) error {
	filelist := make(Filelist, 0, len(data)+len(jrnls)+2) // +2 for metadata and filelist
	for _, f := range append(data, jrnls...) {
		f.Name = path.Clean("./" + strings.TrimPrefix(f.Name, dbpath))
		filelist = append(filelist, f)
	}

	// We'll rewrite rs' LastWriteTS with the cluster LastWriteTS for the meta
	// stored along with the external backup files. So do copy to preserve
	// original LastWriteTS in the meta stored on PBM storage. As rsMeta might
	// be used outside of this method.
	fsMeta := *rsMeta
	bmeta, err := NewDBManager(b.leadConn).GetBackupByName(ctx, bcp.Name)
	if err == nil {
		fsMeta.LastWriteTS = bmeta.LastWriteTS
	} else {
		l.Warning("define LastWriteTS: get backup meta: %v", err)
	}
	// save rs meta along with the data files so it can be used during the restore
	metaf := fmt.Sprintf(defs.ExternalRsMetaFile, fsMeta.Name)
	filelist = append(filelist, File{Name: metaf}, File{Name: FilelistName})
	metadst := filepath.Join(dbpath, metaf)
	err = writeRSmetaToDisk(metadst, &fsMeta)
	if err != nil {
		// we can restore without it
		l.Warning("failed to save rs meta file <%s>: %v", metadst, err)
	}

	filelistPath := filepath.Join(dbpath, FilelistName)
	err = saveFilelist(filelistPath, filelist)
	if err != nil {
		return errors.Wrap(err, "save filelist to dbpath")
	}

	if bcp.Filelist {
		// keep filelist on backup storage for listing files to copy
		bcpStoragePath := path.Join(bcp.Name, rsMeta.Name, FilelistName)
		_, err = storage.Upload(ctx, filelist, stg, compress.CompressionTypeNone, nil, bcpStoragePath, -1)
		if err != nil {
			return errors.Wrapf(err, "save filelist to storage: %q", bcpStoragePath)
		}

		defer func() {
			if err := stg.Delete(bcp.Name); err != nil {
				l.Warning("remove backup folder <%s>: %v", bcp.Name, err)
			}
		}()
	}

	err = b.toState(ctx, defs.StatusCopyReady, bcp.Name, opid.String(), inf, nil)
	if err != nil {
		return errors.Wrapf(err, "converge to %s", defs.StatusCopyReady)
	}

	l.Info("waiting for the datadir to be copied")
	err = b.waitForStatus(ctx, bcp.Name, defs.StatusCopyDone, nil)
	if err != nil {
		return errors.Wrapf(err, "waiting for %s", defs.StatusCopyDone)
	}

	err = os.Remove(metadst)
	if err != nil {
		l.Warning("remove rs meta file <%s>: %v", metadst, err)
	}
	err = os.Remove(filelistPath)
	if err != nil {
		l.Warning("remove file <%s>: %v", filelistPath, err)
	}

	return nil
}

func saveFilelist(filepath string, fl Filelist) error {
	fw, err := os.OpenFile(filepath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.Wrapf(err, "create/open")
	}
	defer fw.Close()

	if _, err = fl.WriteTo(fw); err != nil {
		return errors.Wrap(err, "write")
	}

	return errors.Wrap(fw.Sync(), "fsync")
}

func writeRSmetaToDisk(fname string, rsMeta *BackupReplset) error {
	fw, err := os.OpenFile(fname, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return errors.Wrapf(err, "create/open")
	}
	defer fw.Close()

	enc := json.NewEncoder(fw)
	enc.SetIndent("", "\t")
	err = enc.Encode(rsMeta)
	if err != nil {
		return errors.Wrap(err, "write")
	}
	err = fw.Sync()
	if err != nil {
		return errors.Wrap(err, "fsync")
	}

	return nil
}

func (b *Backup) uploadPhysical(
	ctx context.Context,
	bcp *ctrl.BackupCmd,
	rsMeta *BackupReplset,
	data,
	jrnls []File,
	dbpath string,
	stg storage.Storage,
	l log.LogEvent,
) error {
	l.Info("uploading data")
	dataFiles, err := uploadFiles(ctx, data, bcp.Name+"/"+rsMeta.Name, dbpath,
		b.typ == defs.IncrementalBackup, stg, bcp.Compression, bcp.CompressionLevel, l)
	if err != nil {
		return errors.Wrap(err, "upload data files")
	}
	l.Info("uploading data done")

	l.Info("uploading journals")
	ju, err := uploadFiles(ctx, jrnls, bcp.Name+"/"+rsMeta.Name, dbpath,
		false, stg, bcp.Compression, bcp.CompressionLevel, l)
	if err != nil {
		return errors.Wrap(err, "upload journal files")
	}
	l.Info("uploading journals done")

	filelist := Filelist(dataFiles)
	filelist = append(filelist, ju...)

	size := int64(0)
	for _, f := range filelist {
		size += f.StgSize
	}

	filelistPath := path.Join(bcp.Name, rsMeta.Name, FilelistName)
	flSize, err := storage.Upload(ctx, filelist, stg, compress.CompressionTypeNone, nil, filelistPath, -1)
	if err != nil {
		return errors.Wrapf(err, "upload filelist %q", filelistPath)
	}
	l.Info("uploaded: %q %s", filelistPath, storage.PrettySize(flSize))

	err = IncBackupSize(ctx, b.leadConn, bcp.Name, size+flSize)
	if err != nil {
		return errors.Wrap(err, "inc backup size")
	}

	return nil
}

const storagebson = "storage.bson"

func getStorageBSON(dbpath string) (*File, error) {
	f, err := os.Stat(path.Join(dbpath, storagebson))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			err = storage.ErrNotExist
		}
		return nil, err
	}

	return &File{
		Name:  path.Join(dbpath, storagebson),
		Len:   f.Size(),
		Size:  f.Size(),
		Fmode: f.Mode(),
	}, nil
}

// UUID represents a UUID as saved in MongoDB
type UUID struct{ uuid.UUID }

// MarshalBSONValue implements the bson.ValueMarshaler interface.
func (id UUID) MarshalBSONValue() (bsontype.Type, []byte, error) {
	return bson.TypeBinary, bsoncore.AppendBinary(nil, 4, id.UUID[:]), nil
}

// UnmarshalBSONValue implements the bson.ValueUnmarshaler interface.
func (id *UUID) UnmarshalBSONValue(t bsontype.Type, raw []byte) error {
	if t != bson.TypeBinary {
		return errors.New("invalid format on unmarshal bson value")
	}

	_, data, _, ok := bsoncore.ReadBinary(raw)
	if !ok {
		return errors.New("not enough bytes to unmarshal bson value")
	}

	copy(id.UUID[:], data)

	return nil
}

// IsZero implements the bson.Zeroer interface.
func (id *UUID) IsZero() bool {
	return bytes.Equal(id.UUID[:], uuid.Nil[:])
}

// Uploads given files to the storage. files may come as 16Mb (by default)
// blocks in that case it will concat consecutive blocks in one bigger file.
// For example: f1[0-16], f1[16-24], f1[64-16] becomes f1[0-24], f1[50-16].
// If this is an incremental, NOT base backup, it will skip uploading of
// unchanged files (Len == 0) but add them to the meta as we need know
// what files shouldn't be restored (those which isn't in the target backup).
func uploadFiles(
	ctx context.Context,
	files []File,
	subdir string,
	trimPrefix string,
	incr bool,
	stg storage.Storage,
	comprT compress.CompressionType,
	comprL *int,
	l log.LogEvent,
) ([]File, error) {
	if len(files) == 0 {
		return nil, nil
	}

	trim := func(fname string) string {
		// path.Clean to get rid of `/` at the beginning in case it's
		// left after TrimPrefix. Just for consistent file names in metadata
		return path.Clean("./" + strings.TrimPrefix(fname, trimPrefix))
	}

	wfile := files[0]
	data := []File{}
	for _, file := range files[1:] {
		select {
		case <-ctx.Done():
			return nil, storage.ErrCancelled
		default:
		}

		// Skip uploading unchanged files if incremental
		// but add them to the meta to keep track of files to be restored
		// from prev backups. Plus sometimes the cursor can return an offset
		// beyond the current file size. Such phantom changes shouldn't
		// be copied. But save meta to have file size.
		if incr && (file.Len == 0 || file.Off >= file.Size) {
			file.Off = -1
			file.Len = -1
			file.Name = trim(file.Name)

			data = append(data, file)
			continue
		}

		if wfile.Name == file.Name &&
			wfile.Off+wfile.Len == file.Off {
			wfile.Len += file.Len
			wfile.Size = file.Size
			continue
		}

		fw, err := writeFile(ctx, wfile, path.Join(subdir, trim(wfile.Name)), stg, comprT, comprL, l)
		if err != nil {
			return data, errors.Wrapf(err, "upload file `%s`", wfile.Name)
		}
		fw.Name = trim(wfile.Name)

		data = append(data, *fw)

		wfile = file
	}

	if incr && wfile.Off == 0 && wfile.Len == 0 {
		return data, nil
	}

	f, err := writeFile(ctx, wfile, path.Join(subdir, trim(wfile.Name)), stg, comprT, comprL, l)
	if err != nil {
		return data, errors.Wrapf(err, "upload file `%s`", wfile.Name)
	}
	f.Name = trim(wfile.Name)

	data = append(data, *f)

	return data, nil
}

func writeFile(
	ctx context.Context,
	src File,
	dst string,
	stg storage.Storage,
	compression compress.CompressionType,
	compressLevel *int,
	l log.LogEvent,
) (*File, error) {
	fstat, err := os.Stat(src.Name)
	if err != nil {
		return nil, errors.Wrap(err, "get file stat")
	}

	dst += compression.Suffix()
	sz := fstat.Size()
	if src.Len != 0 {
		// Len is always a multiple of the fixed size block (16Mb default)
		// so Off + Len might be bigger than the actual file size
		sz = src.Len
		if src.Off+src.Len > src.Size {
			sz = src.Size - src.Off
		}
		dst += fmt.Sprintf(".%d-%d", src.Off, src.Len)
	}

	_, err = storage.Upload(ctx, &src, stg, compression, compressLevel, dst, sz)
	if err != nil {
		return nil, errors.Wrap(err, "upload file")
	}

	finf, err := stg.FileStat(dst)
	if err != nil {
		return nil, errors.Wrapf(err, "get storage file stat %s", dst)
	}

	return &File{
		Name:    src.Name,
		Size:    fstat.Size(),
		Fmode:   fstat.Mode(),
		StgSize: finf.Size,
		Off:     src.Off,
		Len:     src.Len,
	}, nil
}
