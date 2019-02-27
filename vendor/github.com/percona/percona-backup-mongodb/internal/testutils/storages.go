package testutils

import (
	"fmt"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/percona/percona-backup-mongodb/internal/awsutils"
	"github.com/percona/percona-backup-mongodb/storage"
)

var (
	storages *storage.Storages
	tmpDir   string
	bucket   string
)

func init() {
	tmpDir = filepath.Join(os.TempDir(), "dump_test")
	bucket = RandomBucket()

	storages = &storage.Storages{
		Storages: map[string]storage.Storage{
			"s3-us-west": {
				Type: "s3",
				S3: storage.S3{
					Region: "us-west-2",
					Bucket: RandomBucket(),
					Credentials: storage.Credentials{
						AccessKeyID:     os.Getenv("AWS_ACCESS_KEY_ID"),
						SecretAccessKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
					},
				},
				Filesystem: storage.Filesystem{},
			},
			"local-filesystem": {
				Type: "filesystem",
				Filesystem: storage.Filesystem{
					Path: tmpDir,
				},
			},
		},
	}

	createTempDir()
	createTempBucket(storages.Storages["s3-us-west"].S3)
}

func TestingStorages() *storage.Storages {
	return storages
}

func createTempDir() {
	if _, err := os.Stat(tmpDir); os.IsNotExist(err) {
		os.MkdirAll(tmpDir, os.ModePerm)
	}
	os.RemoveAll(filepath.Join(tmpDir, "*"))
}

func createTempBucket(stg storage.S3) error {
	sess, err := awsutils.GetAWSSessionFromStorage(stg)
	if err != nil {
		return err
	}

	svc := s3.New(sess)

	exists, err := BucketExists(svc, bucket)
	if err != nil {
		return err
	}
	if !exists {
		if err := CreateBucket(svc, bucket); err != nil {
			return err
		}
	}
	return nil
}

func RandomBucket() string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("pbm-test-bucket-%05d", 99999)
}

func CleanTempDirAndBucket() error {
	err := os.RemoveAll(tmpDir)
	if err != nil {
		log.Printf("cannot delete temporary directory %q", tmpDir)
	}

	stg, _ := storages.Get("s3-us-west")
	sess, err := awsutils.GetAWSSessionFromStorage(stg.S3)
	if err != nil {
		return err
	}

	svc := s3.New(sess)

	exists, err := BucketExists(svc, bucket)
	if err != nil {
		return err
	}
	if exists {
		if err := awsutils.EmptyBucket(svc, bucket); err != nil {
			return err
		}
		if err := DeleteBucket(svc, bucket); err != nil {
			return err
		}
	}
	return nil
}
