package gcs

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"hash/crc32"
	"io"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

type hmacClient struct {
	client *minio.Client
	cfg    *Config
	log    log.LogEvent
}

func newHMACClient(cfg *Config, l log.LogEvent) (*hmacClient, error) {
	if cfg.Credentials.HMACAccessKey == "" || cfg.Credentials.HMACSecret == "" {
		return nil, errors.New("HMACAccessKey and HMACSecret are required for HMAC GCS credentials")
	}

	if cfg.Retryer != nil {
		if cfg.Retryer.BackoffInitial > 0 {
			minio.DefaultRetryUnit = cfg.Retryer.BackoffInitial
		}
		if cfg.Retryer.BackoffMax > 0 {
			minio.DefaultRetryCap = cfg.Retryer.BackoffMax
		}
	}

	minioClient, err := minio.New(gcsEndpointURL, &minio.Options{
		Creds: credentials.NewStaticV2(cfg.Credentials.HMACAccessKey, cfg.Credentials.HMACSecret, ""),
	})
	if err != nil {
		return nil, errors.Wrap(err, "create minio client for GCS HMAC")
	}

	return &hmacClient{
		client: minioClient,
		cfg:    cfg,
		log:    l,
	}, nil
}

func (h hmacClient) save(name string, data io.Reader, options ...storage.Option) error {
	opts := storage.GetDefaultOpts()
	for _, opt := range options {
		if err := opt(opts); err != nil {
			return errors.Wrap(err, "processing options for save")
		}
	}

	partSize := storage.ComputePartSize(
		opts.Size,
		10<<20, // default: 10 MiB
		5<<20,  // min GCS XML part size
		10_000,
		int64(h.cfg.ChunkSize),
	)

	if h.log != nil && opts.UseLogger {
		h.log.Debug(`uploading %q [size hint: %v (%v); part size: %v (%v)]`,
			name,
			opts.Size, storage.PrettySize(opts.Size),
			partSize, storage.PrettySize(partSize))
	}

	crc := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	dataWithCRC := io.TeeReader(data, crc)

	putOpts := minio.PutObjectOptions{
		PartSize:   uint64(partSize),
		NumThreads: uint(max(runtime.NumCPU()/2, 1)),
	}
	putInfo, err := h.client.PutObject(
		context.Background(),
		h.cfg.Bucket,
		path.Join(h.cfg.Prefix, name),
		dataWithCRC,
		-1,
		putOpts,
	)
	if err != nil {
		return errors.Wrap(err, "PutObject")
	}

	localCRC := crcToBase64(crc.Sum32())
	if putInfo.ChecksumCRC32C != localCRC {
		return errors.Errorf("wrong CRC after uploading %s, GCS: %s, PBM: %s",
			name, putInfo.ChecksumCRC32C, localCRC)
	}

	return nil
}

func (h hmacClient) fileStat(name string) (storage.FileInfo, error) {
	objectName := path.Join(h.cfg.Prefix, name)

	object, err := h.client.StatObject(context.Background(), h.cfg.Bucket, objectName, minio.StatObjectOptions{})
	if err != nil {
		respErr := minio.ToErrorResponse(err)
		if respErr.Code == "NoSuchKey" || respErr.Code == "NotFound" {
			return storage.FileInfo{}, storage.ErrNotExist
		}

		return storage.FileInfo{}, errors.Wrap(err, "get object")
	}

	inf := storage.FileInfo{
		Name: name,
		Size: object.Size,
	}

	if inf.Size == 0 {
		return inf, storage.ErrEmpty
	}

	return inf, nil
}

func (h hmacClient) list(prefix, suffix string) ([]storage.FileInfo, error) {
	ctx := context.Background()

	var files []storage.FileInfo

	for obj := range h.client.ListObjects(ctx, h.cfg.Bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, errors.Wrap(obj.Err, "list objects")
		}

		name := strings.TrimPrefix(obj.Key, prefix)
		if len(name) > 0 && name[0] == '/' {
			name = name[1:]
		}

		if suffix != "" && !strings.HasSuffix(name, suffix) {
			continue
		}

		files = append(files, storage.FileInfo{
			Name: name,
			Size: obj.Size,
		})
	}

	return files, nil
}

func (h hmacClient) delete(name string) error {
	ctx := context.Background()
	objectName := path.Join(h.cfg.Prefix, name)

	err := h.client.RemoveObject(ctx, h.cfg.Bucket, objectName, minio.RemoveObjectOptions{})
	if err != nil {
		respErr := minio.ToErrorResponse(err)
		if respErr.Code == "NoSuchKey" || respErr.Code == "NotFound" {
			return storage.ErrNotExist
		}

		return err
	}

	return nil
}

func (h hmacClient) copy(src, dst string) error {
	ctx := context.Background()

	_, err := h.client.CopyObject(ctx,
		minio.CopyDestOptions{
			Bucket: h.cfg.Bucket,
			Object: path.Join(h.cfg.Prefix, dst),
		},
		minio.CopySrcOptions{
			Bucket: h.cfg.Bucket,
			Object: path.Join(h.cfg.Prefix, src),
		},
	)

	return err
}

func (h hmacClient) getPartialObject(name string, buf *storage.Arena, start, length int64) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*60)
	defer cancel()

	objectName := path.Join(h.cfg.Prefix, name)

	opts := minio.GetObjectOptions{}

	err := opts.SetRange(start, start+length-1)
	if err != nil {
		return nil, errors.Wrap(err, "failed to set range on GetObjectOptions")
	}

	object, err := h.client.GetObject(ctx, h.cfg.Bucket, objectName, opts)
	if err != nil {
		respErr := minio.ToErrorResponse(err)
		if respErr.Code == "NoSuchKey" || respErr.Code == "InvalidRange" {
			return nil, io.EOF
		}

		return nil, storage.GetObjError{Err: err}
	}
	defer object.Close()

	ch := buf.GetSpan()
	_, err = io.CopyBuffer(ch, object, buf.CpBuf)
	if err != nil {
		ch.Close()
		return nil, errors.Wrap(err, "copy")
	}

	return ch, nil
}

func crcToBase64(v uint32) string {
	buf := make([]byte, 4)
	binary.BigEndian.PutUint32(buf, v)
	return base64.StdEncoding.EncodeToString(buf)
}
