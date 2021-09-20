package pbm

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"gopkg.in/yaml.v2"

	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/azure"
	"github.com/percona/percona-backup-mongodb/pbm/storage/blackhole"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
)

// Config is a pbm config
type Config struct {
	PITR    PITRConf            `bson:"pitr" json:"pitr" yaml:"pitr"`
	Storage StorageConf         `bson:"storage" json:"storage" yaml:"storage"`
	Restore RestoreConf         `bson:"restore" json:"restore,omitempty" yaml:"restore,omitempty"`
	Backup  BackupConf          `bson:"backup" json:"backup,omitempty" yaml:"backup,omitempty"`
	Epoch   primitive.Timestamp `bson:"epoch" json:"-" yaml:"-"`
}

func (c Config) String() string {
	if c.Storage.S3.Credentials.AccessKeyID != "" {
		c.Storage.S3.Credentials.AccessKeyID = "***"
	}
	if c.Storage.S3.Credentials.SecretAccessKey != "" {
		c.Storage.S3.Credentials.SecretAccessKey = "***"
	}
	if c.Storage.S3.Credentials.Vault.Secret != "" {
		c.Storage.S3.Credentials.Vault.Secret = "***"
	}
	if c.Storage.S3.Credentials.Vault.Token != "" {
		c.Storage.S3.Credentials.Vault.Token = "***"
	}
	if c.Storage.Azure.Credentials.Key != "" {
		c.Storage.Azure.Credentials.Key = "***"
	}

	b, err := yaml.Marshal(c)
	if err != nil {
		return fmt.Sprintln("error:", err)
	}

	return string(b)
}

// PITRConf is a Point-In-Time Recovery options
type PITRConf struct {
	Enabled      bool    `bson:"enabled" json:"enabled" yaml:"enabled"`
	OplogSpanMin float64 `bson:"oplogSpanMin" json:"oplogSpanMin" yaml:"oplogSpanMin"`
}

// StorageType represents a type of the destination storage for backups
type StorageType string

const (
	StorageUndef      StorageType = ""
	StorageS3         StorageType = "s3"
	StorageAzure      StorageType = "azure"
	StorageFilesystem StorageType = "filesystem"
	StorageBlackHole  StorageType = "blackhole"
)

// StorageConf is a configuration of the backup storage
type StorageConf struct {
	Type       StorageType `bson:"type" json:"type" yaml:"type"`
	S3         s3.Conf     `bson:"s3,omitempty" json:"s3,omitempty" yaml:"s3,omitempty"`
	Azure      azure.Conf  `bson:"azure,omitempty" json:"azure,omitempty" yaml:"azure,omitempty"`
	Filesystem fs.Conf     `bson:"filesystem,omitempty" json:"filesystem,omitempty" yaml:"filesystem,omitempty"`
}

func (s *StorageConf) Typ() string {
	switch s.Type {
	case StorageS3:
		return "S3"
	case StorageAzure:
		return "Azure"
	case StorageFilesystem:
		return "FS"
	case StorageBlackHole:
		return "BlackHole"
	default:
		return "Unknown"
	}
}

func (s *StorageConf) Path() string {
	path := ""
	switch s.Type {
	case StorageS3:
		path = "s3://"
		if s.S3.EndpointURL != "" {
			path += s.S3.EndpointURL + "/"
		}
		path += s.S3.Bucket
		if s.S3.Prefix != "" {
			path += "/" + s.S3.Prefix
		}
	case StorageAzure:
		path = fmt.Sprintf(azure.BlobURL, s.Azure.Account, s.Azure.Container)
		if s.Azure.Prefix != "" {
			path += "/" + s.Azure.Prefix
		}
	case StorageFilesystem:
		path = s.Filesystem.Path
	case StorageBlackHole:
		path = "BlackHole"
	}

	return path
}

// RestoreConf is config options for the restore
type RestoreConf struct {
	BatchSize           int `bson:"batchSize" json:"batchSize,omitempty" yaml:"batchSize,omitempty"` // num of documents to buffer
	NumInsertionWorkers int `bson:"numInsertionWorkers" json:"numInsertionWorkers,omitempty" yaml:"numInsertionWorkers,omitempty"`
}

type BackupConf struct {
	Priority map[string]float64 `bson:"priority,omitempty" json:"priority,omitempty" yaml:"priority,omitempty"`
}

type confMap map[string]reflect.Kind

// _confmap is a list of config's valid keys and its types
var _confmap confMap

func init() {
	_confmap = keys(reflect.TypeOf(Config{}))
}

func keys(t reflect.Type) confMap {
	v := make(confMap)
	for i := 0; i < t.NumField(); i++ {
		name := strings.TrimSpace(strings.Split(t.Field(i).Tag.Get("bson"), ",")[0])

		typ := t.Field(i).Type
		if typ.Kind() == reflect.Ptr {
			typ = typ.Elem()
		}
		switch typ.Kind() {
		case reflect.Struct:
			for n, t := range keys(typ) {
				v[name+"."+n] = t
			}
		default:
			v[name] = typ.Kind()
		}
	}
	return v
}

func (p *PBM) SetConfigByte(buf []byte) error {
	var cfg Config
	err := yaml.UnmarshalStrict(buf, &cfg)
	if err != nil {
		return errors.Wrap(err, "unmarshal yaml")
	}
	return errors.Wrap(p.SetConfig(cfg), "write to db")
}

func (p *PBM) SetConfig(cfg Config) error {
	switch cfg.Storage.Type {
	case StorageS3:
		err := cfg.Storage.S3.Cast()
		if err != nil {
			return errors.Wrap(err, "cast storage")
		}
	case StorageFilesystem:
		err := cfg.Storage.Filesystem.Cast()
		if err != nil {
			return errors.Wrap(err, "check config")
		}
	}
	ct, err := p.ClusterTime()
	if err != nil {
		return errors.Wrap(err, "get cluster time")
	}

	cfg.Epoch = ct

	// TODO: if store or pitr changed - need to bump epoch
	// TODO: struct tags to config opts `pbm:"resync,epoch"`?
	p.GetConfig()

	_, err = p.Conn.Database(DB).Collection(ConfigCollection).UpdateOne(
		p.ctx,
		bson.D{},
		bson.M{"$set": cfg},
		options.Update().SetUpsert(true),
	)
	return errors.Wrap(err, "mongo ConfigCollection UpdateOne")
}

func (p *PBM) SetConfigVar(key, val string) error {
	if !ValidateConfigKey(key) {
		return errors.New("invalid config key")
	}

	// just check if config was set
	_, err := p.GetConfig()
	if err != nil {
		if errors.Cause(err) == mongo.ErrNoDocuments {
			return errors.New("config doesn't set")
		}
		return err
	}

	var v interface{}
	switch _confmap[key] {
	case reflect.String:
		v = val
	case reflect.Int, reflect.Int64:
		v, err = strconv.ParseInt(val, 10, 64)
	case reflect.Float32, reflect.Float64:
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
		return errors.Wrap(p.confSetPITR(key, v.(bool)), "write to db")
	case "storage.filesystem.path":
		if v.(string) == "" {
			return errors.New("storage.filesystem.path can't be empty")
		}
	}

	_, err = p.Conn.Database(DB).Collection(ConfigCollection).UpdateOne(
		p.ctx,
		bson.D{},
		bson.M{"$set": bson.M{key: v}},
	)

	return errors.Wrap(err, "write to db")
}

func (p *PBM) confSetPITR(k string, v bool) error {
	ct, err := p.ClusterTime()
	if err != nil {
		return errors.Wrap(err, "get cluster time")
	}
	_, err = p.Conn.Database(DB).Collection(ConfigCollection).UpdateOne(
		p.ctx,
		bson.D{},
		bson.M{"$set": bson.M{k: v, "pitr.changed": time.Now().Unix(), "epoch": ct}},
	)

	return err
}

// GetConfigVar returns value of given config vaiable
func (p *PBM) GetConfigVar(key string) (interface{}, error) {
	if !ValidateConfigKey(key) {
		return nil, errors.New("invalid config key")
	}

	bts, err := p.Conn.Database(DB).Collection(ConfigCollection).FindOne(p.ctx, bson.D{}).DecodeBytes()
	if err != nil {
		return nil, errors.Wrap(err, "get from db")
	}
	v, err := bts.LookupErr(strings.Split(key, ".")...)
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
		return v.String(), nil
	default:
		return nil, errors.Errorf("unexpected type %v", v.Type)
	}
}

// ValidateConfigKey checks if a config key valid
func ValidateConfigKey(k string) bool {
	_, ok := _confmap[k]
	return ok
}

func (p *PBM) GetConfigYaml(fieldRedaction bool) ([]byte, error) {
	c, err := p.GetConfig()
	if err != nil {
		return nil, errors.Wrap(err, "get from db")
	}

	if fieldRedaction {
		if c.Storage.S3.Credentials.AccessKeyID != "" {
			c.Storage.S3.Credentials.AccessKeyID = "***"
		}
		if c.Storage.S3.Credentials.SecretAccessKey != "" {
			c.Storage.S3.Credentials.SecretAccessKey = "***"
		}
		if c.Storage.S3.Credentials.Vault.Secret != "" {
			c.Storage.S3.Credentials.Vault.Secret = "***"
		}
		if c.Storage.S3.Credentials.Vault.Token != "" {
			c.Storage.S3.Credentials.Vault.Token = "***"
		}
		if c.Storage.Azure.Credentials.Key != "" {
			c.Storage.Azure.Credentials.Key = "***"
		}
	}

	b, err := yaml.Marshal(c)
	return b, errors.Wrap(err, "marshal yaml")
}

func (p *PBM) GetConfig() (Config, error) {
	var c Config
	res := p.Conn.Database(DB).Collection(ConfigCollection).FindOne(p.ctx, bson.D{})
	if res.Err() != nil {
		return Config{}, errors.Wrap(res.Err(), "get")
	}
	err := res.Decode(&c)
	return c, errors.Wrap(err, "decode")
}

// ErrStorageUndefined is an error for undefined storage
var ErrStorageUndefined = errors.New("storage undefined")

// GetStorage reads current storage config and creates and
// returns respective storage.Storage object
func (p *PBM) GetStorage(l *log.Event) (storage.Storage, error) {
	c, err := p.GetConfig()
	if err != nil {
		return nil, errors.Wrap(err, "get config")
	}

	switch c.Storage.Type {
	case StorageS3:
		return s3.New(c.Storage.S3, l)
	case StorageAzure:
		return azure.New(c.Storage.Azure, l)
	case StorageFilesystem:
		return fs.New(c.Storage.Filesystem), nil
	case StorageBlackHole:
		return blackhole.New(), nil
	case StorageUndef:
		return nil, ErrStorageUndefined
	default:
		return nil, errors.Errorf("unknown storage type %s", c.Storage.Type)
	}
}
