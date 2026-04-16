package mio

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"path"
	"runtime"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

const (
	// https://docs.min.io/enterprise/aistor-object-store/reference/aistor-server/thresholds/#s3-api-limits
	maxUploadParts        = 10000
	defaultPartSize int64 = 10 * 1024 * 1024 // 10Mb
	minPartSize     int64 = 1024 * 1024 * 5  // 5Mb

	// minio allows 50TiB, sensible default is aligned with S3
	defaultMaxObjSizeGB = 5018 // 4.9 TB

	defaultMaxRetries = 10
)

type Minio struct {
	cfg  *Config
	node string
	log  log.LogEvent
	cl   *minio.Client

	d *Download
}

func New(cfg *Config, node string, l log.LogEvent) (storage.Storage, error) {
	m, err := newMinio(cfg, node, l)
	if err != nil {
		return nil, err
	}

	// default downloader for small files
	m.d = &Download{
		arenas: []*storage.Arena{storage.NewArena(
			storage.DownloadChuckSizeDefault,
			storage.DownloadChuckSizeDefault)},
		spanSize: storage.DownloadChuckSizeDefault,
		cc:       1,
	}

	return storage.NewSplitMergeMW(m, cfg.GetMaxObjSizeGB()), nil
}

func NewWithDownloader(
	cfg *Config, node string, l log.LogEvent,
	cc, bufSizeMb, spanSizeMb int,
) (storage.Storage, error) {
	m, err := newMinio(cfg, node, l)
	if err != nil {
		return nil, err
	}

	arenaSize, spanSize, cc := storage.DownloadOpts(cc, bufSizeMb, spanSizeMb)
	m.log.Debug("download max buf %d (arena %d, span %d, concurrency %d)",
		arenaSize*cc, arenaSize, spanSize, cc)

	var arenas []*storage.Arena
	for range cc {
		arenas = append(arenas, storage.NewArena(arenaSize, spanSize))
	}

	m.d = &Download{
		arenas:   arenas,
		spanSize: spanSize,
		cc:       cc,
		stat:     storage.NewDownloadStat(cc, arenaSize, spanSize),
	}

	return storage.NewSplitMergeMW(m, cfg.GetMaxObjSizeGB()), nil
}

func newMinio(cfg *Config, n string, l log.LogEvent) (*Minio, error) {
	err := cfg.Cast()
	if err != nil {
		return nil, errors.Wrap(err, "set defaults")
	}
	if l == nil {
		l = log.DiscardEvent
	}

	var creds *credentials.Credentials
	if cfg.Credentials.SigVer == "V2" {
		creds = credentials.NewStaticV2(
			cfg.Credentials.AccessKeyID,
			cfg.Credentials.SecretAccessKey,
			cfg.Credentials.SessionToken,
		)
	} else {
		creds = credentials.NewStaticV4(
			cfg.Credentials.AccessKeyID,
			cfg.Credentials.SecretAccessKey,
			cfg.Credentials.SessionToken,
		)
	}

	var transport http.RoundTripper
	if cfg.InsecureSkipTLSVerify {
		tr, err := minio.DefaultTransport(cfg.Secure)
		if err != nil {
			return nil, errors.Wrap(err, "transport for InsecureSkipTLSVerify")
		}
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		transport = tr
	}

	bucketLookup := minio.BucketLookupAuto
	if cfg.ForcePathStyle != nil {
		if *cfg.ForcePathStyle {
			bucketLookup = minio.BucketLookupPath
		} else {
			bucketLookup = minio.BucketLookupDNS
		}
	}

	cl, err := minio.New(cfg.resolveEndpointURL(n), &minio.Options{
		Creds:        creds,
		Secure:       cfg.Secure,
		Region:       cfg.Region,
		MaxRetries:   cfg.Retryer.NumMaxRetries,
		Transport:    transport,
		BucketLookup: bucketLookup,
	})
	if err != nil {
		return nil, errors.Wrap(err, "minio session")
	}

	if cfg.DebugTrace {
		cl.TraceOn(l.GetLogger())
	}

	return &Minio{
		cfg:  cfg,
		node: n,
		log:  l,
		cl:   cl,
	}, nil
}

func (*Minio) Type() storage.Type {
	return storage.Minio
}

func (m *Minio) Save(name string, data io.Reader, options ...storage.Option) error {
	opts := storage.GetDefaultOpts()
	for _, opt := range options {
		if err := opt(opts); err != nil {
			return errors.Wrap(err, "processing options for save")
		}
	}

	partSize := storage.ComputePartSize(
		opts.Size,
		defaultPartSize,
		minPartSize,
		maxUploadParts,
		m.cfg.PartSize,
	)

	if m.log != nil && opts.UseLogger {
		m.log.Debug("uploading %q [size hint: %v (%v); part size: %v (%v)]",
			name,
			opts.Size,
			storage.PrettySize(opts.Size),
			partSize,
			storage.PrettySize(partSize))
	}

	putOpts := minio.PutObjectOptions{
		PartSize:   uint64(partSize),
		NumThreads: uint(max(runtime.NumCPU()/2, 1)),
	}
	_, err := m.cl.PutObject(
		context.Background(),
		m.cfg.Bucket,
		path.Join(m.cfg.Prefix, name),
		data,
		-1,
		putOpts,
	)

	return errors.Wrap(err, "upload using minio")
}

func (m *Minio) FileStat(name string) (storage.FileInfo, error) {
	objectName := path.Join(m.cfg.Prefix, name)

	object, err := m.cl.StatObject(
		context.Background(),
		m.cfg.Bucket,
		objectName,
		minio.StatObjectOptions{})
	if err != nil {
		respErr := minio.ToErrorResponse(err)
		if respErr.Code == "NoSuchKey" || respErr.Code == "NotFound" {
			return storage.FileInfo{}, storage.ErrNotExist
		}

		return storage.FileInfo{}, errors.Wrap(err, "get using minio")
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

func (m *Minio) List(prefix, suffix string) ([]storage.FileInfo, error) {
	ctx := context.Background()

	prfx := path.Join(m.cfg.Prefix, prefix)
	if prfx != "" && !strings.HasSuffix(prfx, "/") {
		prfx += "/"
	}

	var files []storage.FileInfo
	for obj := range m.cl.ListObjects(ctx, m.cfg.Bucket, minio.ListObjectsOptions{
		Prefix:    prfx,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, errors.Wrap(obj.Err, "list using minio")
		}

		name := strings.TrimPrefix(obj.Key, prfx)
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

func (m *Minio) Delete(name string) error {
	if _, err := m.FileStat(name); err == storage.ErrNotExist {
		return err
	}

	ctx := context.Background()
	objName := path.Join(m.cfg.Prefix, name)

	err := m.cl.RemoveObject(ctx, m.cfg.Bucket, objName, minio.RemoveObjectOptions{})
	if err != nil {
		respErr := minio.ToErrorResponse(err)
		if respErr.Code == "NoSuchKey" || respErr.Code == "NotFound" {
			return storage.ErrNotExist
		}

		return errors.Wrap(err, "delete using minio")
	}

	return nil
}

func (m *Minio) Copy(src, dst string) error {
	if _, err := m.FileStat(src); err == storage.ErrNotExist {
		return err
	}

	ctx := context.Background()
	_, err := m.cl.CopyObject(ctx,
		minio.CopyDestOptions{
			Bucket: m.cfg.Bucket,
			Object: path.Join(m.cfg.Prefix, dst),
		},
		minio.CopySrcOptions{
			Bucket: m.cfg.Bucket,
			Object: path.Join(m.cfg.Prefix, src),
		},
	)

	return errors.Wrap(err, "copy using minio")
}
