package oss

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss"
	osscred "github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/credentials"
	"github.com/aliyun/alibabacloud-oss-go-sdk-v2/oss/retry"
	"github.com/aliyun/credentials-go/credentials/providers"
)

const (
	defaultOSSRegion       = "us-east-1"
	maxPart          int32 = 10000

	defaultRetryMaxAttempts       = 5
	defaultRetryBaseDelay         = 30 * time.Millisecond
	defaultRetryerMaxBackoff      = 300 * time.Second
	defaultSessionDurationSeconds = 3600
	defaultConnectTimeout         = 5 * time.Second
	defaultMaxObjSizeGB           = 48700 // 48.8 TB
)

//nolint:lll
type Config struct {
	Region      string `bson:"region" json:"region" yaml:"region"`
	EndpointURL string `bson:"endpointUrl,omitempty" json:"endpointUrl" yaml:"endpointUrl,omitempty"`

	Bucket      string      `bson:"bucket" json:"bucket" yaml:"bucket"`
	Prefix      string      `bson:"prefix,omitempty" json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Credentials Credentials `bson:"credentials" json:"-" yaml:"credentials"`

	Retryer *Retryer `bson:"retryer,omitempty" json:"retryer,omitempty" yaml:"retryer,omitempty"`

	ConnectTimeout time.Duration `bson:"connectTimeout" json:"connectTimeout" yaml:"connectTimeout"`
	UploadPartSize int64         `bson:"uploadPartSize,omitempty" json:"uploadPartSize,omitempty" yaml:"uploadPartSize,omitempty"`
	MaxUploadParts int32         `bson:"maxUploadParts,omitempty" json:"maxUploadParts,omitempty" yaml:"maxUploadParts,omitempty"`
	MaxObjSizeGB   *float64      `bson:"maxObjSizeGB,omitempty" json:"maxObjSizeGB,omitempty" yaml:"maxObjSizeGB,omitempty"`

	ServerSideEncryption *SSE `bson:"serverSideEncryption,omitempty" json:"serverSideEncryption,omitempty" yaml:"serverSideEncryption,omitempty"`
}

type SSE struct {
	EncryptionMethod    string `bson:"encryptionMethod,omitempty" json:"encryptionMethod,omitempty" yaml:"encryptionMethod,omitempty"`
	EncryptionAlgorithm string `bson:"encryptionAlgorithm,omitempty" json:"encryptionAlgorithm,omitempty" yaml:"encryptionAlgorithm,omitempty"`
	EncryptionKeyID     string `bson:"encryptionKeyId,omitempty" json:"encryptionKeyId,omitempty" yaml:"encryptionKeyId,omitempty"`
}

type Retryer struct {
	MaxAttempts int           `bson:"maxAttempts" json:"maxAttempts" yaml:"maxAttempts"`
	MaxBackoff  time.Duration `bson:"maxBackoff" json:"maxBackoff" yaml:"maxBackoff"`
	BaseDelay   time.Duration `bson:"baseDelay" json:"baseDelay" yaml:"baseDelay"`
}

type Credentials struct {
	AccessKeyID     string `bson:"accessKeyId" json:"accessKeyId,omitempty" yaml:"accessKeyId,omitempty"`
	AccessKeySecret string `bson:"accessKeySecret" json:"accessKeySecret,omitempty" yaml:"accessKeySecret,omitempty"`
	SecurityToken   string `bson:"securityToken" json:"securityToken,omitempty" yaml:"securityToken,omitempty"`
	RoleARN         string `bson:"roleArn,omitempty" json:"roleArn,omitempty" yaml:"roleArn,omitempty"`
	SessionName     string `bson:"sessionName,omitempty" json:"sessionName,omitempty" yaml:"sessionName,omitempty"`
}

// IsSameStorage identifies the same instance of the OSS storage.
func (cfg *Config) IsSameStorage(other *Config) bool {
	if cfg == nil || other == nil {
		return cfg == other
	}

	if cfg.Region != other.Region {
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
		return errors.New("missing oss configuration with oss storage type")
	}
	if cfg.Region == "" {
		cfg.Region = defaultOSSRegion
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.Retryer != nil {
		if cfg.Retryer.MaxAttempts == 0 {
			cfg.Retryer.MaxAttempts = defaultRetryMaxAttempts
		}
		if cfg.Retryer.BaseDelay == 0 {
			cfg.Retryer.BaseDelay = defaultRetryBaseDelay
		}
		if cfg.Retryer.MaxBackoff == 0 {
			cfg.Retryer.MaxBackoff = defaultRetryerMaxBackoff
		}
	} else {
		cfg.Retryer = &Retryer{
			MaxAttempts: defaultRetryMaxAttempts,
			MaxBackoff:  defaultRetryerMaxBackoff,
			BaseDelay:   defaultRetryBaseDelay,
		}
	}
	if cfg.MaxUploadParts <= 0 {
		cfg.MaxUploadParts = maxPart
	}
	if cfg.UploadPartSize <= 0 {
		cfg.UploadPartSize = oss.DefaultUploadPartSize
	}
	return nil
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
	if cfg.ServerSideEncryption != nil {
		a := *cfg.ServerSideEncryption
		rv.ServerSideEncryption = &a
	}
	return &rv
}

func (cfg *Config) GetMaxObjSizeGB() float64 {
	if cfg.MaxObjSizeGB != nil && *cfg.MaxObjSizeGB > 0 {
		return *cfg.MaxObjSizeGB
	}
	return defaultMaxObjSizeGB
}

func newCred(config *Config) (*cred, error) {
	var credentialsProvider providers.CredentialsProvider
	var err error

	if config.Credentials.AccessKeyID == "" || config.Credentials.AccessKeySecret == "" {
		return nil, fmt.Errorf("access key ID and secret are required")
	}

	if config.Credentials.SecurityToken != "" {
		credentialsProvider, err = providers.NewStaticSTSCredentialsProviderBuilder().
			WithAccessKeyId(config.Credentials.AccessKeyID).
			WithAccessKeySecret(config.Credentials.AccessKeySecret).
			WithSecurityToken(config.Credentials.SecurityToken).
			Build()
	} else {
		credentialsProvider, err = providers.NewStaticAKCredentialsProviderBuilder().
			WithAccessKeyId(config.Credentials.AccessKeyID).
			WithAccessKeySecret(config.Credentials.AccessKeySecret).
			Build()
	}
	if err != nil {
		return nil, fmt.Errorf("credentials provider: %w", err)
	}

	if config.Credentials.RoleARN != "" {
		internalProvider := credentialsProvider
		credentialsProvider, err = providers.NewRAMRoleARNCredentialsProviderBuilder().
			WithCredentialsProvider(internalProvider).
			WithRoleArn(config.Credentials.RoleARN).
			WithRoleSessionName(config.Credentials.SessionName).
			WithDurationSeconds(defaultSessionDurationSeconds).
			Build()
		if err != nil {
			return nil, fmt.Errorf("ram role credential provider: %w", err)
		}
	}

	return &cred{
		provider: credentialsProvider,
	}, nil
}

type cred struct {
	provider providers.CredentialsProvider
}

func (c *cred) GetCredentials(ctx context.Context) (osscred.Credentials, error) {
	cc, err := c.provider.GetCredentials()
	if err != nil {
		return osscred.Credentials{}, err
	}

	return osscred.Credentials{
		AccessKeyID:     cc.AccessKeyId,
		AccessKeySecret: cc.AccessKeySecret,
		SecurityToken:   cc.SecurityToken,
	}, nil
}

func configureClient(config *Config) (*oss.Client, error) {
	if config == nil {
		return nil, fmt.Errorf("config is nil")
	}

	if config.Region == "" || config.Bucket == "" ||
		config.Credentials.AccessKeyID == "" ||
		config.Credentials.AccessKeySecret == "" {
		return nil, fmt.Errorf("Missing required OSS config: %+v", config)
	}

	cred, err := newCred(config)
	if err != nil {
		return nil, fmt.Errorf("create credentials: %w", err)
	}

	ossConfig := oss.LoadDefaultConfig().
		WithRegion(config.Region).
		WithCredentialsProvider(cred).
		WithSignatureVersion(oss.SignatureVersionV4).
		WithRetryMaxAttempts(config.Retryer.MaxAttempts).
		WithRetryer(retry.NewStandard(func(ro *retry.RetryOptions) {
			ro.MaxAttempts = config.Retryer.MaxAttempts
			ro.MaxBackoff = config.Retryer.MaxBackoff
			ro.BaseDelay = config.Retryer.BaseDelay
		})).
		WithConnectTimeout(config.ConnectTimeout)

	if config.EndpointURL != "" {
		ossConfig = ossConfig.WithEndpoint(config.EndpointURL)
	}

	return oss.NewClient(ossConfig), nil
}
