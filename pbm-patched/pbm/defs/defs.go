package defs

import (
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

const (
	// DB is a name of the PBM database
	DB = "admin"
	// LogCollection is the name of the mongo collection that contains PBM logs
	LogCollection = "pbmLog"
	// ConfigCollection is the name of the mongo collection that contains PBM configs
	ConfigCollection = "pbmConfig"
	// LockCollection is the name of the mongo collection that is used
	// by agents to coordinate mutually exclusive operations (e.g. backup/restore)
	LockCollection = "pbmLock"
	// LockOpCollection is the name of the mongo collection that is used
	// by agents to coordinate operations that don't need to be
	// mutually exclusive to other operation types (e.g. backup-delete)
	LockOpCollection = "pbmLockOp"
	// BcpCollection is a collection for backups metadata
	BcpCollection = "pbmBackups"
	// RestoresCollection is a collection for restores metadata
	RestoresCollection = "pbmRestores"
	// CmdStreamCollection is the name of the mongo collection that contains backup/restore commands stream
	CmdStreamCollection = "pbmCmd"
	// PITRChunksCollection contains index metadata of PITR chunks
	PITRChunksCollection = "pbmPITRChunks"
	// pbmPITR is a collection for PITR operational data
	PITRCollection = "pbmPITR"
	// PBMOpLogCollection contains log of acquired locks (hence run ops)
	PBMOpLogCollection = "pbmOpLog"
	// AgentsStatusCollection is an agents registry with its status/health checks
	AgentsStatusCollection = "pbmAgents"
)

const (
	ConfigDatabasesNS      = "config.databases"
	ConfigCollectionsNS    = "config.collections"
	ConfigChunksNS         = "config.chunks"
	ConfigSystemSessionsNS = "config.system.sessions"
)

const (
	// TmpUsersCollection and TmpRoles are tmp collections used to avoid
	// user related issues while resoring on new cluster and preserving UUID
	// See https://jira.percona.com/browse/PBM-425, https://jira.percona.com/browse/PBM-636
	TmpUsersCollection = `pbmRUsers`
	TmpRolesCollection = `pbmRRoles`
)

const (
	PITRcheckRange       = time.Second * 15
	AgentsStatCheckRange = time.Second * 5
)

var (
	WaitActionStart = time.Second * 15
	WaitBackupStart = WaitActionStart + PITRcheckRange*12/10 // 33 seconds
)

type NodeHealth int

const (
	NodeHealthDown NodeHealth = iota
	NodeHealthUp
)

type NodeState int

// https://github.com/mongodb/mongo/blob/v8.0/src/mongo/db/repl/member_state.h#L52-L109
const (
	NodeStateStartup NodeState = iota
	NodeStatePrimary
	NodeStateSecondary
	NodeStateRecovering
	_NodeStateRSFatal // mongo::repl::MemberState::MS::OBSOLETE_RS_FATAL
	NodeStateStartup2
	NodeStateUnknown
	NodeStateArbiter
	NodeStateDown
	NodeStateRollback
	NodeStateRemoved
)

type BackupType string

const (
	PhysicalBackup    BackupType = "physical"
	ExternalBackup    BackupType = "external"
	IncrementalBackup BackupType = "incremental"
	LogicalBackup     BackupType = "logical"
)

// Status is a backup current status
type Status string

const (
	StatusInit  Status = "init"
	StatusReady Status = "ready"

	// for phys restore, to indicate shards have been stopped
	StatusDown Status = "down"

	StatusStarting       Status = "starting"
	StatusCleanupCluster Status = "cleanupCluster"
	StatusRunning        Status = "running"
	StatusDumpDone       Status = "dumpDone"
	StatusCopyReady      Status = "copyReady"
	StatusCopyDone       Status = "copyDone"
	StatusPartlyDone     Status = "partlyDone"
	StatusDone           Status = "done"
	StatusCancelled      Status = "canceled"
	StatusError          Status = "error"

	// status to communicate last op timestamp if it's not set
	// during external restore
	StatusExtTS Status = "lastTS"
)

func (s Status) IsRunning() bool {
	switch s {
	case
		StatusDone,
		StatusCancelled,
		StatusError:
		return false
	}

	return true
}

type PrintStatus string

const (
	statusSuccess PrintStatus = "success"
	statusFailed  PrintStatus = "failed"
	statusOngoing PrintStatus = "ongoing"
)

var ErrIncompatible = errors.New("incompatible")

func (s Status) PrintStatus(errs ...error) PrintStatus {
	var err error
	if len(errs) > 0 {
		err = errs[0]
	}

	switch s {
	case StatusDone:
		return statusSuccess

	case StatusCancelled:
		return statusFailed

	case StatusError:
		// "incompatible" is treated as success
		if err != nil && errors.Is(err, ErrIncompatible) {
			return statusSuccess
		}
		return statusFailed

	default:
		return statusOngoing
	}
}

type Operation string

const (
	OperationInsert  Operation = "i"
	OperationNoop    Operation = "n"
	OperationUpdate  Operation = "u"
	OperationDelete  Operation = "d"
	OperationCommand Operation = "c"
)

const StaleFrameSec uint32 = 30

const MaxReplicationLagTimeSec = 21

const (
	// MetadataFileSuffix is a suffix for the metadata file on a storage
	MetadataFileSuffix = ".pbm.json"

	ExternalRsMetaFile = "pbm.rsmeta.%s.json"

	StorInitFile    = ".pbm.init"
	PhysRestoresDir = ".pbm.restore"
)

const (
	// DefaultPITRInterval oplog slicing time span
	DefaultPITRInterval = time.Minute * 10
	// PITRfsPrefix is a prefix (folder) for PITR chunks on the storage
	PITRfsPrefix = "pbmPitr"
)

const DefaultCompression = compress.CompressionTypeS2
