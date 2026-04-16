package mio

import (
	"errors"
	"maps"
	"reflect"
)

//nolint:lll
type Config struct {
	Region      string            `bson:"region" json:"region" yaml:"region"`
	Endpoint    string            `bson:"endpoint,omitempty" json:"endpoint" yaml:"endpoint,omitempty"`
	EndpointMap map[string]string `bson:"endpointMap,omitempty" json:"endpointMap,omitempty" yaml:"endpointMap,omitempty"`
	Bucket      string            `bson:"bucket" json:"bucket" yaml:"bucket"`
	Prefix      string            `bson:"prefix" json:"prefix" yaml:"prefix"`
	Credentials Credentials       `bson:"credentials" json:"-" yaml:"credentials"`
	Secure      bool              `bson:"secure" json:"secure" yaml:"secure"`
	DebugTrace  bool              `bson:"debugTrace,omitempty" json:"debugTrace,omitempty" yaml:"debugTrace,omitempty"`

	PartSize     int64    `bson:"partSize,omitempty" json:"partSize,omitempty" yaml:"partSize,omitempty"`
	MaxObjSizeGB *float64 `bson:"maxObjSizeGB,omitempty" json:"maxObjSizeGB,omitempty" yaml:"maxObjSizeGB,omitempty"`

	Retryer *Retryer `bson:"retryer,omitempty" json:"retryer,omitempty" yaml:"retryer,omitempty"`

	// InsecureSkipTLSVerify disables client verification of the server's
	// certificate chain and host name
	InsecureSkipTLSVerify bool  `bson:"insecureSkipTLSVerify" json:"insecureSkipTLSVerify" yaml:"insecureSkipTLSVerify"`
	ForcePathStyle        *bool `bson:"forcePathStyle,omitempty" json:"forcePathStyle,omitempty" yaml:"forcePathStyle,omitempty"`
}

type Credentials struct {
	SigVer          string `bson:"signature-ver" json:"signature-ver,omitempty" yaml:"signature-ver,omitempty"`
	AccessKeyID     string `bson:"access-key-id" json:"access-key-id,omitempty" yaml:"access-key-id,omitempty"`
	SecretAccessKey string `bson:"secret-access-key" json:"secret-access-key,omitempty" yaml:"secret-access-key,omitempty"`
	SessionToken    string `bson:"session-token" json:"session-token,omitempty" yaml:"session-token,omitempty"`
}

type Retryer struct {
	// NumMaxRetries is the number of max retries that will be performed.
	NumMaxRetries int `bson:"numMaxRetries,omitempty" json:"numMaxRetries,omitempty" yaml:"numMaxRetries,omitempty"`
}

func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}

	c := *cfg
	c.EndpointMap = maps.Clone(cfg.EndpointMap)
	if cfg.MaxObjSizeGB != nil {
		v := *cfg.MaxObjSizeGB
		c.MaxObjSizeGB = &v
	}
	if cfg.ForcePathStyle != nil {
		v := *cfg.ForcePathStyle
		c.ForcePathStyle = &v
	}
	if cfg.Retryer != nil {
		v := *cfg.Retryer
		c.Retryer = &v
	}

	return &c
}

func (cfg *Config) Equal(other *Config) bool {
	return reflect.DeepEqual(cfg, other)
}

// IsSameStorage identifies the same instance of the minio storage.
func (cfg *Config) IsSameStorage(other *Config) bool {
	if cfg == nil || other == nil {
		return cfg == other
	}

	if cfg.Region != other.Region {
		return false
	}
	if cfg.Endpoint != other.Endpoint {
		return false
	}
	if !maps.Equal(cfg.EndpointMap, other.EndpointMap) {
		return false
	}
	if cfg.Bucket != other.Bucket {
		return false
	}
	if cfg.Prefix != other.Prefix {
		return false
	}
	return true
}

func (cfg *Config) Cast() error {
	if cfg == nil {
		return errors.New("missing minio configuration with minio storage type")
	}

	if cfg.Endpoint == "" {
		return errors.New("endpointURL cannot be empty")
	}

	if cfg.PartSize == 0 {
		cfg.PartSize = defaultPartSize
	}

	if cfg.Retryer == nil {
		cfg.Retryer = &Retryer{
			NumMaxRetries: defaultMaxRetries,
		}
	} else {
		if cfg.Retryer.NumMaxRetries == 0 {
			cfg.Retryer.NumMaxRetries = defaultMaxRetries
		}
	}

	return nil
}

// resolveEndpointURL returns endpoint url based on provided
// EndpointURL or associated EndpointURLMap configuration fields.
// If specified EndpointURLMap overrides EndpointURL field.
func (cfg *Config) resolveEndpointURL(node string) string {
	ep := cfg.Endpoint
	if epm, ok := cfg.EndpointMap[node]; ok {
		ep = epm
	}
	return ep
}

func (cfg *Config) GetMaxObjSizeGB() float64 {
	if cfg.MaxObjSizeGB != nil && *cfg.MaxObjSizeGB > 0 {
		return *cfg.MaxObjSizeGB
	}
	return defaultMaxObjSizeGB
}
