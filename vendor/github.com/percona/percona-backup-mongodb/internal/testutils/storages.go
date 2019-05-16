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
	"github.com/pkg/errors"
)

var (
	storages *storage.Storages
)

func initialize() {
	tmpDir := filepath.Join(os.TempDir(), "dump_test")
	bucket := RandomBucket()
	minioEndpoint := os.Getenv("MINIO_ENDPOINT")

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
			"minio": {
				Type: "s3",
				S3: storage.S3{
					Region:      "us-west-2",
					EndpointURL: minioEndpoint,
					Bucket:      bucket,
					Credentials: storage.Credentials{
						AccessKeyID:     os.Getenv("MINIO_ACCESS_KEY_ID"),
						SecretAccessKey: os.Getenv("MINIO_SECRET_ACCESS_KEY"),
					},
				},
				Filesystem: storage.Filesystem{},
			},
		},
	}

	createTempDir(storages.Storages["local-filesystem"].Filesystem.Path)
	if err := createTempBucket(storages.Storages["s3-us-west"].S3); err != nil {
		panic(err)
	}
	if err := createTempBucket(storages.Storages["minio"].S3); err != nil {
		panic(err)
	}
}

func TestingStorages() *storage.Storages {
	if storages == nil {
		initialize()
	}
	return storages
}

func createTempDir(tmpDir string) {
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

	exists, err := awsutils.BucketExists(svc, stg.Bucket)
	if err != nil {
		return err
	}
	if !exists {
		if err := awsutils.CreateBucket(svc, stg.Bucket); err != nil {
			return err
		}
	}
	return nil
}

func RandomBucket() string {
	rand.Seed(time.Now().UnixNano())
	return fmt.Sprintf("pbm-test-bucket-%04d", rand.Int63n(10000))
}

func CleanTempDirAndBucket() error {
	if storages == nil { // not used even once
		return nil
	}
	path := storages.Storages["local-filesystem"].Filesystem.Path
	err := os.RemoveAll(path)
	if err != nil {
		log.Printf("cannot delete temporary directory %q", path)
	}

	stg, _ := storages.Get("s3-us-west")
	sess, err := awsutils.GetAWSSessionFromStorage(stg.S3)
	if err != nil {
		return errors.Wrap(err, "cannot get S3 session")
	}

	svc := s3.New(sess)

	bucket := storages.Storages["s3-us-west"].S3.Bucket
	exists, err := awsutils.BucketExists(svc, bucket)
	if err != nil {
		return errors.Wrapf(err, "cannot check if the bucket %q exists", bucket)
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
