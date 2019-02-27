package awsutils

import (
	"fmt"
	"io"
	"log"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/percona/percona-backup-mongodb/storage"
	"github.com/pkg/errors"
)

var (
	FileNotFoundError = fmt.Errorf("File not found")
	awsSession        *session.Session
	lock              = &sync.Mutex{}
)

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

func DownloadFile(svc *s3.S3, bucket, file string, writer io.WriterAt) (int64, error) {
	downloader := s3manager.NewDownloaderWithClient(svc)

	input := &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(file),
	}

	return downloader.Download(writer, input)
}

func EmptyBucket(svc *s3.S3, bucket string) error {
	iter := s3manager.NewDeleteListIterator(svc, &s3.ListObjectsInput{
		Bucket: aws.String(bucket),
	})

	if err := s3manager.NewBatchDeleteWithClient(svc).Delete(aws.BackgroundContext(), iter); err != nil {
		return errors.Wrapf(err, "Unable to delete objects from bucket %q", bucket)
	}

	return nil
}

func GetAWSSession() (*session.Session, error) {
	// Initialize a session in us-west-2 that the SDK will use to load
	// credentials from the shared credentials file ~/.aws/credentials.
	var err error
	lock.Lock()
	defer lock.Unlock()
	if awsSession == nil {
		awsSession, err = session.NewSession(&aws.Config{})
	}
	if err != nil {
		return nil, err
	}
	return awsSession, nil
}

func GetAWSSessionFromStorage(opts storage.S3) (*session.Session, error) {
	token := ""
	sess, err := session.NewSession(&aws.Config{
		Region:           aws.String(opts.Region),
		Endpoint:         aws.String(opts.EndpointURL),
		Credentials:      credentials.NewStaticCredentials(opts.Credentials.AccessKeyID, opts.Credentials.SecretAccessKey, token),
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
	resp, err := svc.ListObjects(&s3.ListObjectsInput{Bucket: aws.String(bucket)})
	if err != nil {
		return nil, err
	}

	for _, item := range resp.Contents {
		if *item.Key == filename {
			return item, nil
		}
	}
	return nil, FileNotFoundError
}

func UploadFileToS3(sess *session.Session, fr io.Reader, bucket, filename string) error {
	svc := s3.New(sess)
	input := &s3.PutObjectInput{
		Body:   aws.ReadSeekCloser(fr),
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
