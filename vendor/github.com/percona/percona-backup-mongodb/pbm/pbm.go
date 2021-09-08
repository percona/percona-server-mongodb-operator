package pbm

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readconcern"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"go.mongodb.org/mongo-driver/mongo/writeconcern"

	"github.com/percona/percona-backup-mongodb/pbm/log"
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
	// by agents to coordinate operations that doesn't need to be
	// mutually exclusive to other operation types (e.g. backup-delete)
	LockOpCollection = "pbmLockOp"
	// BcpCollection is a collection for backups metadata
	BcpCollection = "pbmBackups"
	// BcpOldCollection contains a backup of backups metadata
	BcpOldCollection = "pbmBackups.old"
	// RestoresCollection is a collection for restores metadata
	RestoresCollection = "pbmRestores"
	// CmdStreamCollection is the name of the mongo collection that contains backup/restore commands stream
	CmdStreamCollection = "pbmCmd"
	//PITRChunksCollection contains index metadata of PITR chunks
	PITRChunksCollection = "pbmPITRChunks"
	//PITRChunksOldCollection contains archived index metadata of PITR chunks
	PITRChunksOldCollection = "pbmPITRChunks.old"
	// PBMOpLogCollection contains log of aquired locks (hence run ops)
	PBMOpLogCollection = "pbmOpLog"
	// AgentsStatusCollection is an agents registry with its status/health checks
	AgentsStatusCollection = "pbmAgents"

	// MetadataFileSuffix is a suffix for the metadata file on a storage
	MetadataFileSuffix = ".pbm.json"
)

// ErrNotFound - object not found
var ErrNotFound = errors.New("not found")

// Command represents actions that could be done on behalf of the client by the agents
type Command string

const (
	CmdUndefined        Command = ""
	CmdBackup           Command = "backup"
	CmdRestore          Command = "restore"
	CmdCancelBackup     Command = "cancelBackup"
	CmdResyncBackupList Command = "resyncBcpList"
	CmdPITR             Command = "pitr"
	CmdPITRestore       Command = "pitrestore"
	CmdDeleteBackup     Command = "delete"
)

func (c Command) String() string {
	switch c {
	case CmdBackup:
		return "Snapshot backup"
	case CmdRestore:
		return "Snapshot restore"
	case CmdCancelBackup:
		return "Backup cancelation"
	case CmdResyncBackupList:
		return "Resync storage"
	case CmdPITR:
		return "PITR incremental backup"
	case CmdPITRestore:
		return "PITR restore"
	case CmdDeleteBackup:
		return "Delete"
	default:
		return "Undefined"
	}
}

type OPID primitive.ObjectID

type Cmd struct {
	Cmd        Command         `bson:"cmd"`
	Backup     BackupCmd       `bson:"backup,omitempty"`
	Restore    RestoreCmd      `bson:"restore,omitempty"`
	PITRestore PITRestoreCmd   `bson:"pitrestore,omitempty"`
	Delete     DeleteBackupCmd `bson:"delete,omitempty"`
	TS         int64           `bson:"ts"`
	OPID       OPID            `bson:"-"`
}

func OPIDfromStr(s string) (OPID, error) {
	o, err := primitive.ObjectIDFromHex(s)
	if err != nil {
		return OPID(primitive.NilObjectID), err
	}
	return OPID(o), nil
}

func NilOPID() OPID { return OPID(primitive.NilObjectID) }

func (o OPID) String() string {
	return primitive.ObjectID(o).Hex()
}

func (o OPID) Obj() primitive.ObjectID {
	return primitive.ObjectID(o)
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
	case CmdPITRestore:
		buf.WriteString(" [")
		buf.WriteString(c.PITRestore.String())
		buf.WriteString("]")
	}
	buf.WriteString(" <ts: ")
	buf.WriteString(strconv.FormatInt(c.TS, 10))
	buf.WriteString(">")
	return buf.String()
}

type BackupCmd struct {
	Name        string          `bson:"name"`
	Compression CompressionType `bson:"compression"`
}

func (b BackupCmd) String() string {
	return fmt.Sprintf("name: %s, compression: %s", b.Name, b.Compression)
}

type RestoreCmd struct {
	Name       string `bson:"name"`
	BackupName string `bson:"backupName"`
}

func (r RestoreCmd) String() string {
	return fmt.Sprintf("name: %s, backup name: %s", r.Name, r.BackupName)
}

type PITRestoreCmd struct {
	Name string `bson:"name"`
	TS   int64  `bson:"ts"`
}

func (p PITRestoreCmd) String() string {
	return fmt.Sprintf("name: %s, point-in-time ts: %d", p.Name, p.TS)
}

type DeleteBackupCmd struct {
	Backup    string `bson:"backup"`
	OlderThan int64  `bson:"olderthan"`
}

func (d DeleteBackupCmd) String() string {
	return fmt.Sprintf("backup: %s, older than: %d", d.Backup, d.OlderThan)
}

type CompressionType string

const (
	CompressionTypeNone   CompressionType = "none"
	CompressionTypeGZIP   CompressionType = "gzip"
	CompressionTypePGZIP  CompressionType = "pgzip"
	CompressionTypeSNAPPY CompressionType = "snappy"
	CompressionTypeLZ4    CompressionType = "lz4"
	CompressionTypeS2     CompressionType = "s2"
)

const (
	PITRcheckRange       = time.Second * 15
	AgentsStatCheckRange = time.Second * 5
)

var (
	WaitActionStart = time.Second * 15
	WaitBackupStart = WaitActionStart + PITRcheckRange*12/10
)

// OpLog represents log of started operation.
// Operation progress can be get from logs by OPID.
// Basically it is a log of all ever taken locks. With the
// uniqueness by rs + opid
type OpLog struct {
	LockHeader `bson:",inline" json:",inline"`
}

type PBM struct {
	Conn *mongo.Client
	log  *log.Logger
	ctx  context.Context
}

// New creates a new PBM object.
// In the sharded cluster both agents and ctls should have a connection to ConfigServer replica set in order to communicate via PBM collections.
// If agent's or ctl's local node is not a member of CongigServer, after discovering current topology connection will be established to ConfigServer.
func New(ctx context.Context, uri, appName string) (*PBM, error) {
	uri = "mongodb://" + strings.Replace(uri, "mongodb://", "", 1)

	client, err := connect(ctx, uri, appName)
	if err != nil {
		return nil, errors.Wrap(err, "create mongo connection")
	}

	pbm := &PBM{
		Conn: client,
		ctx:  ctx,
	}
	inf, err := pbm.GetNodeInfo()
	if err != nil {
		return nil, errors.Wrap(err, "get topology")
	}

	if !inf.IsSharded() || inf.ReplsetRole() == ReplRoleConfigSrv {
		return pbm, errors.Wrap(pbm.setupNewDB(), "setup a new backups db")
	}

	csvr := struct {
		URI string `bson:"configsvrConnectionString"`
	}{}
	err = client.Database("admin").Collection("system.version").
		FindOne(ctx, bson.D{{"_id", "shardIdentity"}}).Decode(&csvr)
	if err != nil {
		return nil, errors.Wrap(err, "get config server connetion URI")
	}
	// no need in this connection anymore, we need a new one with the ConfigServer
	err = client.Disconnect(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "diconnect old client")
	}

	chost := strings.Split(csvr.URI, "/")
	if len(chost) < 2 {
		return nil, errors.Wrapf(err, "define config server connetion URI from %s", csvr.URI)
	}

	curi, err := url.Parse(uri)
	if err != nil {
		return nil, errors.Wrapf(err, "parse mongo-uri '%s'", uri)
	}

	// Preserving the `replicaSet` parameter will cause an error while connecting to the ConfigServer (mismatched replicaset names)
	query := curi.Query()
	query.Del("replicaSet")
	curi.RawQuery = query.Encode()
	curi.Host = chost[1]
	pbm.Conn, err = connect(ctx, curi.String(), appName)
	if err != nil {
		return nil, errors.Wrapf(err, "create mongo connection to configsvr with connection string '%s'", curi)
	}

	return pbm, errors.Wrap(pbm.setupNewDB(), "setup a new backups db")
}

func (p *PBM) InitLogger(rs, node string) {
	p.log = log.New(p.Conn.Database(DB).Collection(LogCollection), rs, node)
}

func (p *PBM) Logger() *log.Logger {
	return p.log
}

const (
	cmdCollectionSizeBytes      = 1 << 20  // 1Mb
	pbmOplogCollectionSizeBytes = 10 << 20 // 10Mb
	logsCollectionSizeBytes     = 50 << 20 // 50Mb
)

// setup a new DB for PBM
func (p *PBM) setupNewDB() error {
	err := p.Conn.Database(DB).RunCommand(
		p.ctx,
		bson.D{{"create", CmdStreamCollection}, {"capped", true}, {"size", cmdCollectionSizeBytes}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure cmd collection")
	}

	err = p.Conn.Database(DB).RunCommand(
		p.ctx,
		bson.D{{"create", LogCollection}, {"capped", true}, {"size", logsCollectionSizeBytes}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure log collection")
	}

	err = p.Conn.Database(DB).RunCommand(
		p.ctx,
		bson.D{{"create", LockCollection}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure lock collection")
	}

	// create indexes for the lock collections
	_, err = p.Conn.Database(DB).Collection(LockCollection).Indexes().CreateOne(
		p.ctx,
		mongo.IndexModel{
			Keys: bson.D{{"replset", 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true),
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrapf(err, "ensure lock index on %s", LockCollection)
	}
	_, err = p.Conn.Database(DB).Collection(LockOpCollection).Indexes().CreateOne(
		p.ctx,
		mongo.IndexModel{
			Keys: bson.D{{"replset", 1}, {"type", 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true),
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrapf(err, "ensure lock index on %s", LockOpCollection)
	}

	err = p.Conn.Database(DB).RunCommand(
		p.ctx,
		bson.D{{"create", PBMOpLogCollection}, {"capped", true}, {"size", pbmOplogCollectionSizeBytes}},
	).Err()
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure log collection")
	}
	_, err = p.Conn.Database(DB).Collection(PBMOpLogCollection).Indexes().CreateOne(
		p.ctx,
		mongo.IndexModel{
			Keys: bson.D{{"opid", 1}, {"replset", 1}},
			Options: options.Index().
				SetUnique(true).
				SetSparse(true),
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrapf(err, "ensure lock index on %s", LockOpCollection)
	}

	// create indexs for the pitr cunks
	_, err = p.Conn.Database(DB).Collection(PITRChunksCollection).Indexes().CreateMany(
		p.ctx,
		[]mongo.IndexModel{
			{
				Keys: bson.D{{"rs", 1}, {"start_ts", 1}, {"end_ts", 1}},
				Options: options.Index().
					SetUnique(true).
					SetSparse(true),
			},
			{
				Keys: bson.D{{"start_ts", 1}, {"end_ts", 1}},
			},
		},
	)
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		return errors.Wrap(err, "ensure pitr chunks index")
	}

	_, err = p.Conn.Database(DB).Collection(BcpCollection).Indexes().CreateMany(
		p.ctx,
		[]mongo.IndexModel{
			{
				Keys: bson.D{{"name", 1}},
				Options: options.Index().
					SetUnique(true).
					SetSparse(true),
			},
			{
				Keys: bson.D{{"start_ts", 1}, {"status", 1}},
			},
		},
	)

	return nil
}

func connect(ctx context.Context, uri, appName string) (*mongo.Client, error) {
	client, err := mongo.NewClient(
		options.Client().ApplyURI(uri).
			SetAppName(appName).
			SetReadPreference(readpref.Primary()).
			SetReadConcern(readconcern.Majority()).
			SetWriteConcern(writeconcern.New(writeconcern.WMajority())),
	)
	if err != nil {
		return nil, errors.Wrap(err, "create mongo client")
	}
	err = client.Connect(ctx)
	if err != nil {
		return nil, errors.Wrap(err, "mongo connect")
	}

	err = client.Ping(ctx, nil)
	if err != nil {
		return nil, errors.Wrap(err, "mongo ping")
	}

	return client, nil
}

// BackupMeta is a backup's metadata
type BackupMeta struct {
	OPID             string               `bson:"opid" json:"opid"`
	Name             string               `bson:"name" json:"name"`
	Replsets         []BackupReplset      `bson:"replsets" json:"replsets"`
	Compression      CompressionType      `bson:"compression" json:"compression"`
	Store            StorageConf          `bson:"store" json:"store"`
	MongoVersion     string               `bson:"mongodb_version" json:"mongodb_version,omitempty"`
	StartTS          int64                `bson:"start_ts" json:"start_ts"`
	LastTransitionTS int64                `bson:"last_transition_ts" json:"last_transition_ts"`
	FirstWriteTS     primitive.Timestamp  `bson:"first_write_ts" json:"first_write_ts"`
	LastWriteTS      primitive.Timestamp  `bson:"last_write_ts" json:"last_write_ts"`
	Hb               primitive.Timestamp  `bson:"hb" json:"hb"`
	Status           Status               `bson:"status" json:"status"`
	Conditions       []Condition          `bson:"conditions" json:"conditions"`
	Nomination       []BackupRsNomination `bson:"n" json:"n"`
	Error            string               `bson:"error,omitempty" json:"error,omitempty"`
	PBMVersion       string               `bson:"pbm_version,omitempty" json:"pbm_version,omitempty"`
	BalancerStatus   BalancerMode         `bson:"balancer" json:"balancer"`
}

// BackupRsNomination is used to choose (nominate and elect) nodes for the backup
// within a replica set
type BackupRsNomination struct {
	RS    string   `bson:"rs" json:"rs"`
	Nodes []string `bson:"n" json:"n"`
	Ack   string   `bson:"ack" json:"ack"`
}

type Condition struct {
	Timestamp int64  `bson:"timestamp" json:"timestamp"`
	Status    Status `bson:"status" json:"status"`
	Error     string `bson:"error,omitempty" json:"error,omitempty"`
}

type BackupReplset struct {
	Name             string              `bson:"name" json:"name"`
	DumpName         string              `bson:"dump_name" json:"backup_name" `
	OplogName        string              `bson:"oplog_name" json:"oplog_name"`
	StartTS          int64               `bson:"start_ts" json:"start_ts"`
	Status           Status              `bson:"status" json:"status"`
	LastTransitionTS int64               `bson:"last_transition_ts" json:"last_transition_ts"`
	FirstWriteTS     primitive.Timestamp `bson:"first_write_ts" json:"first_write_ts"`
	LastWriteTS      primitive.Timestamp `bson:"last_write_ts" json:"last_write_ts"`
	Error            string              `bson:"error,omitempty" json:"error,omitempty"`
	Conditions       []Condition         `bson:"conditions" json:"conditions"`
}

// Status is a backup current status
type Status string

const (
	StatusStarting  Status = "starting"
	StatusRunning   Status = "running"
	StatusDumpDone  Status = "dumpDone"
	StatusDone      Status = "done"
	StatusCancelled Status = "canceled"
	StatusError     Status = "error"
)

func (p *PBM) SetBackupMeta(m *BackupMeta) error {
	m.LastTransitionTS = m.StartTS
	m.Conditions = append(m.Conditions, Condition{
		Timestamp: m.StartTS,
		Status:    m.Status,
	})

	_, err := p.Conn.Database(DB).Collection(BcpCollection).InsertOne(p.ctx, m)

	return err
}

// RS returns the metada of the replset with given name.
// It returns nil if no replsent found.
func (b *BackupMeta) RS(name string) *BackupReplset {
	for _, rs := range b.Replsets {
		if rs.Name == name {
			return &rs
		}
	}
	return nil
}

func (p *PBM) ChangeBackupStateOPID(opid string, s Status, msg string) error {
	return p.changeBackupState(bson.D{{"opid", opid}}, s, msg)
}
func (p *PBM) ChangeBackupState(bcpName string, s Status, msg string) error {
	return p.changeBackupState(bson.D{{"name", bcpName}}, s, msg)
}
func (p *PBM) changeBackupState(clause bson.D, s Status, msg string) error {
	ts := time.Now().UTC().Unix()
	_, err := p.Conn.Database(DB).Collection(BcpCollection).UpdateOne(
		p.ctx,
		clause,
		bson.D{
			{"$set", bson.M{"status": s}},
			{"$set", bson.M{"last_transition_ts": ts}},
			{"$set", bson.M{"error": msg}},
			{"$push", bson.M{"conditions": Condition{Timestamp: ts, Status: s, Error: msg}}},
		},
	)

	return err
}

func (p *PBM) BackupHB(bcpName string) error {
	ts, err := p.ClusterTime()
	if err != nil {
		return errors.Wrap(err, "read cluster time")
	}

	_, err = p.Conn.Database(DB).Collection(BcpCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", bcpName}},
		bson.D{
			{"$set", bson.M{"hb": ts}},
		},
	)

	return errors.Wrap(err, "write into db")
}

func (p *PBM) SetFirstLastWrite(bcpName string, first, last primitive.Timestamp) error {
	_, err := p.Conn.Database(DB).Collection(BcpCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", bcpName}},
		bson.D{
			{"$set", bson.M{"first_write_ts": first}},
			{"$set", bson.M{"last_write_ts": last}},
		},
	)

	return err
}

func (p *PBM) AddRSMeta(bcpName string, rs BackupReplset) error {
	rs.LastTransitionTS = rs.StartTS
	rs.Conditions = append(rs.Conditions, Condition{
		Timestamp: rs.StartTS,
		Status:    rs.Status,
	})
	_, err := p.Conn.Database(DB).Collection(BcpCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", bcpName}},
		bson.D{{"$addToSet", bson.M{"replsets": rs}}},
	)

	return err
}

func (p *PBM) ChangeRSState(bcpName string, rsName string, s Status, msg string) error {
	ts := time.Now().UTC().Unix()
	_, err := p.Conn.Database(DB).Collection(BcpCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", bcpName}, {"replsets.name", rsName}},
		bson.D{
			{"$set", bson.M{"replsets.$.status": s}},
			{"$set", bson.M{"replsets.$.last_transition_ts": ts}},
			{"$set", bson.M{"replsets.$.error": msg}},
			{"$push", bson.M{"replsets.$.conditions": Condition{Timestamp: ts, Status: s, Error: msg}}},
		},
	)

	return err
}

func (p *PBM) SetRSFirstWrite(bcpName string, rsName string, ts primitive.Timestamp) error {
	_, err := p.Conn.Database(DB).Collection(BcpCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", bcpName}, {"replsets.name", rsName}},
		bson.D{
			{"$set", bson.M{"replsets.$.first_write_ts": ts}},
		},
	)

	return err
}

func (p *PBM) SetRSLastWrite(bcpName string, rsName string, ts primitive.Timestamp) error {
	_, err := p.Conn.Database(DB).Collection(BcpCollection).UpdateOne(
		p.ctx,
		bson.D{{"name", bcpName}, {"replsets.name", rsName}},
		bson.D{
			{"$set", bson.M{"replsets.$.last_write_ts": ts}},
		},
	)

	return err
}

func (p *PBM) GetBackupMeta(name string) (*BackupMeta, error) {
	return p.getBackupMeta(bson.D{{"name", name}})
}

func (p *PBM) GetBackupByOPID(opid string) (*BackupMeta, error) {
	return p.getBackupMeta(bson.D{{"opid", opid}})
}

func (p *PBM) getBackupMeta(clause bson.D) (*BackupMeta, error) {
	res := p.Conn.Database(DB).Collection(BcpCollection).FindOne(p.ctx, clause)
	if res.Err() != nil {
		if res.Err() == mongo.ErrNoDocuments {
			return nil, ErrNotFound
		}
		return nil, errors.Wrap(res.Err(), "get")
	}

	b := &BackupMeta{}
	err := res.Decode(b)
	return b, errors.Wrap(err, "decode")
}

// GetFirstBackup returns first successfully finished backup
func (p *PBM) GetFirstBackup() (*BackupMeta, error) {
	return p.getRecentBackup(nil, 1)
}

// GetLastBackup returns last successfully finished backup
// and nil if there is no such backup yet. If ts isn't nil it will
// search for the most recent backup that finished before specified timestamp
func (p *PBM) GetLastBackup(before *primitive.Timestamp) (*BackupMeta, error) {
	return p.getRecentBackup(before, -1)
}

func (p *PBM) getRecentBackup(before *primitive.Timestamp, sort int) (*BackupMeta, error) {
	q := bson.D{{"status", StatusDone}}
	if before != nil {
		q = append(q, bson.E{"last_write_ts", bson.M{"$lte": before}})
	}

	res := p.Conn.Database(DB).Collection(BcpCollection).FindOne(
		p.ctx,
		q,
		options.FindOne().SetSort(bson.D{{"start_ts", sort}}),
	)
	if res.Err() != nil {
		if res.Err() == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrap(res.Err(), "get")
	}

	b := new(BackupMeta)
	err := res.Decode(b)
	return b, errors.Wrap(err, "decode")
}

func (p *PBM) BackupGetNext(backup *BackupMeta) (*BackupMeta, error) {
	res := p.Conn.Database(DB).Collection(BcpCollection).FindOne(
		p.ctx,
		bson.D{
			{"start_ts", bson.M{"$gt": backup.LastWriteTS.T}},
			{"status", StatusDone},
		},
	)

	if res.Err() != nil {
		if res.Err() == mongo.ErrNoDocuments {
			return nil, nil
		}
		return nil, errors.Wrap(res.Err(), "get")
	}

	b := new(BackupMeta)
	err := res.Decode(b)
	return b, errors.Wrap(err, "decode")
}

func (p *PBM) BackupsList(limit int64) ([]BackupMeta, error) {
	cur, err := p.Conn.Database(DB).Collection(BcpCollection).Find(
		p.ctx,
		bson.M{},
		options.Find().SetLimit(limit).SetSort(bson.D{{"start_ts", -1}}),
	)
	if err != nil {
		return nil, errors.Wrap(err, "query mongo")
	}

	defer cur.Close(p.ctx)

	backups := []BackupMeta{}
	for cur.Next(p.ctx) {
		b := BackupMeta{}
		err := cur.Decode(&b)
		if err != nil {
			return nil, errors.Wrap(err, "message decode")
		}
		backups = append(backups, b)
	}

	return backups, cur.Err()
}

// ClusterMembers returns list of replicasets current cluster consists of
// (shards + configserver). The list would consist of on rs if cluster is
// a non-sharded rs. If `inf` is nil, method would request mongo to define it.
func (p *PBM) ClusterMembers(inf *NodeInfo) ([]Shard, error) {
	var err error

	if inf == nil {
		inf, err = p.GetNodeInfo()
		if err != nil {
			return nil, errors.Wrap(err, "define cluster state")
		}
	}

	shards := []Shard{{
		RS:   inf.SetName,
		Host: inf.SetName + "/" + strings.Join(inf.Hosts, ","),
	}}
	if inf.IsSharded() {
		s, err := p.GetShards()
		if err != nil {
			return nil, errors.Wrap(err, "get shards")
		}
		shards = append(shards, s...)
	}

	return shards, nil
}

// GetShards gets list of shards
func (p *PBM) GetShards() ([]Shard, error) {
	cur, err := p.Conn.Database("config").Collection("shards").Find(p.ctx, bson.M{})
	if err != nil {
		return nil, errors.Wrap(err, "query mongo")
	}

	defer cur.Close(p.ctx)

	shards := []Shard{}
	for cur.Next(p.ctx) {
		s := Shard{}
		err := cur.Decode(&s)
		if err != nil {
			return nil, errors.Wrap(err, "message decode")
		}
		s.RS = s.ID
		// _id may differ from the rs name, so extract rs name from the host (format like "rs2/localhost:27017")
		// see https://jira.percona.com/browse/PBM-595
		h := strings.Split(s.Host, "/")
		if len(h) > 1 {
			s.RS = h[0]
		}
		shards = append(shards, s)
	}

	return shards, cur.Err()
}

// Context returns object context
func (p *PBM) Context() context.Context {
	return p.ctx
}

// GetNodeInfo returns mongo node info
func (p *PBM) GetNodeInfo() (*NodeInfo, error) {
	inf := &NodeInfo{}
	err := p.Conn.Database(DB).RunCommand(p.ctx, bson.D{{"isMaster", 1}}).Decode(inf)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command")
	}
	return inf, nil
}

// ClusterTime returns mongo's current cluster time
func (p *PBM) ClusterTime() (primitive.Timestamp, error) {
	// Make a read to force the cluster timestamp update.
	// Otherwise, cluster timestamp could remain the same between node info reads, while in fact time has been moved forward.
	err := p.Conn.Database(DB).Collection(LockCollection).FindOne(p.ctx, bson.D{}).Err()
	if err != nil && err != mongo.ErrNoDocuments {
		return primitive.Timestamp{}, errors.Wrap(err, "void read")
	}

	inf, err := p.GetNodeInfo()
	if err != nil {
		return primitive.Timestamp{}, errors.Wrap(err, "get NodeInfo")
	}

	if inf.ClusterTime == nil {
		return primitive.Timestamp{}, errors.Wrap(err, "no clusterTime in response")
	}

	return inf.ClusterTime.ClusterTime, nil
}

func (p *PBM) LogGet(r *log.LogRequest, limit int64) ([]log.LogEntry, error) {
	return p.log.Get(r, limit, false)
}

func (p *PBM) LogGetExactSeverity(r *log.LogRequest, limit int64) ([]log.LogEntry, error) {
	return p.log.Get(r, limit, true)
}

// SetBalancerStatus sets balancer status
func (p *PBM) SetBalancerStatus(m BalancerMode) error {
	var cmd string

	switch m {
	case BalancerModeOn:
		cmd = "_configsvrBalancerStart"
	case BalancerModeOff:
		cmd = "_configsvrBalancerStop"
	default:
		return errors.Errorf("unknown mode %s", m)
	}

	err := p.Conn.Database("admin").RunCommand(p.ctx, bson.D{{cmd, 1}}).Err()
	if err != nil {
		return errors.Wrap(err, "run mongo command")
	}
	return nil
}

// GetBalancerStatus returns balancer status
func (p *PBM) GetBalancerStatus() (*BalancerStatus, error) {
	inf := &BalancerStatus{}
	err := p.Conn.Database("admin").RunCommand(p.ctx, bson.D{{"_configsvrBalancerStatus", 1}}).Decode(inf)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command")
	}
	return inf, nil
}

type Epoch primitive.Timestamp

func (p *PBM) GetEpoch() (Epoch, error) {
	c, err := p.GetConfig()
	if err != nil {
		return Epoch{}, errors.Wrap(err, "get config")
	}

	return Epoch(c.Epoch), nil
}

func (p *PBM) ResetEpoch() (Epoch, error) {
	ct, err := p.ClusterTime()
	if err != nil {
		return Epoch{}, errors.Wrap(err, "get cluster time")
	}
	_, err = p.Conn.Database(DB).Collection(ConfigCollection).UpdateOne(
		p.ctx,
		bson.D{},
		bson.M{"$set": bson.M{"epoch": ct}},
	)

	return Epoch(ct), err
}

func (e Epoch) TS() primitive.Timestamp {
	return primitive.Timestamp(e)
}

// FileCompression return compression alg based on given file extention
func FileCompression(ext string) CompressionType {
	switch ext {
	default:
		return CompressionTypeNone
	case "gz":
		return CompressionTypePGZIP
	case "lz4":
		return CompressionTypeLZ4
	case "snappy":
		return CompressionTypeS2
	}
}

// CopyColl copy documents matching the given filter and return number of copied documents
func CopyColl(ctx context.Context, from, to *mongo.Collection, filter interface{}) (n int, err error) {
	cur, err := from.Find(ctx, filter)
	if err != nil {
		return 0, errors.Wrap(err, "create cursor")
	}
	defer cur.Close(ctx)

	for cur.Next(ctx) {
		_, err = to.InsertOne(ctx, cur.Current)
		if err != nil {
			return 0, errors.Wrap(err, "insert document")
		}
		n++
	}

	return n, nil
}
