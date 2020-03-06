package restore

import (
	"compress/gzip"
	"io"
	"io/ioutil"
	"os"
	"path"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/golang/snappy"
	"github.com/pierrec/lz4"
	"github.com/pkg/errors"

	"github.com/percona/percona-backup-mongodb/pbm"
)

// Source returns io.ReadCloser for the given storage.
// In case compression are used it alse return io.Closer wich should be used
// to close undelying Reader
func Source(stg pbm.Storage, name string, compression pbm.CompressionType) (io.ReadCloser, io.Closer, error) {
	var (
		rr io.ReadCloser
		rc io.Closer
	)

	switch stg.Type {
	case pbm.StorageFilesystem:
		filepath := path.Join(stg.Filesystem.Path, name)
		fr, err := os.Open(filepath)
		if err != nil {
			return nil, nil, errors.Wrapf(err, "open file '%s'", filepath)
		}
		rr = fr
	case pbm.StorageS3:
		awsSession, err := session.NewSession(&aws.Config{
			Region:   aws.String(stg.S3.Region),
			Endpoint: aws.String(stg.S3.EndpointURL),
			Credentials: credentials.NewStaticCredentials(
				stg.S3.Credentials.AccessKeyID,
				stg.S3.Credentials.SecretAccessKey,
				"",
			),
			S3ForcePathStyle: aws.Bool(true),
		})
		if err != nil {
			return nil, nil, errors.Wrap(err, "cannot create AWS session")
		}

		s3obj, err := s3.New(awsSession).GetObject(&s3.GetObjectInput{
			Bucket: aws.String(stg.S3.Bucket),
			Key:    aws.String(path.Join(stg.S3.Prefix, name)),
		})
		if err != nil {
			return nil, nil, errors.Wrapf(err, "read '%s/%s' file from S3", stg.S3.Bucket, name)
		}
		rr = ioutil.NopCloser(s3obj.Body)
	}

	switch compression {
	case pbm.CompressionTypeGZIP:
		rc = rr
		var err error
		rr, err = gzip.NewReader(rr)
		if err != nil {
			return nil, nil, errors.Wrap(err, "gzip reader")
		}
	case pbm.CompressionTypeLZ4:
		rc = rr
		rr = ioutil.NopCloser(lz4.NewReader(rr))
	case pbm.CompressionTypeSNAPPY:
		rc = rr
		rr = ioutil.NopCloser(snappy.NewReader(rr))
	}

	return rr, rc, nil
}
