package awsutils

import (
	"fmt"
	"io"
	"log"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/client"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/percona/percona-backup-mongodb/storage"
	"github.com/pkg/errors"
)

type FileNotFoundError struct {
	err error
}

func NewFileNotFoundErrorf(args ...string) *FileNotFoundError {
	if len(args) < 1 {
		return &FileNotFoundError{
			err: fmt.Errorf("file not found"),
		}
	}

	return &FileNotFoundError{
		err: fmt.Errorf(args[0], args[1:]),
	}
}

func (f *FileNotFoundError) Error() string {
	return f.err.Error()
}

func IsFileNotFoundError(err error) bool {
	_, ok := err.(*FileNotFoundError)
	return ok
}

func BucketExists(svc *s3.S3, bucketname string) (bool, error) {
	input := &s3.ListBucketsInput{}

	result, err := svc.ListBuckets(input)
	if err != nil {
		return false, err
	}
	for _, bucket := range result.Buckets {
		if *bucket.Name == bucketname {
			return true, nil
		}
	}
	return false, nil
}

func CreateBucket(svc *s3.S3, bucket string) error {
	_, err := svc.CreateBucket(&s3.CreateBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return errors.Wrapf(err, "Unable to create bucket %q", bucket)
	}

	err = svc.WaitUntilBucketExists(&s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return errors.Wrap(err, ("error while waiting the S3 bucket to be created"))
	}
	return nil
}

func DeleteFile(svc *s3.S3, bucket, filename string) error {
	_, err := svc.DeleteObject(&s3.DeleteObjectInput{Bucket: aws.String(bucket), Key: aws.String(filename)})
	if err != nil {
		return errors.Wrapf(err, "unable to delete object %q from bucket %q", filename, bucket)
	}

	err = svc.WaitUntilObjectNotExists(&s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(filename),
	})
	if err != nil {
		return errors.Wrapf(err, "file %s was not deleted from the %s bucket", filename, bucket)
	}
	return nil
}

func DeleteBucket(svc *s3.S3, bucket string) error {
	_, err := svc.DeleteBucket(&s3.DeleteBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return errors.Wrapf(err, "unable to delete bucket %q", bucket)
	}

	err = svc.WaitUntilBucketNotExists(&s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return errors.Wrapf(err, "error occurred while waiting for bucket to be deleted, %s", bucket)
	}
	return nil
}

func DownloadFile(svc s3iface.S3API, bucket, file string, writer io.WriterAt) (int64, error) {
	downloader := s3manager.NewDownloaderWithClient(svc)

	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(file),
	}

	return downloader.Download(writer, input)
}

func EmptyBucket(svc s3iface.S3API, bucket string) error {
	iter := s3manager.NewDeleteListIterator(svc, &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
	})

	if err := s3manager.NewBatchDeleteWithClient(svc).Delete(aws.BackgroundContext(), iter); err != nil {
		return errors.Wrapf(err, "Unable to delete objects from bucket %q", bucket)
	}

	return nil
}

func GetAWSSession() (*session.Session, error) {
	return session.NewSession(&aws.Config{})
}

func GetAWSSessionFromStorage(opts storage.S3) (*session.Session, error) {
	token := ""
	sess, err := session.NewSession(&aws.Config{
		Region:   aws.String(opts.Region),
		Endpoint: aws.String(opts.EndpointURL),
		Credentials: credentials.NewStaticCredentials(
			opts.Credentials.AccessKeyID,
			opts.Credentials.SecretAccessKey,
			token,
		),
		S3ForcePathStyle: aws.Bool(true),
	})
	return sess, err
}

func ListObjects(svc *s3.S3, bucket string) (*s3.ListObjectsOutput, error) {
	input := &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
	}

	return svc.ListObjects(input)
}

func S3Stat(svc *s3.S3, bucket, filename string) (*s3.Object, error) {
	obj := &s3.Object{}
	err := svc.ListObjectsPages(
		&s3.ListObjectsInput{
			Bucket:       aws.String(bucket),
			Prefix:       aws.String(filename),
			RequestPayer: aws.String("requester"),
		},
		func(page *s3.ListObjectsOutput, lastPage bool) bool {
			for _, item := range page.Contents {
				if *item.Key == filename {
					obj = item
					return false
				}
			}
			return true
		},
	)

	if err != nil {
		return nil, err
	}

	if obj != nil {
		return obj, nil
	}
	return nil, nil
}

func UploadFileToS3(sess client.ConfigProvider, fr io.ReadSeeker, bucket, filename string) error {
	svc := s3.New(sess)
	input := &s3.PutObjectInput{
		Body:   fr,
		Bucket: aws.String(bucket),
		Key:    aws.String(filename),
	}

	_, err := svc.PutObject(input)
	if err != nil {
		return err
	}
	return nil
}

func Diag(params ...interface{}) {
	if testing.Verbose() {
		log.Printf(params[0].(string), params[1:]...)
	}
}
