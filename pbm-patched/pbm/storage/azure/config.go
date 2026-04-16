package azure

import (
	"fmt"
	"maps"
	"reflect"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

//nolint:lll
type Config struct {
	Account        string            `bson:"account" json:"account,omitempty" yaml:"account,omitempty"`
	Container      string            `bson:"container" json:"container,omitempty" yaml:"container,omitempty"`
	EndpointURL    string            `bson:"endpointUrl" json:"endpointUrl,omitempty" yaml:"endpointUrl,omitempty"`
	EndpointURLMap map[string]string `bson:"endpointUrlMap,omitempty" json:"endpointUrlMap,omitempty" yaml:"endpointUrlMap,omitempty"`
	Prefix         string            `bson:"prefix" json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Credentials    Credentials       `bson:"credentials" json:"-" yaml:"credentials"`
	Retryer        *Retryer          `bson:"retryer,omitempty" json:"retryer,omitempty" yaml:"retryer,omitempty"`
	MaxObjSizeGB   *float64          `bson:"maxObjSizeGB,omitempty" json:"maxObjSizeGB,omitempty" yaml:"maxObjSizeGB,omitempty"`
}

type Credentials struct {
	Key string `bson:"key" json:"key,omitempty" yaml:"key,omitempty"`
}

// Retryer is configuration for retry behavior described:
// https://pkg.go.dev/github.com/Azure/azure-sdk-for-go/sdk/azcore@v1.19.1/policy#RetryOptions
type Retryer struct {
	NumMaxRetries int32         `bson:"numMaxRetries" json:"numMaxRetries" yaml:"numMaxRetries"`
	MinRetryDelay time.Duration `bson:"minRetryDelay" json:"minRetryDelay" yaml:"minRetryDelay"`
	MaxRetryDelay time.Duration `bson:"maxRetryDelay" json:"maxRetryDelay" yaml:"maxRetryDelay"`
}

func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}

	rv := *cfg
	rv.EndpointURLMap = maps.Clone(cfg.EndpointURLMap)
	if cfg.Retryer != nil {
		v := *cfg.Retryer
		rv.Retryer = &v
	}
	if cfg.MaxObjSizeGB != nil {
		v := *cfg.MaxObjSizeGB
		rv.MaxObjSizeGB = &v
	}
	return &rv
}

func (cfg *Config) Equal(other *Config) bool {
	return reflect.DeepEqual(cfg, other)
}

// IsSameStorage identifies the same instance of the Azure storage.
func (cfg *Config) IsSameStorage(other *Config) bool {
	if cfg == nil || other == nil {
		return cfg == other
	}

	if cfg.Account != other.Account {
		return false
	}
	if cfg.Container != other.Container {
		return false
	}
	if cfg.Prefix != other.Prefix {
		return false
	}
	return true
}

func (cfg *Config) Cast() error {
	if cfg == nil {
		return errors.New("missing azure configuration with azure storage type")
	}

	if cfg.Retryer == nil {
		cfg.Retryer = &Retryer{
			NumMaxRetries: defaultMaxRetries,
			MinRetryDelay: defaultMinRetryDelay,
			MaxRetryDelay: defaultMaxRetryDelay,
		}
	} else {
		if cfg.Retryer.NumMaxRetries == 0 {
			cfg.Retryer.NumMaxRetries = defaultMaxRetries
		}
		if cfg.Retryer.MinRetryDelay == 0 {
			cfg.Retryer.MinRetryDelay = defaultMinRetryDelay
		}
		if cfg.Retryer.MaxRetryDelay == 0 {
			cfg.Retryer.MaxRetryDelay = defaultMaxRetryDelay
		}
	}

	return nil
}

// resolveEndpointURL returns endpoint url based on provided
// EndpointURL or associated EndpointURLMap configuration fields.
// If specified EndpointURLMap overrides EndpointURL field.
func (cfg *Config) resolveEndpointURL(node string) string {
	ep := cfg.EndpointURL
	if epm, ok := cfg.EndpointURLMap[node]; ok {
		ep = epm
	}
	if ep == "" {
		ep = fmt.Sprintf(BlobURL, cfg.Account)
	}
	return ep
}

func (cfg *Config) GetMaxObjSizeGB() float64 {
	if cfg.MaxObjSizeGB != nil && *cfg.MaxObjSizeGB > 0 {
		return *cfg.MaxObjSizeGB
	}
	return defaultMaxObjSizeGB
}
