package gcs

import (
	"reflect"
	"time"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
)

//nolint:lll
type Config struct {
	Bucket      string      `bson:"bucket" json:"bucket" yaml:"bucket"`
	Prefix      string      `bson:"prefix" json:"prefix" yaml:"prefix"`
	Credentials Credentials `bson:"credentials" json:"-" yaml:"credentials"`

	// The maximum number of bytes that the Writer will attempt to send in a single request.
	// https://pkg.go.dev/cloud.google.com/go/storage#Writer
	ChunkSize    int      `bson:"chunkSize,omitempty" json:"chunkSize,omitempty" yaml:"chunkSize,omitempty"`
	MaxObjSizeGB *float64 `bson:"maxObjSizeGB,omitempty" json:"maxObjSizeGB,omitempty" yaml:"maxObjSizeGB,omitempty"`

	Retryer *Retryer `bson:"retryer,omitempty" json:"retryer,omitempty" yaml:"retryer,omitempty"`
}

type Credentials struct {
	// JSON credentials (service account)
	ClientEmail string `bson:"clientEmail" json:"clientEmail,omitempty" yaml:"clientEmail,omitempty"`
	PrivateKey  string `bson:"privateKey" json:"privateKey,omitempty" yaml:"privateKey,omitempty"`

	// HMAC credentials for XML API (S3 compatibility)
	HMACAccessKey string `bson:"hmacAccessKey" json:"hmacAccessKey,omitempty" yaml:"hmacAccessKey,omitempty"`
	HMACSecret    string `bson:"hmacSecret" json:"hmacSecret,omitempty" yaml:"hmacSecret,omitempty"`
}

//nolint:lll
type Retryer struct {
	// BackoffInitial is the initial value of the retry period.
	// https://pkg.go.dev/github.com/googleapis/gax-go/v2@v2.12.3#Backoff.Initial
	BackoffInitial time.Duration `bson:"backoffInitial" json:"backoffInitial" yaml:"backoffInitial"`

	// BackoffMax is the maximum value of the retry period.
	// https://pkg.go.dev/github.com/googleapis/gax-go/v2@v2.12.3#Backoff.Max
	BackoffMax time.Duration `bson:"backoffMax" json:"backoffMax" yaml:"backoffMax"`

	// BackoffMultiplier is the factor by which the retry period increases.
	// https://pkg.go.dev/github.com/googleapis/gax-go/v2@v2.12.3#Backoff.Multiplier
	// Ignored for MinIO (only Initial and Max used)
	BackoffMultiplier float64 `bson:"backoffMultiplier" json:"backoffMultiplier" yaml:"backoffMultiplier"`

	// MaxAttempts configures the maximum number of tries.
	// E.g. if you it's set to 5, op will be attempted up to 5 times total (initial call + 4 retries).
	// https://pkg.go.dev/cloud.google.com/go/storage#WithMaxAttempts
	MaxAttempts int `bson:"maxAttempts,omitempty" json:"maxAttempts,omitempty" yaml:"maxAttempts,omitempty"`

	// ChunkRetryDeadline sets a per-chunk retry deadline for multi-chunk
	// resumable uploads.
	// https://pkg.go.dev/cloud.google.com/go/storage#Writer
	ChunkRetryDeadline time.Duration `bson:"chunkRetryDeadline,omitempty" json:"chunkRetryDeadline,omitempty" yaml:"chunkRetryDeadline,omitempty"`
}

func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}

	rv := *cfg
	if cfg.MaxObjSizeGB != nil {
		v := *cfg.MaxObjSizeGB
		rv.MaxObjSizeGB = &v
	}
	if cfg.Retryer != nil {
		v := *cfg.Retryer
		rv.Retryer = &v
	}

	return &rv
}

func (cfg *Config) Equal(other *Config) bool {
	if cfg == nil || other == nil {
		return cfg == other
	}

	if cfg.Bucket != other.Bucket {
		return false
	}
	if cfg.Prefix != other.Prefix {
		return false
	}
	if cfg.ChunkSize != other.ChunkSize {
		return false
	}
	if !reflect.DeepEqual(cfg.MaxObjSizeGB, other.MaxObjSizeGB) {
		return false
	}
	if !reflect.DeepEqual(cfg.Credentials, other.Credentials) {
		return false
	}
	if !reflect.DeepEqual(cfg.Retryer, other.Retryer) {
		return false
	}

	return true
}

// IsSameStorage identifies the same instance of the GCS storage.
func (cfg *Config) IsSameStorage(other *Config) bool {
	if cfg == nil || other == nil {
		return cfg == other
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
		return errors.New("missing GCS configuration with GCS storage type")
	}

	if cfg.ChunkSize == 0 {
		cfg.ChunkSize = defaultChunkSize
	}

	if cfg.Retryer == nil {
		cfg.Retryer = &Retryer{
			MaxAttempts:        defaultMaxAttempts,
			BackoffInitial:     defaultBackoffInitial,
			BackoffMax:         defaultBackoffMax,
			BackoffMultiplier:  defaultBackoffMultiplier,
			ChunkRetryDeadline: defaultChunkRetryDeadline,
		}
	} else {
		if cfg.Retryer.MaxAttempts == 0 {
			cfg.Retryer.MaxAttempts = defaultMaxAttempts
		}
		if cfg.Retryer.BackoffInitial == 0 {
			cfg.Retryer.BackoffInitial = defaultBackoffInitial
		}
		if cfg.Retryer.BackoffMax == 0 {
			cfg.Retryer.BackoffMax = defaultBackoffMax
		}
		if cfg.Retryer.BackoffMultiplier == 0 {
			cfg.Retryer.BackoffMultiplier = defaultBackoffMultiplier
		}
		if cfg.Retryer.ChunkRetryDeadline == 0 {
			cfg.Retryer.ChunkRetryDeadline = defaultChunkRetryDeadline
		}
	}

	return nil
}

func (cfg *Config) GetMaxObjSizeGB() float64 {
	if cfg.MaxObjSizeGB != nil && *cfg.MaxObjSizeGB > 0 {
		return *cfg.MaxObjSizeGB
	}
	return defaultMaxObjSizeGB
}
