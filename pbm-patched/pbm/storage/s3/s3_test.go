package s3

import (
	"context"
	"flag"
	"io"
	"net/http"
	"path"
	"runtime"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/minio"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

func TestS3(t *testing.T) {
	ctx := context.Background()

	minioContainer, err := minio.Run(ctx, "minio/minio:RELEASE.2024-08-17T01-24-54Z")
	defer func() {
		if err := testcontainers.TerminateContainer(minioContainer); err != nil {
			t.Fatalf("failed to terminate container: %s", err)
		}
	}()

	if err != nil {
		t.Fatalf("failed to start container: %s", err)
	}

	endpoint, err := minioContainer.Endpoint(ctx, "http")
	if err != nil {
		t.Fatalf("failed to get endpoint: %s", err)
	}

	defaultConfig, err := config.LoadDefaultConfig(ctx,
		config.WithRegion("us-east-1"),
		config.WithBaseEndpoint(endpoint),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider("minioadmin", "minioadmin", ""),
		),
	)
	if err != nil {
		t.Fatalf("failed to load config: %s", err)
	}

	s3Client := s3.NewFromConfig(defaultConfig, func(o *s3.Options) {
		o.UsePathStyle = true
	})

	bucketName := "test-bucket"
	_, err = s3Client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucketName)})
	if err != nil {
		t.Errorf("failed to create bucket: %s", err)
	}

	opts := &Config{
		EndpointURL: endpoint,
		Bucket:      bucketName,
		Credentials: Credentials{
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		},
		Retryer:               &Retryer{},
		ServerSideEncryption:  &AWSsse{},
		InsecureSkipTLSVerify: true,
	}

	stg, err := New(opts, "node", nil)
	if err != nil {
		t.Fatalf("failed to create s3 storage: %s", err)
	}

	storage.RunStorageBaseTests(t, stg, storage.S3)
	storage.RunStorageAPITests(t, stg)
	storage.RunSplitMergeMWTests(t, stg)

	t.Run("with downloader", func(t *testing.T) {
		stg, err := NewWithDownloader(opts, "node", nil, 0, 0, 0)
		if err != nil {
			t.Fatalf("failed to create s3 storage: %s", err)
		}

		storage.RunStorageBaseTests(t, stg, storage.S3)
		storage.RunStorageAPITests(t, stg)
		storage.RunSplitMergeMWTests(t, stg)
	})

	t.Run("default SDKLogLevel for invalid value", func(t *testing.T) {
		logLvl := SDKLogLevel("invalid", nil)
		if logLvl != 0 {
			t.Fatalf("expected SDKLogLevel to be 0, got %v", 0)
		}
	})
}

func TestConfig(t *testing.T) {
	opts := &Config{
		Bucket: "bucketName",
		Prefix: "prefix",
		Credentials: Credentials{
			AccessKeyID:     "minioadmin",
			SecretAccessKey: "minioadmin",
		},
		Retryer: &Retryer{
			NumMaxRetries: 1,
		},
		ServerSideEncryption: &AWSsse{},
	}

	t.Run("Clone", func(t *testing.T) {
		opts.ForcePathStyle = aws.Bool(true)
		clone := opts.Clone()
		if clone == opts {
			t.Error("expected clone to be a different pointer")
		}

		if !opts.Equal(clone) {
			t.Error("expected clone to be equal")
		}

		opts.Bucket = "updatedName"
		if opts.Equal(clone) {
			t.Error("expected clone to be unchanged when updating original")
		}
	})

	t.Run("Equal fails", func(t *testing.T) {
		if opts.Equal(nil) {
			t.Error("expected not to be equal other nil")
		}

		clone := opts.Clone()
		clone.Prefix = "updatedPrefix"
		if opts.Equal(clone) {
			t.Error("expected not to be equal when updating prefix")
		}

		clone = opts.Clone()
		clone.Region = "updatedRegion"
		if opts.Equal(clone) {
			t.Error("expected not to be equal when updating region")
		}

		clone = opts.Clone()
		clone.EndpointURL = "EndpointURL"
		if opts.Equal(clone) {
			t.Error("expected not to be equal when updating EndpointURL")
		}

		clone = opts.Clone()
		clone.EndpointURLMap = map[string]string{"foo": "bar"}
		if opts.Equal(clone) {
			t.Error("expected not to be equal when updating EndpointURLMap")
		}

		clone = opts.Clone()
		clone.Credentials.AccessKeyID = "updating"
		if opts.Equal(clone) {
			t.Error("expected not to be equal when updating credentials")
		}
	})

	t.Run("Cast succeeds", func(t *testing.T) {
		if opts.Region != "" {
			t.Error("Start value is not ''")
		}
		opts.Cast()

		if opts.Region != "us-east-1" {
			t.Error("Default value should be set on Cast")
		}
	})
}

func TestRetryer(t *testing.T) {
	minBackoff := 100 * time.Millisecond
	retryer := NewCustomRetryer(2, minBackoff, 1000*time.Millisecond)

	delay, _ := retryer.RetryDelay(3, errors.New("error"))

	if delay < minBackoff {
		t.Errorf("Expected delay to be at least %s, but got %s", minBackoff, delay)
	}
}

func TestToClientLogMode(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected aws.ClientLogMode
	}{
		{
			name:     "Empty input",
			input:    "",
			expected: 0,
		},
		{
			name:     "Single flag: Signing",
			input:    "Signing",
			expected: aws.LogSigning,
		},
		{
			name:     "Multiple flags with commas",
			input:    "Retries, Request, Response",
			expected: aws.LogRetries | aws.LogRequest | aws.LogResponse,
		},
		{
			name:     "Deprecated LogDebug",
			input:    "LogDebug",
			expected: aws.LogRequest | aws.LogResponse,
		},
		{
			name:     "Deprecated HTTPBody",
			input:    "HTTPBody",
			expected: aws.LogRequestWithBody | aws.LogResponseWithBody,
		},
		{
			name:     "Flags with extra spaces",
			input:    "  Signing  ,   RequestEventMessage,ResponseEventMessage ",
			expected: aws.LogSigning | aws.LogRequestEventMessage | aws.LogResponseEventMessage,
		},
		{
			name:     "Multiple deprecated flags combined",
			input:    "LogDebug, HTTPBody, RequestRetries, RequestErrors, EventStreamBody",
			expected: aws.LogRequest | aws.LogResponse | aws.LogRequestWithBody | aws.LogResponseWithBody | aws.LogRetries,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := toClientLogMode(tt.input)
			if actual != tt.expected {
				t.Errorf("toClientLogMode(%q) = %v, expected %v", tt.input, actual, tt.expected)
			}
		})
	}
}

var (
	fileSize = flag.Int64("file-size", 500, "file size that will be uploaded")
	partSize = flag.Int64("part-size", 10, "part size that will be used to upload file")
)

// BenchmarkS3Upload measures the performance of uploading file on the AWS S3 SDK level.
// It allows specifying --file-size and --part-size flags.
// Example that was used in the microbenchmarking tests:
/*
go test ./pbm/storage/s3 -bench=BenchmarkS3Upload -run=^$ -v \
-benchtime=5x \
-cpu=1,2,4,8  \
-benchmem  \
-file-size=500 \
-part-size=100
*/
func BenchmarkS3Upload(b *testing.B) {
	numThreds := max(runtime.GOMAXPROCS(0), 1)
	fsize := *fileSize * 1024 * 1024
	pSize := *partSize * 1024 * 1024

	region := "eu-central-1"
	bucket := ""
	prefix := ""
	accessKeyID := ""
	secretAccessKey := ""

	cfgOpts := []func(*config.LoadOptions) error{
		config.WithRegion(region),
		config.WithHTTPClient(&http.Client{}),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(accessKeyID, secretAccessKey, ""),
		),
	}
	awsCfg, err := config.LoadDefaultConfig(context.Background(), cfgOpts...)
	if err != nil {
		b.Fatalf("load default aws config: %v", err)
	}
	s3Client := s3.NewFromConfig(awsCfg)
	b.Logf("aws s3 client: file size=%s; part size=%s; NumThreads=%d",
		storage.PrettySize(fsize), storage.PrettySize(pSize), numThreds)

	b.ResetTimer()
	b.SetBytes(fsize)

	for b.Loop() {
		b.StopTimer()
		infR := NewInfiniteCustomReader()
		r := io.LimitReader(infR, fsize)

		fname := time.Now().Format("2006-01-02T15:04:05")
		b.Logf("uploading file: %s ....", fname)

		putInput := &s3.PutObjectInput{
			Bucket:       aws.String(bucket),
			Key:          aws.String(path.Join(prefix, fname)),
			Body:         r,
			StorageClass: types.StorageClass(types.StorageClassStandard),
		}

		b.StartTimer()
		_, err := manager.NewUploader(s3Client, func(u *manager.Uploader) {
			u.PartSize = pSize
			u.LeavePartsOnError = true
			u.Concurrency = numThreds
		}).Upload(context.Background(), putInput)
		if err != nil {
			b.Fatalf("put object: %v", err)
		}
	}
}

// BenchmarkS3StorageSave measures the performance of uploading file on the
// PBM's storage interface level.
// It allows specifying --file-size and --part-size flags.
// Example that was used in the microbenchmarking tests:
/*
go test ./pbm/storage/s3 -bench=BenchmarkS3StorageSave -run=^$ -v \
-benchtime=5x \
-cpu=1,2,4,8  \
-benchmem  \
-file-size=500 \
-part-size=100
*/
func BenchmarkS3StorageSave(b *testing.B) {
	numThreds := max(runtime.GOMAXPROCS(0), 1)
	fsize := *fileSize * 1024 * 1024
	pSize := *partSize * 1024 * 1024

	cfg := &Config{
		Region: "eu-central-1",
		Bucket: "",
		Prefix: "",
		Credentials: Credentials{
			AccessKeyID:     "",
			SecretAccessKey: "",
		},
		UploadPartSize: int(pSize),
	}

	s, err := New(cfg, "", log.DiscardEvent)
	if err != nil {
		b.Fatalf("s3 storage creation: %v", err)
	}
	b.Logf("aws s3 client: file size=%s; part size=%s; NumThreads=%d",
		storage.PrettySize(fsize), storage.PrettySize(pSize), numThreds)

	b.ResetTimer()
	b.SetBytes(fsize)

	for b.Loop() {
		b.StopTimer()
		infR := NewInfiniteCustomReader()
		r := io.LimitReader(infR, fsize)

		fname := time.Now().Format("2006-01-02T15:04:05")
		b.Logf("uploading file: %s ....", fname)

		b.StartTimer()
		err := s.Save(fname, r)
		if err != nil {
			b.Fatalf("save %s: %v", fname, err)
		}
	}
}

func BenchmarkS3StorageList(b *testing.B) {
	cfg := &Config{
		Region: "eu-central-1",
		Bucket: "",
		Prefix: "",
		Credentials: Credentials{
			AccessKeyID:     "",
			SecretAccessKey: "",
		},
	}

	s, err := New(cfg, "", log.DiscardEvent)
	if err != nil {
		b.Fatalf("s3 storage creation: %v", err)
	}

	b.ResetTimer()

	for b.Loop() {
		fis, err := s.List("", "")
		if err != nil {
			b.Fatalf("list: %v", err)
		}
		b.Logf("got %d files", len(fis))
	}
}

func BenchmarkS3StorageFileStat(b *testing.B) {
	cfg := &Config{
		Region: "eu-central-1",
		Bucket: "",
		Prefix: "",
		Credentials: Credentials{
			AccessKeyID:     "",
			SecretAccessKey: "",
		},
	}

	s, err := New(cfg, "", log.DiscardEvent)
	if err != nil {
		b.Fatalf("s3 storage creation: %v", err)
	}

	b.ResetTimer()

	for b.Loop() {
		fi, err := s.FileStat("2025-10-17T17:05:18")
		if err != nil {
			b.Fatalf("file stat: %v", err)
		}
		b.Logf("file stat: %s, %d", fi.Name, fi.Size)
		fi, err = s.FileStat("abc")
		if err != storage.ErrNotExist {
			b.Fatal("files should not exist")
		}
	}
}

type InfiniteCustomReader struct {
	pattern      []byte
	patternIndex int
}

func NewInfiniteCustomReader() *InfiniteCustomReader {
	pattern := []byte{0xAA, 0xBB, 0xCC, 0xDD, 0xEE, 0xFF, 0x11, 0x22}

	return &InfiniteCustomReader{
		pattern:      pattern,
		patternIndex: 0,
	}
}

func (r *InfiniteCustomReader) Read(p []byte) (int, error) {
	readLen := len(p)

	for i := range readLen {
		p[i] = r.pattern[r.patternIndex]
		r.patternIndex = (r.patternIndex + 1) % len(r.pattern)
	}

	return readLen, nil
}
