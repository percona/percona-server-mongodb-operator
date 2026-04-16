package azure

import (
	"context"
	"io"
	"net/http"
	"path"
	"runtime"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob"
	"github.com/Azure/azure-sdk-for-go/sdk/storage/azblob/blob"

	"github.com/percona/percona-backup-mongodb/pbm/errors"
	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

const (
	BlobURL = "https://%s.blob.core.windows.net"

	defaultUploadBuff = 10 << 20 // 10Mb

	defaultMaxRetries    = 3
	defaultMinRetryDelay = 800 * time.Millisecond
	defaultMaxRetryDelay = 60 * time.Second

	maxBlocks = 50_000

	defaultMaxObjSizeGB = 194560 // 190 TB
)

type Blob struct {
	cfg  *Config
	node string
	log  log.LogEvent
	c    *azblob.Client
}

func New(cfg *Config, node string, l log.LogEvent) (storage.Storage, error) {
	err := cfg.Cast()
	if err != nil {
		return nil, errors.Wrap(err, "set defaults")
	}
	if l == nil {
		l = log.DiscardEvent
	}
	b := &Blob{
		cfg:  cfg,
		node: node,
		log:  l,
	}

	b.c, err = b.client()
	if err != nil {
		return nil, errors.Wrap(err, "init container")
	}

	return storage.NewSplitMergeMW(b, cfg.GetMaxObjSizeGB()), nil
}

func (b *Blob) client() (*azblob.Client, error) {
	cred, err := azblob.NewSharedKeyCredential(b.cfg.Account, b.cfg.Credentials.Key)
	if err != nil {
		return nil, errors.Wrap(err, "create credentials")
	}

	opts := &azblob.ClientOptions{}
	opts.Retry = policy.RetryOptions{
		MaxRetries:    b.cfg.Retryer.NumMaxRetries,
		RetryDelay:    b.cfg.Retryer.MinRetryDelay,
		MaxRetryDelay: b.cfg.Retryer.MaxRetryDelay,
	}
	epURL := b.cfg.resolveEndpointURL(b.node)
	return azblob.NewClientWithSharedKeyCredential(epURL, cred, opts)
}

func (*Blob) Type() storage.Type {
	return storage.Azure
}

func (b *Blob) Save(name string, data io.Reader, options ...storage.Option) error {
	opts := storage.GetDefaultOpts()
	for _, opt := range options {
		if err := opt(opts); err != nil {
			return errors.Wrap(err, "processing options for save")
		}
	}

	bufsz := defaultUploadBuff
	if opts.Size > 0 {
		ps := int(opts.Size / maxBlocks * 11 / 10) // add 10% just in case
		if ps > bufsz {
			bufsz = ps
		}
	}

	cc := max(runtime.NumCPU()/2, 1)

	if b.log != nil && opts.UseLogger {
		b.log.Debug("BufferSize is set to %d (~%dMb) | %d", bufsz, bufsz>>20, opts.Size)
	}

	_, err := b.c.UploadStream(context.TODO(),
		b.cfg.Container,
		path.Join(b.cfg.Prefix, name),
		data,
		&azblob.UploadStreamOptions{
			BlockSize:   int64(bufsz),
			Concurrency: cc,
		})

	return err
}

func (b *Blob) List(prefix, suffix string) ([]storage.FileInfo, error) {
	prfx := path.Join(b.cfg.Prefix, prefix)

	if prfx != "" && !strings.HasSuffix(prfx, "/") {
		prfx += "/"
	}

	pager := b.c.NewListBlobsFlatPager(b.cfg.Container, &azblob.ListBlobsFlatOptions{
		Prefix: &prfx,
	})

	var files []storage.FileInfo
	for pager.More() {
		l, err := pager.NextPage(context.TODO())
		if err != nil {
			return nil, errors.Wrap(err, "list segment")
		}

		for _, b := range l.Segment.BlobItems {
			if b.Name == nil {
				return files, errors.Errorf("blob returned nil Name for item %v", b)
			}
			var sz int64
			if b.Properties.ContentLength != nil {
				sz = *b.Properties.ContentLength
			}
			f := *b.Name
			f = strings.TrimPrefix(f, prfx)
			if len(f) == 0 {
				continue
			}
			if f[0] == '/' {
				f = f[1:]
			}

			if strings.HasSuffix(f, suffix) {
				files = append(files, storage.FileInfo{
					Name: f,
					Size: sz,
				})
			}
		}
	}

	return files, nil
}

func (b *Blob) FileStat(name string) (storage.FileInfo, error) {
	inf := storage.FileInfo{}

	p, err := b.c.ServiceClient().
		NewContainerClient(b.cfg.Container).
		NewBlockBlobClient(path.Join(b.cfg.Prefix, name)).
		GetProperties(context.TODO(), nil)
	if err != nil {
		if isNotFound(err) {
			return inf, storage.ErrNotExist
		}
		return inf, errors.Wrap(err, "get properties")
	}

	inf.Name = name
	if p.ContentLength != nil {
		inf.Size = *p.ContentLength
	}

	if inf.Size == 0 {
		return inf, storage.ErrEmpty
	}

	return inf, nil
}

func (b *Blob) Copy(src, dst string) error {
	to := b.c.ServiceClient().NewContainerClient(b.cfg.Container).NewBlockBlobClient(path.Join(b.cfg.Prefix, dst))
	from := b.c.ServiceClient().NewContainerClient(b.cfg.Container).NewBlockBlobClient(path.Join(b.cfg.Prefix, src))
	r, err := to.StartCopyFromURL(context.TODO(), from.BlobClient().URL(), nil)
	if err != nil {
		return errors.Wrap(err, "start copy")
	}

	if r.CopyStatus == nil {
		return errors.New("undefined copy status")
	}
	status := *r.CopyStatus
	for status == blob.CopyStatusTypePending {
		time.Sleep(time.Second * 2)
		p, err := to.GetProperties(context.TODO(), nil)
		if err != nil {
			return errors.Wrap(err, "get copy status")
		}
		if r.CopyStatus == nil {
			return errors.New("undefined copy status")
		}
		status = *p.CopyStatus
	}

	switch status {
	case blob.CopyStatusTypeSuccess:
		return nil

	case blob.CopyStatusTypeAborted:
		return errors.New("copy aborted")
	case blob.CopyStatusTypeFailed:
		return errors.New("copy failed")
	default:
		return errors.Errorf("undefined status")
	}
}

func (b *Blob) DownloadStat() storage.DownloadStat {
	return storage.DownloadStat{}
}

func (b *Blob) SourceReader(name string) (io.ReadCloser, error) {
	o, err := b.c.DownloadStream(context.TODO(), b.cfg.Container, path.Join(b.cfg.Prefix, name), nil)
	if err != nil {
		if isNotFound(err) {
			return nil, storage.ErrNotExist
		}
		return nil, errors.Wrap(err, "download object")
	}

	rr := o.NewRetryReader(context.TODO(), &azblob.RetryReaderOptions{
		EarlyCloseAsError: true,
		OnFailedRead: func(failureCount int32, lastError error, rnge azblob.HTTPRange, willRetry bool) {
			// failureCount is reset on each call to Read(), so repeats of "attempt 1" are expected
			b.log.Debug("Read from Azure failed (attempt %d): %v, retrying: %v", failureCount, lastError, willRetry)
		},
	})
	return rr, nil
}

func (b *Blob) Delete(name string) error {
	_, err := b.c.DeleteBlob(context.TODO(), b.cfg.Container, path.Join(b.cfg.Prefix, name), nil)
	if err != nil {
		if isNotFound(err) {
			return storage.ErrNotExist
		}
		return errors.Wrap(err, "delete object")
	}

	return nil
}

func isNotFound(err error) bool {
	var stgErr *azcore.ResponseError
	if errors.As(err, &stgErr) {
		return stgErr.StatusCode == http.StatusNotFound
	}

	return false
}
