package azure

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/Azure/azure-storage-blob-go/azblob"
	"github.com/pkg/errors"

	"github.com/percona/percona-backup-mongodb/pbm/log"
	"github.com/percona/percona-backup-mongodb/pbm/storage"
)

const (
	BlobURL = "https://%s.blob.core.windows.net/%s"

	defaultUploadBuff    = 10 << 20 // 10Mb
	defaultUploadMaxBuff = 5

	defaultRetries = 10
)

type Conf struct {
	Account     string      `bson:"account" json:"account,omitempty" yaml:"account,omitempty"`
	Container   string      `bson:"container" json:"container,omitempty" yaml:"container,omitempty"`
	Prefix      string      `bson:"prefix" json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Credentials Credentials `bson:"credentials" json:"-" yaml:"credentials"`
}

type Credentials struct {
	Key string `bson:"key" json:"key,omitempty" yaml:"key,omitempty"`
}

type Blob struct {
	opts Conf
	log  *log.Event
	url  *url.URL
	c    azblob.ContainerURL
}

func New(opts Conf, l *log.Event) (*Blob, error) {
	u, err := url.Parse(fmt.Sprintf(BlobURL, opts.Account, opts.Container))
	if err != nil {
		return nil, errors.Wrap(err, "parse options")
	}

	b := &Blob{
		opts: opts,
		log:  l,
		url:  u,
	}

	b.c, err = b.container()
	if err != nil {
		return nil, errors.Wrap(err, "init container")
	}

	return b, nil
}

func (b *Blob) Save(name string, data io.Reader, sizeb int) error {
	_, err := azblob.UploadStreamToBlockBlob(
		context.TODO(),
		data,
		b.c.NewBlockBlobURL(path.Join(b.opts.Prefix, name)),
		azblob.UploadStreamToBlockBlobOptions{BufferSize: defaultUploadBuff, MaxBuffers: defaultUploadMaxBuff},
	)

	return err
}

func (b *Blob) List(prefix, suffix string) ([]storage.FileInfo, error) {
	prfx := path.Join(b.opts.Prefix, prefix)

	if prfx != "" && !strings.HasSuffix(prfx, "/") {
		prfx = prfx + "/"
	}

	var files []storage.FileInfo
	for m := (azblob.Marker{}); m.NotDone(); {
		l, err := b.c.ListBlobsFlatSegment(context.TODO(), m, azblob.ListBlobsSegmentOptions{Prefix: prfx})
		if err != nil {
			return nil, errors.Wrap(err, "list segment")
		}
		m = l.NextMarker

		for _, b := range l.Segment.BlobItems {
			var sz int64
			if b.Properties.ContentLength != nil {
				sz = *b.Properties.ContentLength
			}
			f := b.Name
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

func (b *Blob) FileStat(name string) (inf storage.FileInfo, err error) {
	p, err := b.c.NewBlockBlobURL(path.Join(b.opts.Prefix, name)).GetProperties(context.TODO(), azblob.BlobAccessConditions{}, azblob.ClientProvidedKeyOptions{})
	if err != nil {
		if isNotFound(err) {
			return inf, storage.ErrNotExist
		}
		return inf, errors.Wrap(err, "get properties")
	}

	inf.Name = name
	inf.Size = p.ContentLength()

	if inf.Size == 0 {
		return inf, storage.ErrEmpty
	}

	return inf, nil
}

func (b *Blob) SourceReader(name string) (io.ReadCloser, error) {
	o, err := b.c.NewBlockBlobURL(path.Join(b.opts.Prefix, name)).Download(context.TODO(), 0, 0, azblob.BlobAccessConditions{}, false, azblob.ClientProvidedKeyOptions{})
	if err != nil {
		return nil, errors.Wrap(err, "download object")
	}

	return o.Body(azblob.RetryReaderOptions{MaxRetryRequests: defaultRetries}), nil
}

func (b *Blob) Delete(name string) error {
	_, err := b.c.NewBlockBlobURL(path.Join(b.opts.Prefix, name)).Delete(context.TODO(), azblob.DeleteSnapshotsOptionNone, azblob.BlobAccessConditions{})
	if err != nil {
		if isNotFound(err) {
			return storage.ErrNotExist
		}
		return errors.Wrap(err, "delete object")
	}

	return nil
}

func (b *Blob) container() (azblob.ContainerURL, error) {
	cred, err := azblob.NewSharedKeyCredential(b.opts.Account, b.opts.Credentials.Key)
	if err != nil {
		return azblob.ContainerURL{}, errors.Wrap(err, "create credentials")
	}
	p := azblob.NewPipeline(cred, azblob.PipelineOptions{
		Retry: azblob.RetryOptions{
			MaxTries:   defaultRetries,
			TryTimeout: time.Minute * time.Duration(2*defaultUploadBuff/(1<<20)), //0.5Mb/sec
		},
	})

	//  azblob.NewServiceURL(*b.url, p).NewContainerURL(b.opts.Container)
	curl := azblob.NewContainerURL(*b.url, p)
	_, err = curl.Create(context.TODO(), azblob.Metadata{}, azblob.PublicAccessNone)
	if stgErr, ok := err.(azblob.StorageError); ok {
		switch stgErr.ServiceCode() {
		case azblob.ServiceCodeContainerAlreadyExists:
			return curl, nil
		default:
			return curl, errors.Wrapf(err, "ensure container %s", b.url)
		}
	}

	return curl, errors.Wrapf(err, "unknown error ensuring container %s", b.url)
}

func isNotFound(err error) bool {
	if stgErr, ok := err.(azblob.StorageError); ok {
		return stgErr.ServiceCode() == azblob.ServiceCodeBlobNotFound
	}

	return false
}
