package s3

import (
	"crypto/md5"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path"
	"reflect"
	"runtime"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/defaults"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/aws/aws-sdk-go/service/sts"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

const (
	// GCSEndpointURL is the endpoint url for Google Clound Strage service
	GCSEndpointURL = "storage.googleapis.com"

	defaultS3Region = "us-east-1"
)

//nolint:lll
type Config struct {
	Provider             string            `bson:"provider,omitempty" json:"provider,omitempty" yaml:"provider,omitempty"`
	Region               string            `bson:"region" json:"region" yaml:"region"`
	EndpointURL          string            `bson:"endpointUrl,omitempty" json:"endpointUrl" yaml:"endpointUrl,omitempty"`
	EndpointURLMap       map[string]string `bson:"endpointUrlMap,omitempty" json:"endpointUrlMap,omitempty" yaml:"endpointUrlMap,omitempty"`
	ForcePathStyle       *bool             `bson:"forcePathStyle,omitempty" json:"forcePathStyle,omitempty" yaml:"forcePathStyle,omitempty"`
	Bucket               string            `bson:"bucket" json:"bucket" yaml:"bucket"`
	Prefix               string            `bson:"prefix,omitempty" json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Credentials          Credentials       `bson:"credentials" json:"-" yaml:"credentials"`
	ServerSideEncryption *AWSsse           `bson:"serverSideEncryption,omitempty" json:"serverSideEncryption,omitempty" yaml:"serverSideEncryption,omitempty"`
	UploadPartSize       int               `bson:"uploadPartSize,omitempty" json:"uploadPartSize,omitempty" yaml:"uploadPartSize,omitempty"`
	MaxUploadParts       int               `bson:"maxUploadParts,omitempty" json:"maxUploadParts,omitempty" yaml:"maxUploadParts,omitempty"`
	StorageClass         string            `bson:"storageClass,omitempty" json:"storageClass,omitempty" yaml:"storageClass,omitempty"`

	// InsecureSkipTLSVerify disables client verification of the server's
	// certificate chain and host name
	InsecureSkipTLSVerify bool `bson:"insecureSkipTLSVerify" json:"insecureSkipTLSVerify" yaml:"insecureSkipTLSVerify"`

	// DebugLogLevels enables AWS SDK debug logging (sub)levels. Available options:
	// LogDebug, Signing, HTTPBody, RequestRetries, RequestErrors, EventStreamBody
	//
	// Any sub levels will enable LogDebug level accordingly to AWS SDK Go module behavior
	// https://pkg.go.dev/github.com/aws/aws-sdk-go@v1.40.7/aws#LogLevelType
	DebugLogLevels string `bson:"debugLogLevels,omitempty" json:"debugLogLevels,omitempty" yaml:"debugLogLevels,omitempty"`

	// Retryer is configuration for client.DefaultRetryer
	// https://pkg.go.dev/github.com/aws/aws-sdk-go/aws/client#DefaultRetryer
	Retryer *Retryer `bson:"retryer,omitempty" json:"retryer,omitempty" yaml:"retryer,omitempty"`
}

type Retryer struct {
	// Num max Retries is the number of max retries that will be performed.
	// https://pkg.go.dev/github.com/aws/aws-sdk-go/aws/client#DefaultRetryer.NumMaxRetries
	NumMaxRetries int `bson:"numMaxRetries" json:"numMaxRetries" yaml:"numMaxRetries"`

	// MinRetryDelay is the minimum retry delay after which retry will be performed.
	// https://pkg.go.dev/github.com/aws/aws-sdk-go/aws/client#DefaultRetryer.MinRetryDelay
	MinRetryDelay time.Duration `bson:"minRetryDelay" json:"minRetryDelay" yaml:"minRetryDelay"`

	// MaxRetryDelay is the maximum retry delay before which retry must be performed.
	// https://pkg.go.dev/github.com/aws/aws-sdk-go/aws/client#DefaultRetryer.MaxRetryDelay
	MaxRetryDelay time.Duration `bson:"maxRetryDelay" json:"maxRetryDelay" yaml:"maxRetryDelay"`
}

type SDKDebugLogLevel string

const (
	LogDebug        SDKDebugLogLevel = "LogDebug"
	Signing         SDKDebugLogLevel = "Signing"
	HTTPBody        SDKDebugLogLevel = "HTTPBody"
	RequestRetries  SDKDebugLogLevel = "RequestRetries"
	RequestErrors   SDKDebugLogLevel = "RequestErrors"
	EventStreamBody SDKDebugLogLevel = "EventStreamBody"
)

// SDKLogLevel returns the appropriate AWS SDK debug logging level. If the level
// is not recognized, returns aws.LogLevelType(0)
func (l SDKDebugLogLevel) SDKLogLevel() aws.LogLevelType {
	switch l {
	case LogDebug:
		return aws.LogDebug
	case Signing:
		return aws.LogDebugWithSigning
	case HTTPBody:
		return aws.LogDebugWithHTTPBody
	case RequestRetries:
		return aws.LogDebugWithRequestRetries
	case RequestErrors:
		return aws.LogDebugWithRequestErrors
	case EventStreamBody:
		return aws.LogDebugWithEventStreamBody
	}

	return aws.LogLevelType(0)
}

type AWSsse struct {
	// Used to specify the SSE algorithm used when keys are managed by the server
	SseAlgorithm string `bson:"sseAlgorithm" json:"sseAlgorithm" yaml:"sseAlgorithm"`
	KmsKeyID     string `bson:"kmsKeyID" json:"kmsKeyID" yaml:"kmsKeyID"`
	// Used to specify SSE-C style encryption. For Amazon S3 SseCustomerAlgorithm must be 'AES256'
	// see https://docs.aws.amazon.com/AmazonS3/latest/userguide/ServerSideEncryptionCustomerKeys.html
	SseCustomerAlgorithm string `bson:"sseCustomerAlgorithm" json:"sseCustomerAlgorithm" yaml:"sseCustomerAlgorithm"`
	// If SseCustomerAlgorithm is set, this must be a base64 encoded key compatible with the algorithm
	// specified in the SseCustomerAlgorithm field.
	SseCustomerKey string `bson:"sseCustomerKey" json:"sseCustomerKey" yaml:"sseCustomerKey"`
}

func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}

	rv := *cfg
	rv.EndpointURLMap = maps.Clone(cfg.EndpointURLMap)
	if cfg.ForcePathStyle != nil {
		a := *cfg.ForcePathStyle
		rv.ForcePathStyle = &a
	}
	if cfg.ServerSideEncryption != nil {
		a := *cfg.ServerSideEncryption
		rv.ServerSideEncryption = &a
	}
	if cfg.Retryer != nil {
		a := *cfg.Retryer
		rv.Retryer = &a
	}

	return &rv
}

func (cfg *Config) Equal(other *Config) bool {
	if cfg == nil || other == nil {
		return cfg == other
	}

	if cfg.Region != other.Region {
		return false
	}
	if cfg.EndpointURL != other.EndpointURL {
		return false
	}
	if !maps.Equal(cfg.EndpointURLMap, other.EndpointURLMap) {
		return false
	}
	if cfg.Bucket != other.Bucket {
		return false
	}
	if cfg.Prefix != other.Prefix {
		return false
	}
	if cfg.StorageClass != other.StorageClass {
		return false
	}

	lhs, rhs := true, true
	if cfg.ForcePathStyle != nil {
		lhs = *cfg.ForcePathStyle
	}
	if other.ForcePathStyle != nil {
		rhs = *other.ForcePathStyle
	}
	if lhs != rhs {
		return false
	}

	// TODO: check only required fields
	if !reflect.DeepEqual(cfg.Credentials, other.Credentials) {
		return false
	}
	// TODO: check only required fields
	if !reflect.DeepEqual(cfg.ServerSideEncryption, other.ServerSideEncryption) {
		return false
	}

	return true
}

func (cfg *Config) Cast() error {
	if cfg.Region == "" {
		cfg.Region = defaultS3Region
	}
	if cfg.ForcePathStyle == nil {
		cfg.ForcePathStyle = aws.Bool(true)
	}
	if cfg.MaxUploadParts <= 0 {
		cfg.MaxUploadParts = s3manager.MaxUploadParts
	}
	if cfg.StorageClass == "" {
		cfg.StorageClass = s3.StorageClassStandard
	}

	if cfg.Retryer != nil {
		if cfg.Retryer.MinRetryDelay == 0 {
			cfg.Retryer.MinRetryDelay = client.DefaultRetryerMinRetryDelay
		}
		if cfg.Retryer.MaxRetryDelay == 0 {
			cfg.Retryer.MaxRetryDelay = client.DefaultRetryerMaxRetryDelay
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
	return ep
}

// SDKLogLevel returns AWS SDK log level value from comma-separated
// SDKDebugLogLevel values string. If the string does not contain a valid value,
// returns aws.LogOff.
//
// If the string is incorrect formatted, prints warnings to the io.Writer.
// Passing nil as the io.Writer will discard any warnings.
func SDKLogLevel(levels string, out io.Writer) aws.LogLevelType {
	if out == nil {
		out = io.Discard
	}

	var logLevel aws.LogLevelType

	for _, lvl := range strings.Split(levels, ",") {
		lvl = strings.TrimSpace(lvl)
		if lvl == "" {
			continue
		}

		l := SDKDebugLogLevel(lvl).SDKLogLevel()
		if l == 0 {
			fmt.Fprintf(out, "Warning: S3 client debug log level: unsupported %q\n", lvl)
			continue
		}

		logLevel |= l
	}

	if logLevel == 0 {
		logLevel = aws.LogOff
	}

	return logLevel
}

type Credentials struct {
	AccessKeyID     string `bson:"access-key-id" json:"access-key-id,omitempty" yaml:"access-key-id,omitempty"`
	SecretAccessKey string `bson:"secret-access-key" json:"secret-access-key,omitempty" yaml:"secret-access-key,omitempty"`
	SessionToken    string `bson:"session-token" json:"session-token,omitempty" yaml:"session-token,omitempty"`
	Vault           struct {
		Server string `bson:"server" json:"server,omitempty" yaml:"server"`
		Secret string `bson:"secret" json:"secret,omitempty" yaml:"secret"`
		Token  string `bson:"token" json:"token,omitempty" yaml:"token"`
	} `bson:"vault" json:"vault" yaml:"vault,omitempty"`
}

type S3 struct {
	opts *Config
	node string
	log  log.LogEvent
	s3s  *s3.S3

	d *Download // default downloader for small files
}

func New(opts *Config, node string, l log.LogEvent) (*S3, error) {
	err := opts.Cast()
	if err != nil {
		return nil, errors.Wrap(err, "cast options")
	}
	if l == nil {
		l = log.DiscardEvent
	}

	s := &S3{
		opts: opts,
		log:  l,
		node: node,
	}

	s.s3s, err = s.s3session()
	if err != nil {
		return nil, errors.Wrap(err, "AWS session")
	}

	s.d = &Download{
		s3:       s,
		arenas:   []*arena{newArena(downloadChuckSizeDefault, downloadChuckSizeDefault)},
		spanSize: downloadChuckSizeDefault,
		cc:       1,
	}

	return s, nil
}

const defaultPartSize int64 = 10 * 1024 * 1024 // 10Mb

func (*S3) Type() storage.Type {
	return storage.S3
}

func (s *S3) Save(name string, data io.Reader, sizeb int64) error {
	awsSession, err := s.session()
	if err != nil {
		return errors.Wrap(err, "create AWS session")
	}
	cc := runtime.NumCPU() / 2
	if cc == 0 {
		cc = 1
	}

	uplInput := &s3manager.UploadInput{
		Bucket:       aws.String(s.opts.Bucket),
		Key:          aws.String(path.Join(s.opts.Prefix, name)),
		Body:         data,
		StorageClass: &s.opts.StorageClass,
	}

	sse := s.opts.ServerSideEncryption
	if sse != nil {
		if sse.SseAlgorithm == s3.ServerSideEncryptionAes256 {
			uplInput.ServerSideEncryption = aws.String(sse.SseAlgorithm)
		} else if sse.SseAlgorithm == s3.ServerSideEncryptionAwsKms {
			uplInput.ServerSideEncryption = aws.String(sse.SseAlgorithm)
			uplInput.SSEKMSKeyId = aws.String(sse.KmsKeyID)
		} else if sse.SseCustomerAlgorithm != "" {
			uplInput.SSECustomerAlgorithm = aws.String(sse.SseCustomerAlgorithm)
			decodedKey, err := base64.StdEncoding.DecodeString(sse.SseCustomerKey)
			uplInput.SSECustomerKey = aws.String(string(decodedKey))
			if err != nil {
				return errors.Wrap(err, "SseCustomerAlgorithm specified with invalid SseCustomerKey")
			}
			keyMD5 := md5.Sum(decodedKey)
			uplInput.SSECustomerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(keyMD5[:]))
		}
	}

	// MaxUploadParts is 1e4 so with PartSize 10Mb the max allowed file size
	// would be ~ 97.6Gb. Hence if the file size is bigger we're enlarging PartSize
	// so PartSize * MaxUploadParts could fit the file.
	// If calculated PartSize is smaller than the default we leave the default.
	// If UploadPartSize option was set we use it instead of the default. Even
	// with the UploadPartSize set the calculated PartSize woulbe used if it's bigger.
	partSize := defaultPartSize
	if s.opts.UploadPartSize > 0 {
		if s.opts.UploadPartSize < int(s3manager.MinUploadPartSize) {
			s.opts.UploadPartSize = int(s3manager.MinUploadPartSize)
		}

		partSize = int64(s.opts.UploadPartSize)
	}
	if sizeb > 0 {
		ps := sizeb / s3manager.MaxUploadParts * 11 / 10 // add 10% just in case
		if ps > partSize {
			partSize = ps
		}
	}

	if s.log != nil {
		s.log.Debug("uploading %q [size hint: %v (%v); part size: %v (%v)]",
			name,
			sizeb,
			storage.PrettySize(sizeb),
			partSize,
			storage.PrettySize(partSize))
	}

	_, err = s3manager.NewUploader(awsSession, func(u *s3manager.Uploader) {
		u.MaxUploadParts = s.opts.MaxUploadParts
		u.PartSize = partSize      // 10MB part size
		u.LeavePartsOnError = true // Don't delete the parts if the upload fails.
		u.Concurrency = cc

		u.RequestOptions = append(u.RequestOptions, func(r *request.Request) {
			if s.opts.Retryer != nil {
				r.Retryer = client.DefaultRetryer{
					NumMaxRetries: s.opts.Retryer.NumMaxRetries,
					MinRetryDelay: s.opts.Retryer.MinRetryDelay,
					MaxRetryDelay: s.opts.Retryer.MaxRetryDelay,
				}
			}
		})
	}).Upload(uplInput)
	return errors.Wrap(err, "upload to S3")
}

func (s *S3) List(prefix, suffix string) ([]storage.FileInfo, error) {
	prfx := path.Join(s.opts.Prefix, prefix)

	if prfx != "" && !strings.HasSuffix(prfx, "/") {
		prfx += "/"
	}

	lparams := &s3.ListObjectsV2Input{
		Bucket: aws.String(s.opts.Bucket),
	}

	if prfx != "" {
		lparams.Prefix = aws.String(prfx)
	}

	var files []storage.FileInfo
	err := s.s3s.ListObjectsV2Pages(lparams,
		func(page *s3.ListObjectsV2Output, lastPage bool) bool {
			for _, o := range page.Contents {
				f := aws.StringValue(o.Key)
				f = strings.TrimPrefix(f, aws.StringValue(lparams.Prefix))
				if len(f) == 0 {
					continue
				}
				if f[0] == '/' {
					f = f[1:]
				}

				if strings.HasSuffix(f, suffix) {
					files = append(files, storage.FileInfo{
						Name: f,
						Size: aws.Int64Value(o.Size),
					})
				}
			}
			return true
		})
	if err != nil {
		return nil, err
	}

	return files, nil
}

func (s *S3) Copy(src, dst string) error {
	copyOpts := &s3.CopyObjectInput{
		Bucket:     aws.String(s.opts.Bucket),
		CopySource: aws.String(path.Join(s.opts.Bucket, s.opts.Prefix, src)),
		Key:        aws.String(path.Join(s.opts.Prefix, dst)),
	}

	sse := s.opts.ServerSideEncryption
	if sse != nil {
		if sse.SseAlgorithm == s3.ServerSideEncryptionAwsKms {
			copyOpts.ServerSideEncryption = aws.String(sse.SseAlgorithm)
			copyOpts.SSEKMSKeyId = aws.String(sse.KmsKeyID)
		} else if sse.SseCustomerAlgorithm != "" {
			copyOpts.SSECustomerAlgorithm = aws.String(sse.SseCustomerAlgorithm)
			decodedKey, err := base64.StdEncoding.DecodeString(sse.SseCustomerKey)
			copyOpts.SSECustomerKey = aws.String(string(decodedKey))
			if err != nil {
				return errors.Wrap(err, "SseCustomerAlgorithm specified with invalid SseCustomerKey")
			}
			keyMD5 := md5.Sum(decodedKey)
			copyOpts.SSECustomerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(keyMD5[:]))

			copyOpts.CopySourceSSECustomerAlgorithm = copyOpts.SSECustomerAlgorithm
			copyOpts.CopySourceSSECustomerKey = copyOpts.SSECustomerKey
			copyOpts.CopySourceSSECustomerKeyMD5 = copyOpts.SSECustomerKeyMD5
		}
	}

	_, err := s.s3s.CopyObject(copyOpts)

	return err
}

func (s *S3) FileStat(name string) (storage.FileInfo, error) {
	inf := storage.FileInfo{}

	headOpts := &s3.HeadObjectInput{
		Bucket: aws.String(s.opts.Bucket),
		Key:    aws.String(path.Join(s.opts.Prefix, name)),
	}

	sse := s.opts.ServerSideEncryption
	if sse != nil && sse.SseCustomerAlgorithm != "" {
		headOpts.SSECustomerAlgorithm = aws.String(sse.SseCustomerAlgorithm)
		decodedKey, err := base64.StdEncoding.DecodeString(sse.SseCustomerKey)
		headOpts.SSECustomerKey = aws.String(string(decodedKey))
		if err != nil {
			return inf, errors.Wrap(err, "SseCustomerAlgorithm specified with invalid SseCustomerKey")
		}
		keyMD5 := md5.Sum(decodedKey)
		headOpts.SSECustomerKeyMD5 = aws.String(base64.StdEncoding.EncodeToString(keyMD5[:]))
	}

	h, err := s.s3s.HeadObject(headOpts)
	if err != nil {
		//nolint:errorlint
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == "NotFound" {
			return inf, storage.ErrNotExist
		}

		return inf, errors.Wrap(err, "get S3 object header")
	}
	inf.Name = name
	inf.Size = aws.Int64Value(h.ContentLength)

	if inf.Size == 0 {
		return inf, storage.ErrEmpty
	}
	if aws.BoolValue(h.DeleteMarker) {
		return inf, errors.New("file has delete marker")
	}

	return inf, nil
}

// Delete deletes given file.
// It returns storage.ErrNotExist if a file isn't exists
func (s *S3) Delete(name string) error {
	_, err := s.s3s.DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.opts.Bucket),
		Key:    aws.String(path.Join(s.opts.Prefix, name)),
	})
	if err != nil {
		//nolint:errorlint
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == s3.ErrCodeNoSuchKey {
			return storage.ErrNotExist
		}
		return errors.Wrapf(err, "delete '%s/%s' file from S3", s.opts.Bucket, name)
	}

	return nil
}

func (s *S3) s3session() (*s3.S3, error) {
	sess, err := s.session()
	if err != nil {
		return nil, errors.Wrap(err, "create aws session")
	}

	return s3.New(sess), nil
}

func (s *S3) session() (*session.Session, error) {
	var providers []credentials.Provider

	// if we have credentials, set them first in the providers list
	if s.opts.Credentials.AccessKeyID != "" && s.opts.Credentials.SecretAccessKey != "" {
		providers = append(providers, &credentials.StaticProvider{Value: credentials.Value{
			AccessKeyID:     s.opts.Credentials.AccessKeyID,
			SecretAccessKey: s.opts.Credentials.SecretAccessKey,
			SessionToken:    s.opts.Credentials.SessionToken,
		}})
	}

	awsSession, err := session.NewSession()
	if err != nil {
		return nil, errors.Wrap(err, "new session")
	}

	// allow fetching credentials from env variables and ec2 metadata endpoint
	providers = append(providers, &credentials.EnvProvider{})

	// If defined, IRSA-related credentials should have the priority over any potential role attached to the EC2.
	awsRoleARN := os.Getenv("AWS_ROLE_ARN")
	awsTokenFile := os.Getenv("AWS_WEB_IDENTITY_TOKEN_FILE")

	if awsRoleARN != "" && awsTokenFile != "" {
		tokenFetcher := stscreds.FetchTokenPath(awsTokenFile)

		providers = append(providers, stscreds.NewWebIdentityRoleProviderWithOptions(
			sts.New(awsSession),
			awsRoleARN,
			"",
			tokenFetcher,
		))
	}

	httpClient := &http.Client{}
	if s.opts.InsecureSkipTLSVerify {
		httpClient = &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
			},
		}
	}

	cfg := &aws.Config{
		Region:           aws.String(s.opts.Region),
		Endpoint:         aws.String(s.opts.resolveEndpointURL(s.node)),
		S3ForcePathStyle: s.opts.ForcePathStyle,
		HTTPClient:       httpClient,
		LogLevel:         aws.LogLevel(SDKLogLevel(s.opts.DebugLogLevels, nil)),
		Logger:           awsLogger(s.log),
	}

	// fetch credentials from remote endpoints like EC2 or ECS roles
	providers = append(providers, defaults.RemoteCredProvider(*cfg, defaults.Handlers()))

	cfg.Credentials = credentials.NewChainCredentials(providers)

	return session.NewSession(cfg)
}

func awsLogger(l log.LogEvent) aws.Logger {
	if l == nil {
		return aws.NewDefaultLogger()
	}

	return aws.LoggerFunc(func(xs ...interface{}) {
		if len(xs) == 0 {
			return
		}

		msg := "%v"
		for i := len(xs) - 1; i != 0; i++ {
			msg += " %v"
		}

		l.Debug(msg, xs...)
	})
}
