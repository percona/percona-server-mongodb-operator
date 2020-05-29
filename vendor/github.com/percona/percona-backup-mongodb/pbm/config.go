package pbm

import (
	"reflect"
	"strings"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"gopkg.in/yaml.v2"

	"github.com/percona/percona-backup-mongodb/pbm/storage"
	"github.com/percona/percona-backup-mongodb/pbm/storage/blackhole"
	"github.com/percona/percona-backup-mongodb/pbm/storage/fs"
	"github.com/percona/percona-backup-mongodb/pbm/storage/s3"
)

// Config is a pbm config
type Config struct {
	Storage StorageConf `bson:"storage" json:"storage" yaml:"storage"`
}

type StorageType string

const (
	StorageUndef      StorageType = ""
	StorageS3         StorageType = "s3"
	StorageFilesystem StorageType = "filesystem"
	StorageBlackHole  StorageType = "blackhole"
)

type StorageConf struct {
	Type       StorageType `bson:"type" json:"type" yaml:"type"`
	S3         s3.Conf     `bson:"s3,omitempty" json:"s3,omitempty" yaml:"s3,omitempty"`
	Filesystem fs.Conf     `bson:"filesystem,omitempty" json:"filesystem,omitempty" yaml:"filesystem,omitempty"`
}

// ConfKeys returns valid config keys (option names)
func ConfKeys() []string {
	return keys(reflect.TypeOf(Config{}))
}

func keys(t reflect.Type) []string {
	var v []string
	for i := 0; i < t.NumField(); i++ {
		name := strings.TrimSpace(strings.Split(t.Field(i).Tag.Get("bson"), ",")[0])
		if t.Field(i).Type.Kind() == reflect.Struct {
			for _, n := range keys(t.Field(i).Type) {
				v = append(v, name+"."+n)
			}
		} else {
			v = append(v, name)
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
	if cfg.Storage.Type == StorageS3 {
		err := cfg.Storage.S3.Cast()
		if err != nil {
			return errors.Wrap(err, "cast storage")
		}
	}

	_, err := p.Conn.Database(DB).Collection(ConfigCollection).UpdateOne(
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
	_, err := p.GetConfigVar(key)
	if err != nil {
		if errors.Cause(err) == mongo.ErrNoDocuments {
			return errors.New("config doesn't set")
		}
		return err
	}
	_, err = p.Conn.Database(DB).Collection(ConfigCollection).UpdateOne(
		p.ctx,
		bson.D{},
		bson.M{"$set": bson.M{key: val}},
	)

	return errors.Wrap(err, "write to db")
}

// GetConfigVar returns value of given config vaiable
func (p *PBM) GetConfigVar(key string) (string, error) {
	if !ValidateConfigKey(key) {
		return "", errors.New("invalid config key")
	}

	bts, err := p.Conn.Database(DB).Collection(ConfigCollection).FindOne(p.ctx, bson.D{}).DecodeBytes()
	if err != nil {
		return "", errors.Wrap(err, "get from db")
	}
	return string(bts.Lookup(strings.Split(key, ".")...).Value), nil
}

// ValidateConfigKey checks if a config key valid
func ValidateConfigKey(k string) bool {
	for _, v := range ConfKeys() {
		if k == v {
			return true
		}
	}
	return false
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
func (p *PBM) GetStorage() (storage.Storage, error) {
	c, err := p.GetConfig()
	if err != nil {
		return nil, errors.Wrap(err, "get config")
	}

	switch c.Storage.Type {
	case StorageS3:
		return s3.New(c.Storage.S3)
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
