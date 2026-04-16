package ctrl

import (
	"bytes"
	"fmt"
	"strconv"

	"go.mongodb.org/mongo-driver/bson/primitive"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/config"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

// Command represents actions that could be done on behalf of the client by the agents
type Command string

const (
	CmdUndefined           Command = ""
	CmdAddConfigProfile    Command = "addConfigProfile"
	CmdRemoveConfigProfile Command = "removeConfigProfile"
	CmdBackup              Command = "backup"
	CmdRestore             Command = "restore"
	CmdReplay              Command = "replay"
	CmdCancelBackup        Command = "cancelBackup"
	CmdResync              Command = "resync"
	CmdPITR                Command = "pitr"
	CmdDeleteBackup        Command = "delete"
	CmdDeletePITR          Command = "deletePitr"
	CmdCleanup             Command = "cleanup"
)

func (c Command) String() string {
	switch c {
	case CmdAddConfigProfile:
		return "Add Config Profile"
	case CmdRemoveConfigProfile:
		return "Remove Config Profile"
	case CmdBackup:
		return "Snapshot backup"
	case CmdRestore:
		return "Snapshot restore"
	case CmdReplay:
		return "Oplog replay"
	case CmdCancelBackup:
		return "Backup cancellation"
	case CmdResync:
		return "Resync storage"
	case CmdPITR:
		return "PITR incremental backup"
	case CmdDeleteBackup:
		return "Delete"
	case CmdDeletePITR:
		return "Delete PITR chunks"
	case CmdCleanup:
		return "Cleanup backups and PITR chunks"
	default:
		return "Undefined"
	}
}

type OPID primitive.ObjectID

func ParseOPID(s string) (OPID, error) {
	o, err := primitive.ObjectIDFromHex(s)
	if err != nil {
		return OPID(primitive.NilObjectID), err
	}
	return OPID(o), nil
}

var NilOPID = OPID(primitive.NilObjectID)

func (o OPID) String() string {
	return primitive.ObjectID(o).Hex()
}

func (o OPID) Obj() primitive.ObjectID {
	return primitive.ObjectID(o)
}

type Cmd struct {
	Cmd        Command          `bson:"cmd"`
	Resync     *ResyncCmd       `bson:"resync,omitempty"`
	Profile    *ProfileCmd      `bson:"profile,omitempty"`
	Backup     *BackupCmd       `bson:"backup,omitempty"`
	Restore    *RestoreCmd      `bson:"restore,omitempty"`
	Replay     *ReplayCmd       `bson:"replay,omitempty"`
	Delete     *DeleteBackupCmd `bson:"delete,omitempty"`
	DeletePITR *DeletePITRCmd   `bson:"deletePitr,omitempty"`
	Cleanup    *CleanupCmd      `bson:"cleanup,omitempty"`
	TS         int64            `bson:"ts"`
	OPID       OPID             `bson:"-"`
}

func (c Cmd) String() string {
	var buf bytes.Buffer

	buf.WriteString(string(c.Cmd))
	switch c.Cmd {
	case CmdBackup:
		buf.WriteString(" [")
		buf.WriteString(c.Backup.String())
		buf.WriteString("]")
	case CmdRestore:
		buf.WriteString(" [")
		buf.WriteString(c.Restore.String())
		buf.WriteString("]")
	}
	buf.WriteString(" <ts: ")
	buf.WriteString(strconv.FormatInt(c.TS, 10))
	buf.WriteString(">")
	return buf.String()
}

type ProfileCmd struct {
	Name      string             `bson:"name"`
	IsProfile bool               `bson:"profile"`
	Storage   config.StorageConf `bson:"storage"`
}

type ResyncCmd struct {
	Name            string `bson:"name,omitempty"`
	All             bool   `bson:"all,omitempty"`
	Clear           bool   `bson:"clear,omitempty"`
	IncludeRestores bool   `bson:"includeRestores,omitempty"`
}

type BackupCmd struct {
	Type             defs.BackupType          `bson:"type"`
	IncrBase         bool                     `bson:"base"`
	Name             string                   `bson:"name"`
	Namespaces       []string                 `bson:"nss,omitempty"`
	Compression      compress.CompressionType `bson:"compression"`
	CompressionLevel *int                     `bson:"level,omitempty"`
	NumParallelColls *int32                   `bson:"numParallelColls,omitempty"`
	Filelist         bool                     `bson:"filelist,omitempty"`
	Profile          string                   `bson:"profile,omitempty"`
}

func (b BackupCmd) String() string {
	var level string
	if b.CompressionLevel == nil {
		level = "default"
	} else {
		level = strconv.Itoa(*b.CompressionLevel)
	}
	return fmt.Sprintf("name: %s, compression: %s (level: %s)", b.Name, b.Compression, level)
}

type RestoreCmd struct {
	Name            string            `bson:"name"`
	BackupName      string            `bson:"backupName"`
	Namespaces      []string          `bson:"nss,omitempty"`
	NamespaceFrom   string            `bson:"nsFrom,omitempty"`
	NamespaceTo     string            `bson:"nsTo,omitempty"`
	UsersAndRoles   bool              `bson:"usersAndRoles,omitempty"`
	RSMap           map[string]string `bson:"rsMap,omitempty"`
	Fallback        *bool             `bson:"fallbackEnabled"`
	AllowPartlyDone *bool             `bson:"allowPartlyDone"`

	NumParallelColls    *int32 `bson:"numParallelColls,omitempty"`
	NumInsertionWorkers *int32 `bson:"numInsertionWorkers,omitempty"`

	OplogTS primitive.Timestamp `bson:"oplogTS,omitempty"`

	External bool                `bson:"external"`
	ExtConf  topo.ExternOpts     `bson:"extConf"`
	ExtTS    primitive.Timestamp `bson:"extTS"`
}

func (r RestoreCmd) String() string {
	bcp := ""
	if r.BackupName != "" {
		bcp = "snapshot: " + r.BackupName
	}
	if r.External {
		bcp += "[external]"
	}
	if r.ExtTS.T > 0 {
		bcp += fmt.Sprintf(" external ts: <%d,%d>", r.ExtTS.T, r.ExtTS.I)
	}
	if r.OplogTS.T > 0 {
		bcp += fmt.Sprintf(" point-in-time: <%d,%d>", r.OplogTS.T, r.OplogTS.I)
	}

	return fmt.Sprintf("name: %s, %s", r.Name, bcp)
}

type ReplayCmd struct {
	Name  string              `bson:"name"`
	Start primitive.Timestamp `bson:"start,omitempty"`
	End   primitive.Timestamp `bson:"end,omitempty"`
	RSMap map[string]string   `bson:"rsMap,omitempty"`
}

func (c ReplayCmd) String() string {
	return fmt.Sprintf("name: %s, time: %d - %d", c.Name, c.Start, c.End)
}

type DeleteBackupCmd struct {
	Backup    string          `bson:"backup"`
	OlderThan int64           `bson:"olderthan"`
	Type      defs.BackupType `bson:"type"`
}

type DeletePITRCmd struct {
	OlderThan int64 `bson:"olderthan"`
}

type CleanupCmd struct {
	OlderThan primitive.Timestamp `bson:"olderThan"`
}

func (d DeleteBackupCmd) String() string {
	return fmt.Sprintf("backup: %s, older than: %d", d.Backup, d.OlderThan)
}
