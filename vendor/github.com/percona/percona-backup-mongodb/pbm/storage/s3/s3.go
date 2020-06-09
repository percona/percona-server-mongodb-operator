package s3

import (
	"io"
	"io/ioutil"
	"net/url"
	"path"
	"runtime"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/minio/minio-go"
	"github.com/pkg/errors"
)

const (
	// GCSEndpointURL is the endpoint url for Google Clound Strage service
	GCSEndpointURL = "storage.googleapis.com"

	defaultS3Region = "us-east-1"
)

type Conf struct {
	Provider    S3Provider  `bson:"provider,omitempty" json:"provider,omitempty" yaml:"provider,omitempty"`
	Region      string      `bson:"region" json:"region" yaml:"region"`
	EndpointURL string      `bson:"endpointUrl,omitempty" json:"endpointUrl" yaml:"endpointUrl,omitempty"`
	Bucket      string      `bson:"bucket" json:"bucket" yaml:"bucket"`
	Prefix      string      `bson:"prefix,omitempty" json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Credentials Credentials `bson:"credentials" json:"credentials,omitempty" yaml:"credentials"`
}

func (c *Conf) Cast() error {
	if c.Region == "" {
		c.Region = defaultS3Region
	}
	if c.Provider == S3ProviderUndef {
		c.Provider = S3ProviderAWS
		if c.EndpointURL != "" {
			eu, err := url.Parse(c.EndpointURL)
			if err != nil {
				return errors.Wrap(err, "parse EndpointURL")
			}
			if eu.Host == GCSEndpointURL {
				c.Provider = S3ProviderGCS
			}
		}
	}

	return nil
}

type Credentials struct {
	AccessKeyID     string `bson:"access-key-id" json:"access-key-id,omitempty" yaml:"access-key-id,omitempty"`
	SecretAccessKey string `bson:"secret-access-key" json:"secret-access-key,omitempty" yaml:"secret-access-key,omitempty"`
	Vault           struct {
		Server string `bson:"server" json:"server,omitempty" yaml:"server"`
		Secret string `bson:"secret" json:"secret,omitempty" yaml:"secret"`
		Token  string `bson:"token" json:"token,omitempty" yaml:"token"`
	} `bson:"vault" json:"vault" yaml:"vault,omitempty"`
}

type S3Provider string

const (
	S3ProviderUndef S3Provider = ""
	S3ProviderAWS   S3Provider = "aws"
	S3ProviderGCS   S3Provider = "gcs"
)

type S3 struct {
	opts Conf
}

func New(opts Conf) (*S3, error) {
	err := opts.Cast()
	if err != nil {
		return nil, errors.Wrap(err, "cast options")
	}

	return &S3{
		opts: opts,
	}, nil
}

func (s *S3) Save(name string, data io.Reader) error {
	switch s.opts.Provider {
	default:
		awsSession, err := session.NewSession(&aws.Config{
			Region:   aws.String(s.opts.Region),
			Endpoint: aws.String(s.opts.EndpointURL),
			Credentials: credentials.NewStaticCredentials(
				s.opts.Credentials.AccessKeyID,
				s.opts.Credentials.SecretAccessKey,
				"",
			),
			S3ForcePathStyle: aws.Bool(true),
		})
		if err != nil {
			return errors.Wrap(err, "create AWS session")
		}
		cc := runtime.NumCPU() / 2
		if cc == 0 {
			cc = 1
		}
		_, err = s3manager.NewUploader(awsSession, func(u *s3manager.Uploader) {
			u.PartSize = 10 * 1024 * 1024 // 10MB part size
			u.LeavePartsOnError = true    // Don't delete the parts if the upload fails.
			u.Concurrency = cc
		}).Upload(&s3manager.UploadInput{
			Bucket: aws.String(s.opts.Bucket),
			Key:    aws.String(path.Join(s.opts.Prefix, name)),
			Body:   data,
		})
		return errors.Wrap(err, "upload to S3")
	case S3ProviderGCS:
		// using minio client with GCS because it
		// allows to disable chuncks muiltipertition for upload
		mc, err := minio.NewWithRegion(GCSEndpointURL, s.opts.Credentials.AccessKeyID, s.opts.Credentials.SecretAccessKey, true, s.opts.Region)
		if err != nil {
			return errors.Wrap(err, "NewWithRegion")
		}
		_, err = mc.PutObject(s.opts.Bucket, path.Join(s.opts.Prefix, name), data, -1, minio.PutObjectOptions{})
		return errors.Wrap(err, "upload to GCS")
	}
}

func (s *S3) FilesList(suffix string) ([][]byte, error) {
	awsSession, err := session.NewSession(&aws.Config{
		Region:   aws.String(s.opts.Region),
		Endpoint: aws.String(s.opts.EndpointURL),
		Credentials: credentials.NewStaticCredentials(
			s.opts.Credentials.AccessKeyID,
			s.opts.Credentials.SecretAccessKey,
			"",
		),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		return nil, errors.Wrap(err, "create AWS session")
	}

	lparams := &s3.ListObjectsInput{
		Bucket:    aws.String(s.opts.Bucket),
		Delimiter: aws.String("/"),
	}
	if s.opts.Prefix != "" {
		lparams.Prefix = aws.String(s.opts.Prefix)
		if s.opts.Prefix[len(s.opts.Prefix)-1] != '/' {
			*lparams.Prefix += "/"
		}
	}

	var bcps [][]byte
	var berr error
	awscli := s3.New(awsSession)
	err = awscli.ListObjectsPages(lparams,
		func(page *s3.ListObjectsOutput, lastPage bool) bool {
			for _, o := range page.Contents {
				name := aws.StringValue(o.Key)
				if strings.HasSuffix(name, suffix) {
					s3obj, err := awscli.GetObject(&s3.GetObjectInput{
						Bucket: aws.String(s.opts.Bucket),
						Key:    aws.String(name),
					})
					if err != nil {
						berr = errors.Wrapf(err, "get object '%s'", name)
						return false
					}

					b, err := ioutil.ReadAll(s3obj.Body)
					if err != nil {
						berr = errors.Wrapf(err, "read object '%s'", name)
						return false
					}
					bcps = append(bcps, b)
				}
			}
			return true
		})

	if err != nil {
		return nil, errors.Wrap(err, "get backup list")
	}

	if berr != nil {
		return nil, errors.Wrap(berr, "metadata")
	}

	return bcps, nil
}

func (s *S3) SourceReader(name string) (io.ReadCloser, error) {
	awsSession, err := session.NewSession(&aws.Config{
		Region:   aws.String(s.opts.Region),
		Endpoint: aws.String(s.opts.EndpointURL),
		Credentials: credentials.NewStaticCredentials(
			s.opts.Credentials.AccessKeyID,
			s.opts.Credentials.SecretAccessKey,
			"",
		),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		return nil, errors.Wrap(err, "cannot create AWS session")
	}

	s3obj, err := s3.New(awsSession).GetObject(&s3.GetObjectInput{
		Bucket: aws.String(s.opts.Bucket),
		Key:    aws.String(path.Join(s.opts.Prefix, name)),
	})
	if err != nil {
		return nil, errors.Wrapf(err, "read '%s/%s' file from S3", s.opts.Bucket, name)
	}
	return s3obj.Body, nil
}

func (s *S3) Delete(name string) error {
	awsSession, err := session.NewSession(&aws.Config{
		Region:   aws.String(s.opts.Region),
		Endpoint: aws.String(s.opts.EndpointURL),
		Credentials: credentials.NewStaticCredentials(
			s.opts.Credentials.AccessKeyID,
			s.opts.Credentials.SecretAccessKey,
			"",
		),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		return errors.Wrap(err, "cannot create AWS session")
	}

	_, err = s3.New(awsSession).DeleteObject(&s3.DeleteObjectInput{
		Bucket: aws.String(s.opts.Bucket),
		Key:    aws.String(path.Join(s.opts.Prefix, name)),
	})

	return errors.Wrapf(err, "delete '%s/%s' file from S3", s.opts.Bucket, name)
}
