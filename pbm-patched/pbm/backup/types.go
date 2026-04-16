package backup

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/archive"
	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

// BackupMeta is a backup's metadata
type BackupMeta struct {
	Type defs.BackupType `bson:"type" json:"type"`
	OPID string          `bson:"opid" json:"opid"`
	Name string          `bson:"name" json:"name"`

	// SrcBackup is the source for the incremental backups. The souce might be
	// incremental as well.
	// Empty means this is a full backup (and a base for further incremental bcps).
	SrcBackup string `bson:"src_backup,omitempty" json:"src_backup,omitempty"`

	// Increments is a list of all increments, including failed or canceled.
	// Each top-level slice cell contains the list of the next incremental backup attempts.
	// If the value is nil, increments have not been fetched.
	// If the value is an empty non-nil slice, no increment exists.
	Increments [][]*BackupMeta `bson:"-" json:"-"`

	// ShardRemap is map of replset to shard names.
	// If shard name is different from replset name, it will be stored in the map.
	// If all shard names are the same as their replset names, the map is nil.
	ShardRemap map[string]string `bson:"shardRemap,omitempty" json:"shardRemap,omitempty"`

	Namespaces       []string                 `bson:"nss,omitempty" json:"nss,omitempty"`
	Replsets         []BackupReplset          `bson:"replsets" json:"replsets"`
	Compression      compress.CompressionType `bson:"compression" json:"compression"`
	Store            Storage                  `bson:"store" json:"store"`
	Size             int64                    `bson:"size" json:"size"`
	SizeUncompressed int64                    `bson:"size_uncompressed" json:"size_uncompressed"`
	MongoVersion     string                   `bson:"mongodb_version" json:"mongodb_version"`
	FCV              string                   `bson:"fcv" json:"fcv"`
	StartTS          int64                    `bson:"start_ts" json:"start_ts"`
	LastTransitionTS int64                    `bson:"last_transition_ts" json:"last_transition_ts"`
	FirstWriteTS     primitive.Timestamp      `bson:"first_write_ts" json:"first_write_ts"`
	LastWriteTS      primitive.Timestamp      `bson:"last_write_ts" json:"last_write_ts"`
	Hb               primitive.Timestamp      `bson:"hb" json:"hb"`
	Status           defs.Status              `bson:"status" json:"status"`
	Conditions       []Condition              `bson:"conditions" json:"conditions"`
	Nomination       []BackupRsNomination     `bson:"n" json:"n"`
	Err              string                   `bson:"error,omitempty" json:"error,omitempty"`
	PBMVersion       string                   `bson:"pbm_version" json:"pbm_version"`
	BalancerStatus   topo.BalancerMode        `bson:"balancer" json:"balancer"`
	runtimeError     error
}

func (b *BackupMeta) Error() error {
	switch {
	case b.runtimeError != nil:
		return b.runtimeError
	case b.Err != "":
		return errors.New(b.Err)
	default:
		return nil
	}
}

func (b *BackupMeta) SetRuntimeError(err error) {
	b.runtimeError = err
	b.Status = defs.StatusError
}

// RS returns the metadata of the replset with given name.
// It returns nil if no replset found.
func (b *BackupMeta) RS(name string) *BackupReplset {
	for _, rs := range b.Replsets {
		if rs.Name == name {
			return &rs
		}
	}
	return nil
}

// Storage keeps storage configuration used during backup.
//
// If external configuration is used, IsProfile is `true` and Name is set.
type Storage struct {
	// Name is config profile name.
	Name string `bson:"name,omitempty" json:"name,omitempty"`
	// IsProfile is true when storage is non-main (external).
	IsProfile bool `bson:"profile,omitempty" json:"profile,omitempty"`

	config.StorageConf `bson:",inline" json:",inline"`
}

// BackupRsNomination is used to choose (nominate and elect) nodes for the backup
// within a replica set
type BackupRsNomination struct {
	RS    string   `bson:"rs" json:"rs"`
	Nodes []string `bson:"n" json:"n"`
	Ack   string   `bson:"ack" json:"ack"`
}

type BackupReplset struct {
	Name string `bson:"name" json:"name"`

	// Journal is not used. left for backward compatibility
	Journal          []File              `bson:"journal,omitempty" json:"journal,omitempty"`
	Files            []File              `bson:"files,omitempty" json:"files,omitempty"`
	DumpName         string              `bson:"dump_name,omitempty" json:"backup_name,omitempty"`
	OplogName        string              `bson:"oplog_name,omitempty" json:"oplog_name,omitempty"`
	StartTS          int64               `bson:"start_ts" json:"start_ts"`
	Status           defs.Status         `bson:"status" json:"status"`
	Size             int64               `bson:"size" json:"size"`
	SizeUncompressed int64               `bson:"size_uncompressed" json:"size_uncompressed"`
	IsConfigSvr      *bool               `bson:"iscs,omitempty" json:"iscs,omitempty"`
	IsConfigShard    *bool               `bson:"configshard,omitempty" json:"configshard,omitempty"`
	LastTransitionTS int64               `bson:"last_transition_ts" json:"last_transition_ts"`
	FirstWriteTS     primitive.Timestamp `bson:"first_write_ts" json:"first_write_ts"`
	LastWriteTS      primitive.Timestamp `bson:"last_write_ts" json:"last_write_ts"`
	Node             string              `bson:"node" json:"node"` // node that performed backup
	Error            string              `bson:"error,omitempty" json:"error,omitempty"`
	Conditions       []Condition         `bson:"conditions" json:"conditions"`
	MongodOpts       *topo.MongodOpts    `bson:"mongod_opts,omitempty" json:"mongod_opts,omitempty"`

	// required for external backup (PBM-1252)
	PBMVersion   string `bson:"pbm_version,omitempty" json:"pbm_version,omitempty"`
	MongoVersion string `bson:"mongo_version,omitempty" json:"mongo_version,omitempty"`

	// CustomThisID is customized thisBackupName value for $backupCursor (in WT: "this_id").
	// If it is not set (empty), the default value was used.
	CustomThisID string `bson:"this_id,omitempty" json:"this_id,omitempty"`
}

type Condition struct {
	Timestamp int64       `bson:"timestamp" json:"timestamp"`
	Status    defs.Status `bson:"status" json:"status"`
	Error     string      `bson:"error,omitempty" json:"error,omitempty"`
}

type File struct {
	Name    string      `bson:"filename" json:"filename"`
	Off     int64       `bson:"offset" json:"offset"` // offset for incremental backups
	Len     int64       `bson:"length" json:"length"` // length of chunk after the offset
	Size    int64       `bson:"fileSize" json:"fileSize"`
	StgSize int64       `bson:"stgSize" json:"stgSize"`
	Fmode   os.FileMode `bson:"fmode" json:"fmode"`
}

func (f File) String() string {
	if f.Off == 0 && f.Len == 0 {
		return f.Name
	}
	return fmt.Sprintf("%s [%d:%d]", f.Name, f.Off, f.Len)
}

func (f File) Path(c compress.CompressionType) string {
	src := filepath.Join(f.Name + c.Suffix())
	if f.Len == 0 {
		return src
	}

	return fmt.Sprintf("%s.%d-%d", src, f.Off, f.Len)
}

func (f *File) WriteTo(w io.Writer) (int64, error) {
	fd, err := os.Open(f.Name)
	if err != nil {
		return 0, errors.Wrap(err, "open file for reading")
	}
	defer fd.Close()

	if f.Len == 0 && f.Off == 0 {
		return io.Copy(w, fd)
	}

	return io.Copy(w, io.NewSectionReader(fd, f.Off, f.Len))
}

// FilelistName is filename that is used to store list of files for physical backup
const FilelistName = "filelist.pbm"

// Filelist represents a list of files.
type Filelist []File

func (filelist Filelist) WriteTo(w io.Writer) (int64, error) {
	size := int64(0)

	for _, file := range filelist {
		data, err := bson.Marshal(file)
		if err != nil {
			return 0, errors.Wrap(err, "json encode")
		}

		n, err := w.Write(data)
		if err != nil {
			return size, errors.Wrap(err, "write")
		}
		if n != len(data) {
			return size, io.ErrShortWrite
		}

		size += int64(n)
	}

	return size, nil
}

func ReadFilelist(r io.Reader) (Filelist, error) {
	filelist := Filelist{}

	var err error
	buf := make([]byte, archive.MaxBSONSize)
	for {
		buf, err = archive.ReadBSONBuffer(r, buf[:cap(buf)])
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}

			return nil, errors.Wrap(err, "read bson")
		}

		file := File{}
		err = bson.Unmarshal(buf, &file)
		if err != nil {
			return nil, errors.Wrap(err, "decode")
		}

		filelist = append(filelist, file)
	}

	return filelist, nil
}
