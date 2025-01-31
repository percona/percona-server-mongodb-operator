package topo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/version"
)

// ReplsetRole is a replicaset role in sharded cluster
type ReplsetRole string

const (
	RoleUnknown   ReplsetRole = "unknown"
	RoleShard     ReplsetRole = "shard"
	RoleConfigSrv ReplsetRole = "configsrv"
)

type NodeRole string

const (
	RolePrimary   NodeRole = "P"
	RoleSecondary NodeRole = "S"
	RoleArbiter   NodeRole = "A"
	RoleHidden    NodeRole = "H"
	RoleDelayed   NodeRole = "D"
)

type OpTime struct {
	TS   primitive.Timestamp `bson:"ts" json:"ts"`
	Term int64               `bson:"t" json:"t"`
}

// MongoLastWrite represents the last write to the MongoDB server
type MongoLastWrite struct {
	OpTime            OpTime    `bson:"opTime"`
	LastWriteDate     time.Time `bson:"lastWriteDate"`
	MajorityOpTime    OpTime    `bson:"majorityOpTime"`
	MajorityWriteDate time.Time `bson:"majorityWriteDate"`
}

type ClusterTime struct {
	ClusterTime primitive.Timestamp `bson:"clusterTime"`
	Signature   struct {
		Hash  primitive.Binary `bson:"hash"`
		KeyID int64            `bson:"keyId"`
	} `bson:"signature"`
}

type ConfigServerState struct {
	OpTime *OpTime `bson:"opTime"`
}

type NodeBrief struct {
	URI       string
	SetName   string
	Me        string
	Sharded   bool
	ConfigSvr bool
	Version   version.MongoVersion
}

// NodeInfo represents the mongo's node info
type NodeInfo struct {
	Hosts                        []string             `bson:"hosts,omitempty"`
	Msg                          string               `bson:"msg"`
	MaxBsonObjectSise            int64                `bson:"maxBsonObjectSize"`
	MaxMessageSizeBytes          int64                `bson:"maxMessageSizeBytes"`
	MaxWriteBatchSize            int64                `bson:"maxWriteBatchSize"`
	LocalTime                    time.Time            `bson:"localTime"`
	LogicalSessionTimeoutMinutes int64                `bson:"logicalSessionTimeoutMinutes"`
	MaxWireVersion               int64                `bson:"maxWireVersion"`
	MinWireVersion               int64                `bson:"minWireVersion"`
	OK                           int                  `bson:"ok"`
	SetName                      string               `bson:"setName,omitempty"`
	Primary                      string               `bson:"primary,omitempty"`
	SetVersion                   int32                `bson:"setVersion,omitempty"`
	IsPrimary                    bool                 `bson:"ismaster"`
	Secondary                    bool                 `bson:"secondary,omitempty"`
	Hidden                       bool                 `bson:"hidden,omitempty"`
	Passive                      bool                 `bson:"passive,omitempty"`
	ArbiterOnly                  bool                 `bson:"arbiterOnly"`
	SecondaryDelayOld            int32                `bson:"slaveDelay"`
	SecondaryDelaySecs           int32                `bson:"secondaryDelaySecs"`
	ConfigSvr                    int                  `bson:"configsvr,omitempty"`
	Me                           string               `bson:"me"`
	LastWrite                    MongoLastWrite       `bson:"lastWrite"`
	ClusterTime                  *ClusterTime         `bson:"$clusterTime,omitempty"`
	ConfigServerState            *ConfigServerState   `bson:"$configServerState,omitempty"`
	OperationTime                *primitive.Timestamp `bson:"operationTime,omitempty"`
	Opts                         MongodOpts           `bson:"-"`
}

func (i *NodeInfo) IsDelayed() bool {
	return i.SecondaryDelayOld != 0 || i.SecondaryDelaySecs != 0
}

// IsSharded returns true is replset is part sharded cluster
func (i *NodeInfo) IsMongos() bool {
	return i.Msg == "isdbgrid"
}

// IsSharded returns true is replset is part sharded cluster
func (i *NodeInfo) IsSharded() bool {
	return i.SetName != "" && (i.ConfigServerState != nil || i.Opts.Sharding.ClusterRole != "" || i.ConfigSvr == 2)
}

// IsLeader returns true if node can act as backup leader (it's configsrv or non shareded rs)
func (i *NodeInfo) IsLeader() bool {
	return !i.IsSharded() || i.ReplsetRole() == RoleConfigSrv
}

// IsConfigSrv returns true if node belongs to the CSRS in a sharded cluster
func (i *NodeInfo) IsConfigSrv() bool {
	return i.IsSharded() && i.ReplsetRole() == RoleConfigSrv
}

// IsClusterLeader - cluster leader is a primary node on configsrv
// or just primary node in non-sharded replicaset
func (i *NodeInfo) IsClusterLeader() bool {
	return i.IsPrimary && i.Me == i.Primary && i.IsLeader()
}

// ReplsetRole returns replset role in sharded clister
func (i *NodeInfo) ReplsetRole() ReplsetRole {
	switch {
	case i.ConfigSvr == 2:
		return RoleConfigSrv
	case i.ConfigServerState != nil:
		return RoleShard
	default:
		return RoleUnknown
	}
}

// IsStandalone returns true if node is not a part of replica set
func (i *NodeInfo) IsStandalone() bool {
	return i.SetName == ""
}

type MongodOpts struct {
	Net struct {
		BindIP string `bson:"bindIp" json:"bindIp" yaml:"bindIp"`
		Port   int    `bson:"port" json:"port" yaml:"port"`
	} `bson:"net" json:"net"`
	Sharding struct {
		ClusterRole string `bson:"clusterRole" json:"clusterRole" yaml:"-"`
	} `bson:"sharding" json:"sharding" yaml:"-"`
	Storage  MongodOptsStorage `bson:"storage" json:"storage" yaml:"storage"`
	Security *MongodOptsSec    `bson:"security,omitempty" json:"security,omitempty" yaml:"security,omitempty"`
}

//nolint:lll
type MongodOptsSec struct {
	EnableEncryption     *bool   `bson:"enableEncryption,omitempty" json:"enableEncryption,omitempty" yaml:"enableEncryption,omitempty"`
	EncryptionCipherMode *string `bson:"encryptionCipherMode,omitempty" json:"encryptionCipherMode,omitempty" yaml:"encryptionCipherMode,omitempty"`
	EncryptionKeyFile    *string `bson:"encryptionKeyFile,omitempty" json:"encryptionKeyFile,omitempty" yaml:"encryptionKeyFile,omitempty"`
	RelaxPermChecks      *bool   `bson:"relaxPermChecks,omitempty" json:"relaxPermChecks,omitempty" yaml:"relaxPermChecks,omitempty"`
	Vault                *struct {
		ServerName           *string `bson:"serverName,omitempty" json:"serverName,omitempty" yaml:"serverName,omitempty"`
		Port                 *int    `bson:"port,omitempty" json:"port,omitempty" yaml:"port,omitempty"`
		TokenFile            *string `bson:"tokenFile,omitempty" json:"tokenFile,omitempty" yaml:"tokenFile,omitempty"`
		Secret               *string `bson:"secret,omitempty" json:"secret,omitempty" yaml:"secret,omitempty"`
		ServerCAFile         *string `bson:"serverCAFile,omitempty" json:"serverCAFile,omitempty" yaml:"serverCAFile,omitempty"`
		SecretVersion        *uint32 `bson:"secretVersion,omitempty" json:"secretVersion,omitempty" yaml:"secretVersion,omitempty"`
		DisableTLSForTesting *bool   `bson:"disableTLSForTesting,omitempty" json:"disableTLSForTesting,omitempty" yaml:"disableTLSForTesting,omitempty"`
	} `bson:"vault,omitempty" json:"vault,omitempty" yaml:"vault,omitempty"`
	KMIP *struct {
		ServerName                *string `bson:"serverName,omitempty" json:"serverName,omitempty" yaml:"serverName,omitempty"`
		Port                      *int    `bson:"port,omitempty" json:"port,omitempty" yaml:"port,omitempty"`
		ClientCertificateFile     *string `bson:"clientCertificateFile,omitempty" json:"clientCertificateFile,omitempty" yaml:"clientCertificateFile,omitempty"`
		ClientKeyFile             *string `bson:"clientKeyFile,omitempty" json:"clientKeyFile,omitempty" yaml:"clientKeyFile,omitempty"`
		ServerCAFile              *string `bson:"serverCAFile,omitempty" json:"serverCAFile,omitempty" yaml:"serverCAFile,omitempty"`
		KeyIdentifier             *string `bson:"keyIdentifier,omitempty" json:"keyIdentifier,omitempty" yaml:"keyIdentifier,omitempty"`
		ClientCertificatePassword *string `bson:"clientCertificatePassword,omitempty" json:"-" yaml:"clientCertificatePassword,omitempty"`
	} `bson:"kmip,omitempty" json:"kmip,omitempty" yaml:"kmip,omitempty"`
}

type ExternOpts map[string]MongodOpts

type MongodOptsStorage struct {
	DirectoryPerDB bool   `bson:"directoryPerDB" json:"directoryPerDB" yaml:"directoryPerDB"`
	DBpath         string `bson:"dbPath" json:"dbPath" yaml:"dbPath"`
	WiredTiger     struct {
		EngineConfig struct {
			JournalCompressor   string `bson:"journalCompressor" json:"journalCompressor" yaml:"journalCompressor"`
			DirectoryForIndexes bool   `bson:"directoryForIndexes" json:"directoryForIndexes" yaml:"directoryForIndexes"`
		} `bson:"engineConfig" json:"engineConfig" yaml:"engineConfig"`
		CollectionConfig struct {
			BlockCompressor string `bson:"blockCompressor" json:"blockCompressor" yaml:"blockCompressor"`
		} `bson:"collectionConfig" json:"collectionConfig" yaml:"collectionConfig"`
		IndexConfig struct {
			PrefixCompression bool `bson:"prefixCompression" json:"prefixCompression" yaml:"prefixCompression"`
		} `bson:"indexConfig" json:"indexConfig" yaml:"indexConfig"`
	} `bson:"wiredTiger" json:"wiredTiger" yaml:"wiredTiger"`
}

// NewMongodOptsStorage return MongodOptsStorage with default settings
func NewMongodOptsStorage() *MongodOptsStorage {
	m := &MongodOptsStorage{}
	m.setDefaults()
	return m
}

func (stg *MongodOptsStorage) setDefaults() {
	stg.DBpath = "/data/db"
	stg.WiredTiger.EngineConfig.JournalCompressor = "snappy"
	stg.WiredTiger.CollectionConfig.BlockCompressor = "snappy"
	stg.WiredTiger.IndexConfig.PrefixCompression = true
}

func (stg *MongodOptsStorage) UnmarshalYAML(unmarshal func(interface{}) error) error {
	stg.setDefaults()
	type rawStg MongodOptsStorage
	return unmarshal((*rawStg)(stg))
}

// GetNodeInfoExt returns mongo node info with mongod options
func GetNodeInfoExt(ctx context.Context, m *mongo.Client) (*NodeInfo, error) {
	i, err := GetNodeInfo(ctx, m)
	if err != nil {
		return nil, errors.Wrap(err, "get NodeInfo")
	}
	opts, err := GetMongodOpts(ctx, m, nil)
	if err != nil {
		return nil, errors.Wrap(err, "get mongod options")
	}
	if opts != nil {
		i.Opts = *opts
	}
	return i, nil
}

func GetNodeInfo(ctx context.Context, m *mongo.Client) (*NodeInfo, error) {
	res := m.Database(defs.DB).RunCommand(ctx, bson.D{{"isMaster", 1}})
	if err := res.Err(); err != nil {
		return nil, errors.Wrap(err, "cmd: isMaster")
	}

	n := &NodeInfo{}
	err := res.Decode(&n)
	return n, errors.Wrap(err, "decode")
}

func GetMongodOpts(ctx context.Context, m *mongo.Client, defaults *MongodOpts) (*MongodOpts, error) {
	opts := struct {
		Parsed MongodOpts `bson:"parsed" json:"parsed"`
	}{}
	if defaults != nil {
		opts.Parsed = *defaults
	}
	err := m.Database("admin").RunCommand(ctx, bson.D{{"getCmdLineOpts", 1}}).Decode(&opts)
	if err != nil {
		return nil, errors.Wrap(err, "run mongo command")
	}
	return &opts.Parsed, nil
}

//nolint:lll
type RSConfig struct {
	ID                      string     `bson:"_id" json:"_id"`
	CSRS                    bool       `bson:"configsvr,omitempty" json:"configsvr"`
	Protocol                int64      `bson:"protocolVersion,omitempty" json:"protocolVersion"`
	Version                 int        `bson:"version" json:"version"`
	Members                 []RSMember `bson:"members" json:"members"`
	WConcernMajorityJournal bool       `bson:"writeConcernMajorityJournalDefault,omitempty" json:"writeConcernMajorityJournalDefault"`
	Settings                struct {
		ChainingAllowed         bool `bson:"chainingAllowed,omitempty" json:"chainingAllowed"`
		HeartbeatIntervalMillis int  `bson:"heartbeatIntervalMillis,omitempty" json:"heartbeatIntervalMillis"`
		HeartbeatTimeoutSecs    int  `bson:"heartbeatTimeoutSecs,omitempty" json:"heartbeatTimeoutSecs"`
		ElectionTimeoutMillis   int  `bson:"electionTimeoutMillis,omitempty" json:"electionTimeoutMillis"`
		CatchUpTimeoutMillis    int  `bson:"catchUpTimeoutMillis,omitempty" json:"catchUpTimeoutMillis"`
	} `bson:"settings,omitempty" json:"settings"`
}

type RSMember struct {
	ID                 int               `bson:"_id" json:"_id"`
	Host               string            `bson:"host" json:"host"`
	ArbiterOnly        bool              `bson:"arbiterOnly" json:"arbiterOnly"`
	BuildIndexes       bool              `bson:"buildIndexes" json:"buildIndexes"`
	Hidden             bool              `bson:"hidden" json:"hidden"`
	Priority           float64           `bson:"priority" json:"priority"`
	Tags               map[string]string `bson:"tags,omitempty" json:"tags"`
	SecondaryDelayOld  int64             `bson:"slaveDelay,omitempty"`
	SecondaryDelaySecs int64             `bson:"secondaryDelaySecs,omitempty"`
	Votes              int               `bson:"votes" json:"votes"`
}

func (m *RSMember) IsDelayed() bool {
	return m.SecondaryDelayOld != 0 || m.SecondaryDelaySecs != 0
}

func (m *RSMember) Role() NodeRole {
	switch {
	case m.ArbiterOnly:
		return RoleArbiter
	case m.IsDelayed():
		return RoleDelayed
	case m.Hidden:
		return RoleHidden
	}

	return ""
}

func GetReplSetConfig(ctx context.Context, m *mongo.Client) (*RSConfig, error) {
	res := m.Database("admin").RunCommand(ctx, bson.D{{"replSetGetConfig", 1}})
	if err := res.Err(); err != nil {
		return nil, errors.Wrap(err, "run command")
	}

	val := struct{ Config *RSConfig }{}
	if err := res.Decode(&val); err != nil {
		return nil, errors.Wrap(err, "decode")
	}

	return val.Config, nil
}

func ConfSvrConn(ctx context.Context, cn *mongo.Client) (string, error) {
	csvr := struct {
		URI string `bson:"configsvrConnectionString"`
	}{}
	err := cn.Database("admin").Collection("system.version").
		FindOne(ctx, bson.D{{"_id", "shardIdentity"}}).Decode(&csvr)

	return csvr.URI, err
}
