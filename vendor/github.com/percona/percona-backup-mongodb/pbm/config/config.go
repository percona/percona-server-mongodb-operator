package config

import (
	"context"
	"fmt"
	"io"
	"maps"
	"os"
	"reflect"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/x/bsonx/bsoncore"
	"gopkg.in/yaml.v2"

	"github.com/percona/percona-backup-mongodb/pbm/compress"
	"github.com/percona/percona-backup-mongodb/pbm/connect"
	"github.com/percona/percona-backup-mongodb/pbm/defs"
	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
	"github.com/percona/percona-backup-mongodb/pbm/topo"
)

var (
	ErrUnkownStorageType   = errors.New("unknown storage type")
	ErrMissedConfig        = errors.New("missed config")
	ErrMissedConfigProfile = errors.New("missed config profile")
	ErrUnsetConfigPath     = bsoncore.ErrElementNotFound
)

type confMap map[string]reflect.Kind

// _confmap is a list of config's valid keys and its types
var _confmap confMap = keys(reflect.TypeOf(Config{}))

func keys(t reflect.Type) confMap {
	v := make(confMap)
	for i := 0; i < t.NumField(); i++ {
		name := strings.TrimSpace(strings.Split(t.Field(i).Tag.Get("bson"), ",")[0])

		typ := t.Field(i).Type
		if typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
		if typ.Kind() == reflect.Struct {
			for n, t := range keys(typ) {
				v[name+"."+n] = t
			}
		} else {
			v[name] = typ.Kind()
		}
	}
	return v
}

// validateConfigKey checks if a config key valid
func validateConfigKey(k string) bool {
	_, ok := _confmap[k]
	return ok
}

// Config is a pbm config
type Config struct {
	Name      string `bson:"name,omitempty" json:"name,omitempty" yaml:"name,omitempty"`
	IsProfile bool   `bson:"profile,omitempty" json:"profile,omitempty" yaml:"profile,omitempty"`

	Storage StorageConf  `bson:"storage" json:"storage" yaml:"storage"`
	PITR    *PITRConf    `bson:"pitr,omitempty" json:"pitr,omitempty" yaml:"pitr,omitempty"`
	Backup  *BackupConf  `bson:"backup,omitempty" json:"backup,omitempty" yaml:"backup,omitempty"`
	Restore *RestoreConf `bson:"restore,omitempty" json:"restore,omitempty" yaml:"restore,omitempty"`

	Epoch primitive.Timestamp `bson:"epoch" json:"-" yaml:"-"`
}

func Parse(r io.Reader) (*Config, error) {
	cfg := &Config{}

	dec := yaml.NewDecoder(r)
	dec.SetStrict(true)
	err := dec.Decode(cfg)
	if err != nil {
		return nil, errors.Wrap(err, "decode")
	}

	err = cfg.Storage.Cast()
	if err != nil {
		return nil, errors.Wrap(err, "storage cast")
	}

	return cfg, nil
}

func (c *Config) Clone() *Config {
	if c == nil {
		return nil
	}

	rv := &Config{
		Name:      c.Name,
		IsProfile: c.IsProfile,
		Storage:   *c.Storage.Clone(),
		PITR:      c.PITR.Clone(),
		Restore:   c.Restore.Clone(),
		Backup:    c.Backup.Clone(),
		Epoch:     c.Epoch,
	}

	return rv
}

func (c *Config) String() string {
	c = c.Clone()

	if c.Storage.S3 != nil {
		if c.Storage.S3.Credentials.AccessKeyID != "" {
			c.Storage.S3.Credentials.AccessKeyID = "***"
		}
		if c.Storage.S3.Credentials.SecretAccessKey != "" {
			c.Storage.S3.Credentials.SecretAccessKey = "***"
		}
		if c.Storage.S3.Credentials.SessionToken != "" {
			c.Storage.S3.Credentials.SessionToken = "***"
		}
		if c.Storage.S3.Credentials.Vault.Secret != "" {
			c.Storage.S3.Credentials.Vault.Secret = "***"
		}
		if c.Storage.S3.Credentials.Vault.Token != "" {
			c.Storage.S3.Credentials.Vault.Token = "***"
		}
		if c.Storage.S3.ServerSideEncryption != nil &&
			c.Storage.S3.ServerSideEncryption.SseCustomerKey != "" {
			c.Storage.S3.ServerSideEncryption.SseCustomerKey = "***"
		}
	}
	if c.Storage.Azure != nil {
		if c.Storage.Azure.Credentials.Key != "" {
			c.Storage.Azure.Credentials.Key = "***"
		}
	}

	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Sprintln("error:", err)
	}

	return string(b)
}

// OplogSlicerInterval returns interval for general oplog slicer routine.
// If it is not configured, the function returns default (hardcoded) value 10 mins.
func (c *Config) OplogSlicerInterval() time.Duration {
	if c.PITR == nil || c.PITR.OplogSpanMin == 0 {
		return defs.DefaultPITRInterval
	}

	return time.Duration(c.PITR.OplogSpanMin * float64(time.Minute))
}

// BackupSlicerInterval returns interval for backup slicer routine.
// If it is not confugured, the function returns general oplog slicer interval.
func (c *Config) BackupSlicerInterval() time.Duration {
	if c.Backup == nil || c.Backup.OplogSpanMin == 0 {
		return c.OplogSlicerInterval()
	}

	return time.Duration(c.Backup.OplogSpanMin * float64(time.Minute))
}

// Priority contains priority values for cluster members.
// It is used for specifying Backup and PITR configuration priorities.
type Priority map[string]float64

// PITRConf is a Point-In-Time Recovery options
//
//nolint:lll
type PITRConf struct {
	Enabled          bool                     `bson:"enabled" json:"enabled" yaml:"enabled"`
	OplogSpanMin     float64                  `bson:"oplogSpanMin,omitempty" json:"oplogSpanMin,omitempty" yaml:"oplogSpanMin,omitempty"`
	OplogOnly        bool                     `bson:"oplogOnly,omitempty" json:"oplogOnly,omitempty" yaml:"oplogOnly,omitempty"`
	Priority         Priority                 `bson:"priority,omitempty" json:"priority,omitempty" yaml:"priority,omitempty"`
	Compression      compress.CompressionType `bson:"compression,omitempty" json:"compression,omitempty" yaml:"compression,omitempty"`
	CompressionLevel *int                     `bson:"compressionLevel,omitempty" json:"compressionLevel,omitempty" yaml:"compressionLevel,omitempty"`
}

func (cfg *PITRConf) Clone() *PITRConf {
	if cfg == nil {
		return nil
	}

	rv := *cfg
	rv.Priority = maps.Clone(cfg.Priority)
	if cfg.CompressionLevel != nil {
		a := *cfg.CompressionLevel
		rv.CompressionLevel = &a
	}

	return &rv
}

// StorageConf is a configuration of the backup storage
type StorageConf struct {
	Type       storage.Type  `bson:"type" json:"type" yaml:"type"`
	S3         *s3.Config    `bson:"s3,omitempty" json:"s3,omitempty" yaml:"s3,omitempty"`
	Azure      *azure.Config `bson:"azure,omitempty" json:"azure,omitempty" yaml:"azure,omitempty"`
	Filesystem *fs.Config    `bson:"filesystem,omitempty" json:"filesystem,omitempty" yaml:"filesystem,omitempty"`
}

func (s *StorageConf) Clone() *StorageConf {
	if s == nil {
		return nil
	}

	rv := &StorageConf{
		Type: s.Type,
	}

	switch s.Type {
	case storage.Filesystem:
		rv.Filesystem = s.Filesystem.Clone()
	case storage.S3:
		rv.S3 = s.S3.Clone()
	case storage.Azure:
		rv.Azure = s.Azure.Clone()
	case storage.Blackhole: // no config
	}

	return rv
}

func (s *StorageConf) Equal(other *StorageConf) bool {
	if s.Type != other.Type {
		return false
	}

	switch s.Type {
	case storage.S3:
		return s.S3.Equal(other.S3)
	case storage.Azure:
		return s.Azure.Equal(other.Azure)
	case storage.Filesystem:
		return s.Filesystem.Equal(other.Filesystem)
	case storage.Blackhole:
		return true
	}

	return false
}

func (s *StorageConf) Cast() error {
	switch s.Type {
	case storage.Filesystem:
		return s.Filesystem.Cast()
	case storage.S3:
		return s.S3.Cast()
	case storage.Azure: // noop
		return nil
	case storage.Blackhole: // noop
		return nil
	}

	return errors.Wrap(ErrUnkownStorageType, string(s.Type))
}

func (s *StorageConf) Typ() string {
	switch s.Type {
	case storage.S3:
		return "S3"
	case storage.Azure:
		return "Azure"
	case storage.Filesystem:
		return "FS"
	case storage.Blackhole:
		return "blackhole"
	case storage.Undefined:
		fallthrough
	default:
		return "Unknown"
	}
}

func (s *StorageConf) Path() string {
	path := ""
	switch s.Type {
	case storage.S3:
		path = s.S3.EndpointURL
		if !strings.Contains(path, "://") {
			path = "s3://" + path
		}
		path += "/" + s.S3.Bucket
		if s.S3.Prefix != "" {
			path += "/" + s.S3.Prefix
		}
	case storage.Azure:
		epURL := s.Azure.EndpointURL
		if epURL == "" {
			epURL = fmt.Sprintf(azure.BlobURL, s.Azure.Account)
		}
		path = epURL + "/" + s.Azure.Container
		if s.Azure.Prefix != "" {
			path += "/" + s.Azure.Prefix
		}
	case storage.Filesystem:
		path = s.Filesystem.Path
	}

	return path
}

// RestoreConf is config options for the restore
//
//nolint:lll
type RestoreConf struct {
	// Logical restore
	//
	// num of documents to buffer
	BatchSize              int `bson:"batchSize" json:"batchSize,omitempty" yaml:"batchSize,omitempty"`
	NumInsertionWorkers    int `bson:"numInsertionWorkers" json:"numInsertionWorkers,omitempty" yaml:"numInsertionWorkers,omitempty"`
	NumParallelCollections int `bson:"numParallelCollections" json:"numParallelCollections,omitempty" yaml:"numParallelCollections,omitempty"`

	// NumDownloadWorkers sets the num of goroutine would be requesting chunks
	// during the download. By default, it's set to GOMAXPROCS.
	NumDownloadWorkers int `bson:"numDownloadWorkers" json:"numDownloadWorkers,omitempty" yaml:"numDownloadWorkers,omitempty"`
	// MaxDownloadBufferMb sets the max size of the in-memory buffer that is used
	// to download files from the storage.
	MaxDownloadBufferMb int `bson:"maxDownloadBufferMb" json:"maxDownloadBufferMb,omitempty" yaml:"maxDownloadBufferMb,omitempty"`
	DownloadChunkMb     int `bson:"downloadChunkMb" json:"downloadChunkMb,omitempty" yaml:"downloadChunkMb,omitempty"`

	// MongodLocation sets the location of mongod used for internal runs during
	// physical restore. Will try $PATH/mongod if not set.
	MongodLocation    string            `bson:"mongodLocation" json:"mongodLocation,omitempty" yaml:"mongodLocation,omitempty"`
	MongodLocationMap map[string]string `bson:"mongodLocationMap" json:"mongodLocationMap,omitempty" yaml:"mongodLocationMap,omitempty"`
}

func (cfg *RestoreConf) Clone() *RestoreConf {
	if cfg == nil {
		return nil
	}

	rv := *cfg
	if len(cfg.MongodLocationMap) != 0 {
		rv.MongodLocationMap = make(map[string]string, len(cfg.MongodLocationMap))
		for k, v := range cfg.MongodLocationMap {
			rv.MongodLocationMap[k] = v
		}
	}

	return &rv
}

//nolint:lll
type BackupConf struct {
	OplogSpanMin     float64                  `bson:"oplogSpanMin" json:"oplogSpanMin" yaml:"oplogSpanMin"`
	Priority         Priority                 `bson:"priority,omitempty" json:"priority,omitempty" yaml:"priority,omitempty"`
	Timeouts         *BackupTimeouts          `bson:"timeouts,omitempty" json:"timeouts,omitempty" yaml:"timeouts,omitempty"`
	Compression      compress.CompressionType `bson:"compression,omitempty" json:"compression,omitempty" yaml:"compression,omitempty"`
	CompressionLevel *int                     `bson:"compressionLevel,omitempty" json:"compressionLevel,omitempty" yaml:"compressionLevel,omitempty"`

	NumParallelCollections int `bson:"numParallelCollections" json:"numParallelCollections,omitempty" yaml:"numParallelCollections,omitempty"`
}

func (cfg *BackupConf) Clone() *BackupConf {
	if cfg == nil {
		return nil
	}

	rv := *cfg

	rv.Priority = maps.Clone(cfg.Priority)
	if cfg.Timeouts != nil {
		if cfg.Timeouts.Starting != nil {
			rv.Timeouts = &BackupTimeouts{
				Starting: cfg.Timeouts.Starting,
			}
		}
	}
	if cfg.CompressionLevel != nil {
		a := *cfg.CompressionLevel
		rv.CompressionLevel = &a
	}

	return &rv
}

type BackupTimeouts struct {
	// Starting is timeout (in seconds) to wait for a backup to start.
	Starting *uint32 `bson:"startingStatus,omitempty" json:"startingStatus,omitempty" yaml:"startingStatus,omitempty"`
}

// StartingStatus returns timeout duration for .
// If not set or zero, returns default value (WaitBackupStart).
func (t *BackupTimeouts) StartingStatus() time.Duration {
	if t == nil || t.Starting == nil || *t.Starting == 0 {
		return defs.WaitBackupStart
	}

	return time.Duration(*t.Starting) * time.Second
}

func GetConfig(ctx context.Context, m connect.Client) (*Config, error) {
	res := m.ConfigCollection().FindOne(ctx, bson.D{{"profile", nil}})
	if err := res.Err(); err != nil {
		return nil, errors.Wrap(err, "get")
	}

	cfg := &Config{}
	if err := res.Decode(&cfg); err != nil {
		return nil, errors.Wrap(err, "decode")
	}

	if cfg.PITR == nil {
		cfg.PITR = &PITRConf{}
	}
	if cfg.Backup == nil {
		cfg.Backup = &BackupConf{}
	}
	if cfg.Restore == nil {
		cfg.Restore = &RestoreConf{}
	}

	if cfg.Backup.Compression == "" {
		cfg.Backup.Compression = defs.DefaultCompression
	}
	if cfg.PITR.Compression == "" {
		cfg.PITR.Compression = cfg.Backup.Compression
	}
	if cfg.PITR.CompressionLevel == nil {
		cfg.PITR.CompressionLevel = cfg.Backup.CompressionLevel
	}

	return cfg, nil
}

// SetConfig stores config doc within the database.
// It also applies default storage parameters depending on the type of storage
// and assigns those possible default values to the cfg parameter.
func SetConfig(ctx context.Context, m connect.Client, cfg *Config) error {
	if err := cfg.Storage.Cast(); err != nil {
		return errors.Wrap(err, "cast storage")
	}

	if cfg.Storage.Type == storage.S3 {
		// call the function for notification purpose.
		// warning about unsupported levels will be printed
		s3.SDKLogLevel(cfg.Storage.S3.DebugLogLevels, os.Stderr)
	}

	if cfg.PITR != nil {
		if c := string(cfg.PITR.Compression); c != "" && !compress.IsValidCompressionType(c) {
			return errors.Errorf("unsupported compression type: %q", c)
		}
	}

	ct, err := topo.GetClusterTime(ctx, m)
	if err != nil {
		return errors.Wrap(err, "get cluster time")
	}

	cfg.Epoch = ct

	// TODO: if store or pitr changed - need to bump epoch
	// TODO: struct tags to config opts `pbm:"resync,epoch"`?
	_, _ = GetConfig(ctx, m)

	_, err = m.ConfigCollection().ReplaceOne(ctx,
		bson.D{{"profile", nil}},
		cfg,
		options.Replace().SetUpsert(true))
	return errors.Wrap(err, "mongo defs.ConfigCollection UpdateOne")
}

func SetConfigVar(ctx context.Context, m connect.Client, key, val string) error {
	if !validateConfigKey(key) {
		return errors.New("invalid config key")
	}

	// just check if config was set
	_, err := GetConfig(ctx, m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return errors.New("config is not set")
		}
		return err
	}

	var v interface{}
	switch _confmap[key] {
	case reflect.String:
		v = val
	case reflect.Uint, reflect.Uint32:
		v, err = strconv.ParseUint(val, 10, 32)
	case reflect.Uint64:
		v, err = strconv.ParseUint(val, 10, 64)
	case reflect.Int, reflect.Int32:
		v, err = strconv.ParseInt(val, 10, 32)
	case reflect.Int64:
		v, err = strconv.ParseInt(val, 10, 64)
	case reflect.Float32:
		v, err = strconv.ParseFloat(val, 32)
	case reflect.Float64:
		v, err = strconv.ParseFloat(val, 64)
	case reflect.Bool:
		v, err = strconv.ParseBool(val)
	}
	if err != nil {
		return errors.Wrapf(err, "casting value of %s", key)
	}

	// TODO: how to be with special case options like pitr.enabled
	switch key {
	case "pitr.enabled":
		return errors.Wrap(confSetPITR(ctx, m, v.(bool)), "write to db")
	case "pitr.compression":
		if c := v.(string); c != "" && !compress.IsValidCompressionType(c) {
			return errors.Errorf("unsupported compression type: %q", c)
		}
	case "storage.filesystem.path":
		if v.(string) == "" {
			return errors.New("storage.filesystem.path can't be empty")
		}
	case "storage.s3.debugLogLevels":
		s3.SDKLogLevel(v.(string), os.Stderr)
	}

	_, err = m.ConfigCollection().UpdateOne(ctx,
		bson.D{{"profile", nil}},
		bson.M{"$set": bson.M{key: v}})
	return errors.Wrap(err, "write to db")
}

func confSetPITR(ctx context.Context, m connect.Client, value bool) error {
	ct, err := topo.GetClusterTime(ctx, m)
	if err != nil {
		return errors.Wrap(err, "get cluster time")
	}

	_, err = m.ConfigCollection().UpdateOne(ctx,
		bson.D{{"profile", nil}},
		bson.M{"$set": bson.M{
			"pitr.enabled": value,
			"epoch":        ct,
		}})

	return err
}

// GetConfigVar returns value of given config vaiable
func GetConfigVar(ctx context.Context, m connect.Client, key string) (interface{}, error) {
	if !validateConfigKey(key) {
		return nil, errors.Errorf("invalid config key: %s", key)
	}

	bts, err := m.ConfigCollection().
		FindOne(ctx, bson.D{{"profile", nil}}).
		Raw()
	if err != nil {
		return nil, errors.Wrap(err, "get from db")
	}
	v, err := bts.LookupErr(strings.Split(key, ".")...)
	if err != nil {
		return nil, errors.Wrap(err, "lookup in document")
	}

	switch v.Type {
	case bson.TypeBoolean:
		return v.Boolean(), nil
	case bson.TypeInt32:
		return v.Int32(), nil
	case bson.TypeInt64:
		return v.Int64(), nil
	case bson.TypeDouble:
		return v.Double(), nil
	case bson.TypeString:
		return v.StringValue(), nil
	default:
		return nil, errors.Errorf("unexpected type %v", v.Type)
	}
}

func IsPITREnabled(ctx context.Context, m connect.Client) (bool, bool, error) {
	cfg, err := GetConfig(ctx, m)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			err = ErrMissedConfig
		}

		return false, false, errors.Wrap(err, "get config")
	}

	return cfg.PITR.Enabled, cfg.PITR.OplogOnly, nil
}

type Epoch primitive.Timestamp

func (e Epoch) TS() primitive.Timestamp {
	return primitive.Timestamp(e)
}

func GetEpoch(ctx context.Context, m connect.Client) (Epoch, error) {
	opts := options.FindOne().SetProjection(bson.D{{"_id", 0}, {"epoch", 1}})
	res := m.ConfigCollection().FindOne(ctx, bson.D{{"profile", nil}}, opts)
	if err := res.Err(); err != nil {
		return Epoch{}, errors.Wrap(err, "query")
	}

	raw, err := res.Raw()
	if err != nil {
		return Epoch{}, errors.Wrap(err, "read raw")
	}

	val, err := raw.LookupErr("epoch")
	if err != nil {
		return Epoch{}, errors.Wrap(err, "lookup")
	}

	t, i, ok := val.TimestampOK()
	if !ok {
		return Epoch{}, errors.Wrap(err, "not a timestamp")
	}

	return Epoch{T: t, I: i}, nil
}

func ResetEpoch(ctx context.Context, m connect.Client) (Epoch, error) {
	ct, err := topo.GetClusterTime(ctx, m)
	if err != nil {
		return Epoch{}, errors.Wrap(err, "get cluster time")
	}

	_, err = m.ConfigCollection().UpdateOne(ctx,
		bson.D{{"profile", nil}},
		bson.M{"$set": bson.M{"epoch": ct}})
	return Epoch(ct), err
}
